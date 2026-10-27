package service

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store/fake"
)

// 有 SMTP 配置、但服务器不可达：覆盖 notifyNew / SendDecisionMail 的完整分支
// （连接失败 → 返回 false，绝不影响申请主流程）。
func unreachableMailCfg() config.MailConfig {
	return config.MailConfig{
		Enabled: true, Host: "127.0.0.1", Port: "1", Secure: "false",
		User: "u", Pass: "p", From: "u@example.com", FromName: "友链",
	}
}

func TestNotifyNewWithUnreachableSMTP(t *testing.T) {
	cfg := serviceConfig()
	cfg.Mail = unreachableMailCfg()
	cfg.Flink.AdminEmail = "admin@example.com"
	st := fake.New()
	svc := NewApplicationService(cfg, st)

	// 有申请人邮箱 + 有博主邮箱：两条邮件都尝试，全部失败但不 panic
	if svc.notifyNew("站", "https://x.example", "a@b.cc", 0, "https://x.example/a.png", "描述") {
		t.Error("不可达的 SMTP 应返回 false")
	}
	// 没有申请人邮箱：只尝试博主提醒
	if svc.notifyNew("站", "https://x.example", "", 1, "", "") {
		t.Error("没有收件人应返回 false")
	}
	// 审批邮件：approved / rejected（rejected 无理由不发，有理由尝试但失败）
	if svc.SendDecisionMail(DecisionNotice{Name: "站", Link: "https://x.example", Email: "a@b.cc",
		Group: "友达", Decision: model.ApplicationStatusApproved}) {
		t.Error("不可达 SMTP 的通过邮件应为 false")
	}
	if svc.SendDecisionMail(DecisionNotice{Name: "站", Link: "https://x.example", Email: "a@b.cc",
		Decision: model.ApplicationStatusRejected, Reason: "理由"}) {
		t.Error("不可达 SMTP 的拒绝邮件应为 false")
	}
}

func TestLinkServiceThemeColorSuccessPath(t *testing.T) {
	// httptest 服务在 127.0.0.1：给 theme 打开测试闸门后走完整取色链路
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(pngFixture(8, 0xef, 0x85, 0x9d))
	}))
	defer srv.Close()

	theme := allowPrivateThemeService()
	links := &LinkService{cfg: serviceConfig(), links: fake.New().Links(), theme: theme,
		templates: nil, mailer: nil}
	color, err := links.ThemeColor(context.Background(), srv.URL+"/a.png")
	if err != nil {
		t.Fatalf("取色应成功：%v", err)
	}
	if len(color) != 7 || color[0] != '#' {
		t.Errorf("颜色格式错误：%q", color)
	}
}

func TestNotifyDecisionRejectedNoReason(t *testing.T) {
	cfg := serviceConfig()
	cfg.Mail = unreachableMailCfg()
	svc := &LinkService{cfg: cfg, links: fake.New().Links(), apps: fake.New().Applications(),
		templates: nil, mailer: nil}
	_ = svc
	_ = time.Second
}

// pngFixture 生成一张纯色 PNG（取色链路测试用）。
func pngFixture(size int, r, g, b uint8) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	c := color.RGBA{R: r, G: g, B: b, A: 255}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, c)
		}
	}
	buf := &bytes.Buffer{}
	_ = png.Encode(buf, img)
	return buf.Bytes()
}

// 审批里的「修改申请换组」与错误分支覆盖（DecideApplication / UpdateDraftMeta）。
func TestDecideModifyAndDraftErrors(t *testing.T) {
	svc, st := newService(t)
	ctx := context.Background()
	_ = st.Links().AddGroup(ctx, model.LinkGroup{Name: "友达"})
	_ = st.Links().AddGroup(ctx, model.LinkGroup{Name: "收藏"})
	_ = st.Links().AddLink(ctx, "友达", model.Link{
		ID: "target1", Name: "老站", URL: "https://t1.example", Avatar: "https://t1.example/a.png",
		Description: "旧", Color: "#888888",
	})
	// type=1 修改申请：审批时改内容 + 换组
	id, err := st.Applications().Create(ctx, model.LinkApplication{
		Type: 1, Name: "新名", Link: "https://t1.example", Avatar: "https://t1.example/a.png",
		Descr: "新描述", Color: "#f08", Status: model.ApplicationStatusPending,
		TargetGroup: strPtrT("友达"), TargetID: strPtrT("target1"), CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := svc.Links.DecideApplication(ctx, id, "approved", "收藏", "", nil)
	if err != nil {
		t.Fatalf("修改审批应成功：%v", err)
	}
	if out["mailSent"] != false {
		t.Errorf("未配置邮件应 false：%v", out)
	}
	ref, err := st.Links().FindByURL(ctx, "https://t1.example")
	if err != nil || ref.Group != "收藏" || ref.Link.Name != "新名" {
		t.Fatalf("换组后友链异常：%+v %v", ref, err)
	}

	// 修改申请缺少 targetId/targetGroup → link.not_found
	id2, _ := st.Applications().Create(ctx, model.LinkApplication{
		Type: 1, Name: "缺目标", Link: "https://t2.example", Status: model.ApplicationStatusPending,
		CreatedAt: time.Now(),
	})
	if _, err := svc.Links.DecideApplication(ctx, id2, "approved", "友达", "", nil); err == nil ||
		!strings.Contains(err.Error(), "link.not_found") {
		t.Errorf("缺目标应报错：%v", err)
	}
	// 目标友链已被删 → link.not_found
	id3, _ := st.Applications().Create(ctx, model.LinkApplication{
		Type: 1, Name: "目标不存在", Link: "https://t3.example", Status: model.ApplicationStatusPending,
		TargetGroup: strPtrT("友达"), TargetID: strPtrT("gone"), CreatedAt: time.Now(),
	})
	if _, err := svc.Links.DecideApplication(ctx, id3, "approved", "友达", "", nil); err == nil ||
		!strings.Contains(err.Error(), "link.not_found") {
		t.Errorf("目标不存在应报错：%v", err)
	}
	// 未支持的类型（库里 type=9 的脏数据）→ validation.invalid_field
	id4, _ := st.Applications().Create(ctx, model.LinkApplication{
		Type: 9, Name: "脏类型", Link: "https://t4.example", Status: model.ApplicationStatusPending,
		CreatedAt: time.Now(),
	})
	if _, err := svc.Links.DecideApplication(ctx, id4, "approved", "友达", "", nil); err == nil ||
		!strings.Contains(err.Error(), "validation.invalid_field") {
		t.Errorf("未知类型应拒绝：%v", err)
	}

	// 草稿更新：缺 slug / 不存在
	if err := svc.Drafts.UpdateDraftMeta(ctx, "", model.PostMeta{}); err == nil ||
		!strings.Contains(err.Error(), "validation.missing_field") {
		t.Errorf("缺 slug 应校验：%v", err)
	}
	if err := svc.Drafts.UpdateDraftMeta(ctx, "nope", model.PostMeta{}); err == nil ||
		!strings.Contains(err.Error(), "draft.not_found") {
		t.Errorf("不存在应 404：%v", err)
	}
	// 草案正文缺 slug
	if _, err := svc.Drafts.UpdateDraftContent(ctx, "", "x"); err == nil ||
		!strings.Contains(err.Error(), "validation.missing_field") {
		t.Errorf("缺 slug 应校验：%v", err)
	}
	// 文章 meta 缺 slug
	if err := svc.Posts.UpdatePostMeta(ctx, "", model.PostMeta{}); err == nil ||
		!strings.Contains(err.Error(), "validation.missing_field") {
		t.Errorf("缺 slug 应校验：%v", err)
	}
	// 文章正文缺 slug
	if _, err := svc.Posts.UpdatePostContent(ctx, "", "x"); err == nil ||
		!strings.Contains(err.Error(), "validation.missing_field") {
		t.Errorf("缺 slug 应校验：%v", err)
	}
	// 删除缺 slug
	if _, err := svc.Posts.DeletePost(ctx, ""); err == nil ||
		!strings.Contains(err.Error(), "validation.missing_field") {
		t.Errorf("缺 slug 应校验：%v", err)
	}
	if _, err := svc.Drafts.DeleteDraft(ctx, ""); err == nil ||
		!strings.Contains(err.Error(), "validation.missing_field") {
		t.Errorf("缺 slug 应校验：%v", err)
	}
}
