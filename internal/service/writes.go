package service

import (
	"context"
	"strings"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/textutil"
)

// plainContentLimit 是存量契约：plainContent 在库里截断到 201 字
// （design-notes §2.5）。写入时保持同构，别让新数据破坏这个不变量。
const plainContentLimit = 201

// ---------------- 文章写操作 ----------------

// CreatePost 新建文章（只写元信息，正文走 UpdateContent——与旧 addPost 一致）。
// slug 冲突靠 unique index 拦截，绝不先 Count 再 Insert。
func (s *PostService) CreatePost(ctx context.Context, p model.Post) (map[string]any, error) {
	if p.Slug == "" {
		return nil, apperr.New(apperr.ValidationMissingField, "缺少 slug",
			fieldDetail("slug", "必填"))
	}
	p.PlainContent = ""
	p.SearchText = textutil.BuildSearchText(p.Title, "")
	if err := s.posts.Create(ctx, p); err != nil {
		if err == store.ErrDuplicate {
			return nil, apperr.New(apperr.PostSlugConflict, "这个 slug 已经有文章了，换一个试试", nil)
		}
		return nil, err
	}
	return map[string]any{"slug": p.Slug}, nil
}

// UpdatePostMeta 局部更新元信息。
func (s *PostService) UpdatePostMeta(ctx context.Context, slug string, m model.PostMeta) error {
	if slug == "" {
		return apperr.New(apperr.ValidationMissingField, "缺少 slug", fieldDetail("slug", "必填"))
	}
	if err := s.posts.UpdateMeta(ctx, slug, m); err != nil {
		if err == store.ErrNotFound {
			return apperr.New(apperr.PostNotFound, "文章不存在或已被删除", nil)
		}
		return err
	}
	return nil
}

// UpdatePostContent 只改正文：服务端重算 wordCount / plainContent / searchText。
// 算派生字段是这里（service）的职责，仓储只管存——保持分层。
func (s *PostService) UpdatePostContent(ctx context.Context, slug, markdown string) (map[string]any, error) {
	if slug == "" {
		return nil, apperr.New(apperr.ValidationMissingField, "缺少 slug", fieldDetail("slug", "必填"))
	}
	// 标题参与 searchText：先取当前文章标题（不存在会返回 ErrNotFound → 404）
	p, err := s.posts.BySlug(ctx, slug)
	if err == store.ErrNotFound {
		return nil, apperr.New(apperr.PostNotFound, "文章不存在或已被删除", nil)
	}
	if err != nil {
		return nil, err
	}
	// 与旧后端一致：wordCount 作用于 markdown 原文；plainContent 剥 Markdown、
	// 换行压成空格后截 201 字（updatePostMarkdown 的 .replace("\n", " ")[:201]）。
	plain := textutil.CleanMarkdown(markdown)
	wordCount := textutil.WordCount(markdown)
	err = s.posts.UpdateContent(ctx, slug, model.PostContent{
		Markdown:     markdown,
		PlainContent: truncateRunes(strings.ReplaceAll(plain, "\n", " "), plainContentLimit),
		SearchText:   textutil.BuildSearchText(p.Title, markdown),
		WordCount:    wordCount,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"wordCount": wordCount}, nil
}

// DeletePost 幂等删除：不存在的 slug 也返回 deleted=true。
func (s *PostService) DeletePost(ctx context.Context, slug string) (map[string]any, error) {
	if slug == "" {
		return nil, apperr.New(apperr.ValidationMissingField, "缺少 slug", fieldDetail("slug", "必填"))
	}
	if _, err := s.posts.Delete(ctx, slug); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

// ---------------- 草稿 ----------------

type DraftService struct {
	drafts store.DraftRepository
}

// ListDrafts 草稿列表（标题模糊搜索等价旧 searchDraftsByTitle）。
func (s *DraftService) List(ctx context.Context, f store.DraftFilter) (store.Page[model.Draft], error) {
	return s.drafts.Page(ctx, f)
}

// DetailDraft 草稿详情。
func (s *DraftService) Detail(ctx context.Context, slug string) (*model.Draft, error) {
	d, err := s.drafts.BySlug(ctx, slug)
	if err == store.ErrNotFound {
		return nil, apperr.New(apperr.DraftNotFound, "草稿不存在或已被删除", nil)
	}
	return d, err
}

// Slugs 全部草稿 slug。
func (s *DraftService) Slugs(ctx context.Context) ([]string, error) {
	return s.drafts.Slugs(ctx)
}

// CreateDraft 新建草稿（与文章一致：只写元信息）。
func (s *DraftService) CreateDraft(ctx context.Context, d model.Draft) (map[string]any, error) {
	if d.Slug == "" {
		return nil, apperr.New(apperr.ValidationMissingField, "缺少 slug", fieldDetail("slug", "必填"))
	}
	if err := s.drafts.Create(ctx, d); err != nil {
		if err == store.ErrDuplicate {
			return nil, apperr.New(apperr.DraftSlugConflict, "这个 slug 已经有草稿了，换一个试试", nil)
		}
		return nil, err
	}
	return map[string]any{"slug": d.Slug}, nil
}

// UpdateDraftMeta 局部更新草稿元信息。
func (s *DraftService) UpdateDraftMeta(ctx context.Context, slug string, m model.PostMeta) error {
	if slug == "" {
		return apperr.New(apperr.ValidationMissingField, "缺少 slug", fieldDetail("slug", "必填"))
	}
	if err := s.drafts.UpdateMeta(ctx, slug, m); err != nil {
		if err == store.ErrNotFound {
			return apperr.New(apperr.DraftNotFound, "草稿不存在或已被删除", nil)
		}
		return err
	}
	return nil
}

// UpdateDraftContent 只改草稿正文。草稿的 md/plain/wordCount 可空（库里 4/8 条缺），
// 但一旦写了正文就按完整形态存。
func (s *DraftService) UpdateDraftContent(ctx context.Context, slug, markdown string) (map[string]any, error) {
	if slug == "" {
		return nil, apperr.New(apperr.ValidationMissingField, "缺少 slug", fieldDetail("slug", "必填"))
	}
	d, err := s.drafts.BySlug(ctx, slug)
	if err == store.ErrNotFound {
		return nil, apperr.New(apperr.DraftNotFound, "草稿不存在或已被删除", nil)
	}
	if err != nil {
		return nil, err
	}
	plain := textutil.CleanMarkdown(markdown)
	wordCount := textutil.WordCount(markdown)
	if err := s.drafts.UpdateContent(ctx, slug, model.PostContent{
		Markdown:     markdown,
		PlainContent: truncateRunes(strings.ReplaceAll(plain, "\n", " "), plainContentLimit),
		SearchText:   textutil.BuildSearchText(d.Title, markdown),
		WordCount:    wordCount,
	}); err != nil {
		return nil, err
	}
	return map[string]any{"wordCount": wordCount}, nil
}

// DeleteDraft 幂等删除草稿。
func (s *DraftService) DeleteDraft(ctx context.Context, slug string) (map[string]any, error) {
	if slug == "" {
		return nil, apperr.New(apperr.ValidationMissingField, "缺少 slug", fieldDetail("slug", "必填"))
	}
	if _, err := s.drafts.Delete(ctx, slug); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

// ---------------- 说说写操作 ----------------

// CreateSpeak 新建说说：content 必填；time 缺省取当前秒。
func (s *SpeakService) CreateSpeak(ctx context.Context, content string, ts *int64) error {
	if content == "" {
		return apperr.New(apperr.ValidationMissingField, "缺少 content", fieldDetail("content", "必填"))
	}
	var when int64
	if ts != nil {
		when = *ts
	} else {
		when = time.Now().Unix()
	}
	// 说说的 plainContent 口径与文章不同：replace_and_remove_tags（<a>→[链接] 等）
	plain := textutil.SpeakPlainContent(content)
	if err := s.speaks.Create(ctx, model.Speak{
		Time: when, Content: content, PlainContent: plain,
	}); err != nil {
		if err == store.ErrDuplicate {
			return apperr.InvalidField("time", "该时间戳已有一条说说")
		}
		return err
	}
	return nil
}

// UpdateSpeak 按 time 定位更新说说内容。
func (s *SpeakService) UpdateSpeak(ctx context.Context, when int64, content string) error {
	if content == "" {
		return apperr.New(apperr.ValidationMissingField, "缺少 content", fieldDetail("content", "必填"))
	}
	ok, err := s.speaks.Update(ctx, when, content, textutil.SpeakPlainContent(content))
	if err != nil {
		return err
	}
	if !ok {
		return apperr.New(apperr.ValidationInvalidField, "该时间戳的说说不存在",
			fieldDetail("time", "不存在"))
	}
	return nil
}

// DeleteSpeak 幂等删除说说。
func (s *SpeakService) DeleteSpeak(ctx context.Context, when int64) error {
	_, err := s.speaks.Delete(ctx, when)
	return err
}

// ---------------- 友链写操作（P2 部分） ----------------

// AddLink 向分组追加友链。
func (s *LinkService) AddLink(ctx context.Context, group string, l model.Link) error {
	if group == "" || l.ID == "" || l.Name == "" || l.URL == "" {
		return apperr.New(apperr.ValidationMissingField, "缺少必填字段（group/id/name/url）", nil)
	}
	if err := s.links.AddLink(ctx, group, l); err != nil {
		if err == store.ErrNotFound {
			return apperr.New(apperr.LinkNotFound, "分组不存在，请先创建分组", nil)
		}
		if err == store.ErrDuplicate {
			return apperr.New(apperr.ApplicationDuplicate, "该友链已收录（网址或 ID 重复）", nil)
		}
		return err
	}
	return nil
}

// UpdateLink 更新友链；分组或链接不存在返回 (false→link.not_found)。
func (s *LinkService) UpdateLink(ctx context.Context, group, id string, patch store.LinkPatch) error {
	matched, err := s.links.UpdateLink(ctx, group, id, patch)
	if err != nil {
		return err
	}
	if !matched {
		return apperr.New(apperr.LinkNotFound, "分组或友链不存在", nil)
	}
	return nil
}

// DeleteLink 删除友链（幂等）。
func (s *LinkService) DeleteLink(ctx context.Context, group, id string) (map[string]any, error) {
	_, err := s.links.DeleteLink(ctx, group, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

// SetLatency 批量更新友链延迟（内部 token 上报），返回实际更新条数。
func (s *LinkService) SetLatency(ctx context.Context, updates []store.LatencyUpdate) (int64, error) {
	if len(updates) == 0 {
		return 0, nil
	}
	return s.links.SetLatency(ctx, updates)
}

// MoveLink 移动友链到另一分组（原子，仓储实现保证失败回滚）。
func (s *LinkService) MoveLink(ctx context.Context, fromGroup, toGroup, id string) error {
	if fromGroup == "" || toGroup == "" || id == "" {
		return apperr.New(apperr.ValidationMissingField, "缺少必填字段（fromGroup/toGroup/id）", nil)
	}
	if err := s.links.MoveLink(ctx, fromGroup, toGroup, id); err != nil {
		if err == store.ErrNotFound {
			return apperr.New(apperr.LinkNotFound, "源分组或友链不存在", nil)
		}
		return err
	}
	return nil
}

// AddGroup 新建分组（重名 → link.group_conflict）。
func (s *LinkService) AddGroup(ctx context.Context, g model.LinkGroup) error {
	if g.Name == "" {
		return apperr.New(apperr.ValidationMissingField, "缺少分组名", fieldDetail("name", "必填"))
	}
	if err := s.links.AddGroup(ctx, g); err != nil {
		if err == store.ErrDuplicate {
			return apperr.New(apperr.LinkGroupConflict, "分组名已存在", nil)
		}
		return err
	}
	return nil
}

// UpdateGroup 更新分组（含改名）。
func (s *LinkService) UpdateGroup(ctx context.Context, oldName string, g model.LinkGroup) error {
	if oldName == "" || g.Name == "" {
		return apperr.New(apperr.ValidationMissingField, "缺少分组名（oldName/name）", nil)
	}
	matched, err := s.links.UpdateGroup(ctx, oldName, g)
	if err != nil {
		if err == store.ErrDuplicate {
			return apperr.New(apperr.LinkGroupConflict, "分组名已存在", nil)
		}
		return err
	}
	if !matched {
		return apperr.New(apperr.LinkNotFound, "分组不存在", nil)
	}
	return nil
}

// DeleteGroup 删除分组（幂等）。
func (s *LinkService) DeleteGroup(ctx context.Context, name string) (map[string]any, error) {
	_, err := s.links.DeleteGroup(ctx, name)
	if err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

// NotifyDecision 审批后通知申请人（rejected 且 reason 为空不发）。
// 入参带 avatar/descr/isModify/group：邮件模板要和旧版一致，这些都得给。
func (s *LinkService) NotifyDecision(n DecisionNotice) bool {
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

// PendingApplications 后台申请列表：按 createdAt 升序（最早的先审）。
func (s *LinkService) PendingApplications(ctx context.Context) ([]model.LinkApplication, int64, error) {
	list, err := s.apps.ListPending(ctx, 1000)
	if err != nil {
		return nil, 0, err
	}
	n, err := s.apps.CountPending(ctx)
	if err != nil {
		return nil, 0, err
	}
	localizeApplications(list, s.cfg.Location())
	return list, n, nil
}

// localizeApplications 把 createdAt/decidedAt 转成配置时区（契约：RFC3339 上海时区）。
// 库里存的是 UTC Date，转型只影响 JSON 输出，不写库。
func localizeApplications(list []model.LinkApplication, loc *time.Location) {
	for i := range list {
		list[i].CreatedAt = list[i].CreatedAt.In(loc)
		if list[i].DecidedAt != nil {
			t := list[i].DecidedAt.In(loc)
			list[i].DecidedAt = &t
		}
	}
}

// DecideApplication 审批申请。
// approved + type=0：追加到 group；approved + type=1：覆盖原友链（允许借 group 换组，
// 走 MoveLink 的原子移动）；rejected：写 reason。申请不存在/已处理 → application.not_found。
func (s *LinkService) DecideApplication(ctx context.Context, id, decision, group, reason string, overrides *store.LinkPatch) (map[string]any, error) {
	if decision != model.ApplicationStatusApproved && decision != model.ApplicationStatusRejected {
		return nil, apperr.InvalidField("decision", "只接受 approved 或 rejected")
	}
	app, err := s.apps.ByID(ctx, id)
	if err == store.ErrNotFound {
		return nil, apperr.New(apperr.ApplicationNotFound, "申请不存在或已被处理", nil)
	}
	if err != nil {
		return nil, err
	}
	if app.Status != model.ApplicationStatusPending {
		return nil, apperr.New(apperr.ApplicationNotFound, "申请不存在或已被处理", nil)
	}

	if decision == model.ApplicationStatusRejected {
		if _, err := s.apps.Decide(ctx, id, decision, reason, time.Now()); err != nil {
			return nil, err
		}
		// reason 非空才发拒绝邮件
		mailSent := s.NotifyDecision(DecisionNotice{
			Name: app.Name, Link: app.Link, Email: app.Email,
			Avatar: app.Avatar, Descr: app.Descr,
			Decision: decision, Reason: reason,
		})
		return map[string]any{"mailSent": mailSent}, nil
	}

	// approved
	switch app.Type {
	case 0: // 新增：追加到指定分组
		if group == "" {
			return nil, apperr.New(apperr.ValidationMissingField, "approved 需要指定分组", fieldDetail("group", "必填"))
		}
		l := model.Link{
			ID: app.ID, Name: app.Name, URL: app.Link, Avatar: app.Avatar,
			Description: app.Descr, Color: app.Color,
		}
		if overrides != nil {
			if overrides.Name != "" {
				l.Name = overrides.Name
			}
			if overrides.URL != "" {
				l.URL = overrides.URL
			}
			if overrides.Avatar != "" {
				l.Avatar = overrides.Avatar
			}
			if overrides.Description != "" {
				l.Description = overrides.Description
			}
			if overrides.Color != "" {
				l.Color = overrides.Color
			}
		}
		if err := s.links.AddLink(ctx, group, l); err != nil {
			if err == store.ErrNotFound {
				return nil, apperr.New(apperr.LinkNotFound, "目标分组不存在，请先创建分组", nil)
			}
			return nil, err
		}
	case 1: // 修改：覆盖原友链；允许借 group 换组（先改内容再原子移动）
		targetID := ""
		if app.TargetID != nil {
			targetID = *app.TargetID
		}
		targetGroup := ""
		if app.TargetGroup != nil {
			targetGroup = *app.TargetGroup
		}
		if targetID == "" || targetGroup == "" {
			return nil, apperr.New(apperr.LinkNotFound, "修改申请缺少目标友链信息", nil)
		}
		patch := store.LinkPatch{Name: app.Name, URL: app.Link, Avatar: app.Avatar,
			Description: app.Descr, Color: app.Color}
		if overrides != nil {
			if overrides.Name != "" {
				patch.Name = overrides.Name
			}
			if overrides.URL != "" {
				patch.URL = overrides.URL
			}
			if overrides.Avatar != "" {
				patch.Avatar = overrides.Avatar
			}
			if overrides.Description != "" {
				patch.Description = overrides.Description
			}
			if overrides.Color != "" {
				patch.Color = overrides.Color
			}
		}
		matched, err := s.links.UpdateLink(ctx, targetGroup, targetID, patch)
		if err != nil {
			return nil, err
		}
		if !matched {
			return nil, apperr.New(apperr.LinkNotFound, "要修改的友链不存在", nil)
		}
		if group != "" && group != targetGroup {
			if err := s.links.MoveLink(ctx, targetGroup, group, targetID); err != nil {
				return nil, apperr.New(apperr.LinkNotFound, "移动友链到新分组失败："+errMessage(err), nil)
			}
		}
	default:
		return nil, apperr.InvalidField("type", "申请类型未知")
	}

	if _, err := s.apps.Decide(ctx, id, decision, reason, time.Now()); err != nil {
		return nil, err
	}
	mailSent := s.NotifyDecision(DecisionNotice{
		Name: app.Name, Link: app.Link, Email: app.Email,
		Group: group, IsModify: app.Type == 1,
		Avatar: app.Avatar, Descr: app.Descr,
		Decision: decision, Reason: reason,
	})
	return map[string]any{"mailSent": mailSent}, nil
}

// SetSiteUpdateTime 写站点更新时间（胶水默认当前；dashboard 可自定义）。
func (s *SiteService) SetSiteUpdateTime(ctx context.Context, ts *int64) (map[string]any, error) {
	return s.UpdateTime(ctx, ts)
}

// ---------------- 小工具 ----------------

func fieldDetail(field, msg string) apperr.FieldDetails {
	return apperr.FieldDetails{Fields: []apperr.FieldError{{Field: field, Message: msg}}}
}

func errMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
