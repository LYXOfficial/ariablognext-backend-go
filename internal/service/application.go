package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/platform"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/validate"
)

// ApplicationService 是友链申请全流程：
// 校验 → 限流(中间件层) → 队列容量 → 判重 → 入库 → 触发初筛 → 发邮件。
// 业务规则与旧 flink/main.py 逐条对齐，不自创。
type ApplicationService struct {
	cfg        *config.Config
	links      store.LinkRepository
	apps       store.ApplicationRepository
	templates  *platform.MailTemplates
	mailer     *platform.Mailer
	screenRepo config.FlinkConfig
}

func NewApplicationService(cfg *config.Config, st store.Store) *ApplicationService {
	return &ApplicationService{
		cfg:        cfg,
		links:      st.Links(),
		apps:       st.Applications(),
		templates:  platform.NewMailTemplates(cfg.Site, cfg.Flink),
		mailer:     platform.NewMailer(cfg.Mail),
		screenRepo: cfg.Flink,
	}
}

// ApplyRequestBody 是 POST /links/applications/apply 的载荷（全部字段可缺省，
// 由校验层统一给出友好 400，避免框架先抛 422）。
type ApplyInput struct {
	Name      string
	Link      string
	Avatar    string
	Descr     string
	Email     string
	Color     string
	Type      int
	TargetURL string
	IP        string
}

// Apply 执行完整申请流程。返回 {"screeningTriggered": bool, "mailSent": bool}。
func (s *ApplicationService) Apply(ctx context.Context, in ApplyInput) (map[string]any, error) {
	// 归一化（与校验同一套，防止「校验用原文、入库用归一化」的漂移）
	name := validate.NormalizeText(in.Name)
	link := validate.NormalizeText(in.Link)
	avatar := validate.NormalizeText(in.Avatar)
	descr := validate.NormalizeText(in.Descr)
	email := validate.NormalizeText(in.Email)
	color := validate.NormalizeText(in.Color)
	targetURL := validate.NormalizeText(in.TargetURL)

	// ① 校验：一次报全
	if v := validate.ValidateApplication(validate.Limits{
		MaxName:        s.cfg.Flink.MaxName,
		MaxDescription: s.cfg.Flink.MaxDescription,
		MaxURL:         s.cfg.Flink.MaxURL,
	}, name, link, avatar, descr, email, color, targetURL, in.Type); len(v) > 0 {
		fields := make([]apperr.FieldError, 0, len(v))
		for _, f := range v {
			fields = append(fields, apperr.FieldError{Field: f.Field, Message: f.Message})
		}
		return nil, apperr.New(apperr.ValidationInvalidField, "申请内容有误，请修改后重新提交",
			apperr.FieldDetails{Fields: fields})
	}

	// ② 队列容量（顺带清理超期的已出结果记录）
	if err := s.ensureCapacity(ctx); err != nil {
		return nil, err
	}

	// ③ 判重与业务检查
	var targetGroup, targetID string
	if in.Type == 1 {
		g, id, err := s.checkModify(ctx, targetURL, name, link, avatar, descr, color, email)
		if err != nil {
			return nil, err
		}
		targetGroup, targetID = g, id
	} else {
		if err := s.checkNew(ctx, link, name, avatar, descr, color, email); err != nil {
			return nil, err
		}
	}

	// ④ 同一邮箱重新提交：清掉旧的已出结果记录（相当于重新排队）
	if email != "" {
		if _, err := s.apps.DeleteDecidedByEmail(ctx, email); err != nil {
			return nil, err
		}
	}

	// ⑤ 入库
	id, err := s.apps.Create(ctx, model.LinkApplication{
		Type: in.Type, Name: name, Link: link, Avatar: avatar, Descr: descr,
		Color: color, Email: email,
		TargetURL:   strPtr(targetURL),
		TargetGroup: strPtr(targetGroup),
		TargetID:    strPtr(targetID),
		Status:      model.ApplicationStatusPending,
		Reason:      "",
		CreatedAt:   time.Now(),
		IP:          in.IP,
	})
	if err != nil {
		return nil, err
	}

	// ⑥ AI 初筛（静默降级：未配置/触发失败都不影响申请）
	screeningTriggered := s.triggerScreening(id, link, name, descr)

	// ⑦ 邮件回执 + 博主提醒（静默降级，结果回给前端提示）
	mailSent := s.notifyNew(name, link, email, in.Type, avatar, descr)

	return map[string]any{"screeningTriggered": screeningTriggered, "mailSent": mailSent}, nil
}

// ensureCapacity 清理过期 + 队列满检查（application.full 429）。
func (s *ApplicationService) ensureCapacity(ctx context.Context) error {
	retention := time.Duration(s.cfg.Flink.RetentionDays) * 24 * time.Hour
	if _, err := s.apps.PurgeDecidedBefore(ctx, time.Now().Add(-retention)); err != nil {
		return err
	}
	pending, err := s.apps.CountPending(ctx)
	if err != nil {
		return err
	}
	if pending >= int64(s.cfg.Flink.PendingLimit) {
		return apperr.New(apperr.ApplicationFull, "申请列表已满，请稍后再提交", nil)
	}
	return nil
}

// checkNew：网址已收录 → application.duplicate；有完全相同的待审申请 → 同。
func (s *ApplicationService) checkNew(ctx context.Context, link, name, avatar, descr, color, email string) error {
	if _, err := s.links.FindByURL(ctx, link); err == nil {
		return apperr.New(apperr.ApplicationDuplicate,
			"该网址已经在友链里了，如需修改请提交修改申请", nil)
	}
	ident := store.ApplicationIdentity{
		Type: 0, Name: name, Link: validate.NormalizeURLText(link),
		Avatar: avatar, Descr: descr, Color: validate.NormalizeColor(color),
		Email: strings.ToLower(email), TargetID: "",
	}
	if dup, _ := s.apps.FindIdentical(ctx, ident); dup != nil {
		return apperr.New(apperr.ApplicationDuplicate,
			"已有一条内容完全相同的申请在等待审核，无需重复提交", nil)
	}
	return nil
}

// checkModify：目标存在 → 内容有变化 → 无相同待审。返回 (targetGroup, targetID)。
func (s *ApplicationService) checkModify(ctx context.Context, targetURL, name, link, avatar, descr, color, email string) (string, string, error) {
	ref, err := s.links.FindByURL(ctx, targetURL)
	if err == store.ErrNotFound {
		return "", "", apperr.New(apperr.LinkNotFound,
			"没有找到这个网址的友链，请确认填写的是当前已收录的地址", nil)
	}
	if err != nil {
		return "", "", err
	}
	// 与现网友链完全一致 = 无变化（400 application.no_change）
	current := ref.Link
	same := validate.NormalizeText(current.Name) == name &&
		validate.NormalizeURLText(current.URL) == validate.NormalizeURLText(link) &&
		validate.NormalizeText(current.Avatar) == avatar &&
		validate.NormalizeText(current.Description) == descr &&
		validate.NormalizeColor(current.Color) == validate.NormalizeColor(color)
	if same {
		return "", "", apperr.New(apperr.ApplicationNoChange,
			"内容与当前友链完全一致，无需提交修改申请", nil)
	}
	ident := store.ApplicationIdentity{
		Type: 1, Name: name, Link: validate.NormalizeURLText(link),
		Avatar: avatar, Descr: descr, Color: validate.NormalizeColor(color),
		Email: strings.ToLower(email), TargetID: current.ID,
	}
	if dup, _ := s.apps.FindIdentical(ctx, ident); dup != nil {
		return "", "", apperr.New(apperr.ApplicationDuplicate,
			"已有一条内容完全相同的申请在等待审核，无需重复提交", nil)
	}
	return ref.Group, current.ID, nil
}

// triggerScreening 触发 GitHub Actions 初筛。未配置 = 功能关闭 = false。
func (s *ApplicationService) triggerScreening(id, url, name, descr string) bool {
	sc := s.cfg.Flink.Screening
	if !sc.Enabled || sc.Repo == "" || sc.Token == "" {
		return false
	}
	payload := map[string]any{
		"id":    id,
		"url":   url,
		"name":  truncate(name, 100),
		"descr": truncate(descr, 300),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := platform.DispatchGitHub(ctx, sc.Repo, sc.Token, "flink-screening", payload); err != nil {
		// 初筛只是辅助手段，挂掉不能影响申请提交
		slogWarnOnly("触发友链初筛失败（不影响申请）", "err", err)
		return false
	}
	return true
}

// notifyNew 给申请人回执 + 给博主提醒。未配置 SMTP = 全部跳过（旧后端行为）。
func (s *ApplicationService) notifyNew(name, link, email string, kind int, avatar, descr string) bool {
	if !s.mailer.Configured() {
		return false
	}
	isModify := kind == 1
	sent := false
	if email != "" {
		subject, text, html := s.templates.ApplicationReceived(name, link, isModify, avatar, descr)
		sent = s.mailer.Send(email, subject, text, html)
	}
	if admin := s.cfg.Flink.AdminEmail; admin != "" {
		subject, text, html := s.templates.AdminNewApplication(name, link, orUnnamed(email), isModify, avatar, descr)
		// 博主提醒失败不影响「回执已发」的结果
		s.mailer.Send(admin, subject, text, html)
	}
	return sent
}

// DecisionNotice 是审批通知的入参（模板需要分组、是否修改、头像与描述）。
type DecisionNotice struct {
	Name     string
	Link     string
	Email    string
	Group    string
	IsModify bool
	Avatar   string
	Descr    string
	Decision string
	Reason   string
}

// SendDecisionMail 审批后通知申请人。rejected 且 reason 为空时不发（旧后端语义）。
func (s *ApplicationService) SendDecisionMail(n DecisionNotice) bool {
	if !s.mailer.Configured() || n.Email == "" {
		return false
	}
	if n.Decision == model.ApplicationStatusRejected {
		if n.Reason == "" {
			return false
		}
		subject, text, html := s.templates.ApplicationRejected(n.Name, n.Link, n.Reason, n.Avatar)
		return s.mailer.Send(n.Email, subject, text, html)
	}
	subject, text, html := s.templates.ApplicationApproved(n.Name, n.Link, n.Group, n.IsModify, n.Avatar, n.Descr)
	return s.mailer.Send(n.Email, subject, text, html)
}

// Screening 回写初筛结论（内部 token 路由调用）。只更新 pending。
// 参数名不能用 model（会遮蔽 model 包），用 modelName。
func (s *ApplicationService) Screening(ctx context.Context, id, verdict, backlink, summary, reason, modelName string) error {
	if verdict != "pass" && verdict != "review" {
		return apperr.InvalidField("verdict", "只接受 pass 或 review")
	}
	ok, err := s.apps.SetScreening(ctx, id, aiScreeningValue(verdict, backlink, summary, reason, modelName))
	if err != nil {
		return err
	}
	if !ok {
		return apperr.New(apperr.ApplicationNotFound, "申请不存在或已被处理", nil)
	}
	return nil
}

func aiScreeningValue(verdict, backlink, summary, reason, modelName string) model.AIScreening {
	return model.AIScreening{
		Verdict: verdict, Backlink: backlink, Summary: summary, Reason: reason, Model: modelName,
		ScreenedAt: time.Now(),
	}
}

func slogWarnOnly(msg string, args ...any) { slog.Warn(msg, args...) }

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func orUnnamed(s string) string {
	if s == "" {
		return "（未填写）"
	}
	return s
}
