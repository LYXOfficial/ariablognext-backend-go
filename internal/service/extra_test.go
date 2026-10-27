package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/auth"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/platform"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store/fake"
)

// 补齐：详情/相关、说说列表、主题取色转发、摘要生成分支、
// 邮件通知（未配置 SMTP 时的降级）、AttachLogin。

func TestPostDetailAndRelated(t *testing.T) {
	svc, st := newService(t)
	ctx := context.Background()
	st.SetPosts([]model.Post{
		{Slug: "a", Title: "A", Category: "技术", Tags: []string{"go"}, PublishTime: 1750000000},
		{Slug: "b", Title: "B", Tags: []string{"go"}, PublishTime: 1749990000},
		{Slug: "c", Title: "C", PublishTime: 1749980000}, // 无标签
	})
	if p, err := svc.Posts.Detail(ctx, "a"); err != nil || p.Slug != "a" {
		t.Errorf("详情异常：%v %v", p, err)
	}
	if _, err := svc.Posts.Detail(ctx, "nope"); err == nil ||
		!strings.Contains(err.Error(), "post.not_found") {
		t.Errorf("不存在应 404：%v", err)
	}
	// related：共有 tag 的 b，排除自身
	rel, err := svc.Posts.Related(ctx, "a", 6)
	if err != nil || len(rel) != 1 || rel[0].Slug != "b" {
		t.Errorf("相关文章异常：%+v %v", rel, err)
	}
	// 无标签文章：空切片不是 nil
	rel, err = svc.Posts.Related(ctx, "c", 6)
	if err != nil || rel == nil || len(rel) != 0 {
		t.Errorf("无标签应返回空切片：%v %v", rel, err)
	}
}

func TestSpeaksListAndSiteTime(t *testing.T) {
	svc, st := newService(t)
	ctx := context.Background()
	st.SetSpeaks([]model.Speak{{Time: 3}, {Time: 1}, {Time: 2}})
	page, err := svc.Speaks.List(ctx, store.ListFilter{Page: 1, PageSize: 2})
	if err != nil || page.Count != 3 || len(page.Items) != 2 || page.Items[0].Time != 3 {
		t.Errorf("说说列表异常：%+v %v", page, err)
	}
	// site：缺省当前时间 / 自定义
	if out, err := svc.Sites.UpdateTime(ctx, nil); err != nil || out["latestUpdateTime"] == nil {
		t.Errorf("缺省更新时间异常：%v %v", out, err)
	}
	custom := int64(1700000000)
	if out, err := svc.Sites.SetSiteUpdateTime(ctx, &custom); err != nil || out["latestUpdateTime"] != custom {
		t.Errorf("自定义时间异常：%v %v", out, err)
	}
	if info, err := svc.Sites.Info(ctx); err != nil || info["latestUpdateTime"] != custom {
		t.Errorf("站点信息异常：%v %v", info, err)
	}
}

func TestLinkListAndThemeColorForward(t *testing.T) {
	svc, st := newService(t)
	ctx := context.Background()
	st.SetGroups([]model.LinkGroup{
		{Name: "b", Order: 2, Links: []model.Link{{ID: "1"}}},
		{Name: "a", Order: 1, Links: []model.Link{{ID: "2"}, {ID: "3"}}},
	})
	groups, n, err := svc.Links.List(ctx)
	if err != nil || len(groups) != 2 || n != 3 {
		t.Errorf("友链列表异常：%d %d %v", len(groups), n, err)
	}
	if groups[0].Name != "a" {
		t.Errorf("应按 order 升序：%v", groups[0].Name)
	}
	// theme-color：非法 URL 在 SSRF 校验阶段被拒（不发起网络请求）
	if _, err := svc.Links.ThemeColor(ctx, "ftp://x/y.png"); err == nil ||
		!strings.Contains(err.Error(), "validation.invalid_field") {
		t.Errorf("非法协议应拒绝：%v", err)
	}
}

func TestSummaryGenerateBranches(t *testing.T) {
	svc, st := newService(t)
	ctx := context.Background()
	st.SetPosts([]model.Post{{Slug: "a", Title: "A"}})
	// 未启用 LLM
	if _, err := svc.Posts.SummaryGenerate(ctx, "a"); err == nil ||
		!strings.Contains(err.Error(), "upstream.failed") {
		t.Errorf("未启用应 upstream.failed：%v", err)
	}
	// 启用 LLM 但地址不可达：错误应带 upstream 信息（不打真实外网）
	cfg := serviceConfig()
	cfg.AI.Summary = summaryCfgUnreachable()
	svc2 := NewSet(cfg, st)
	_, err := svc2.Posts.SummaryGenerate(ctx, "a")
	if err == nil {
		t.Fatal("不可达应报错")
	}
	if !strings.Contains(err.Error(), "upstream.failed") {
		t.Errorf("应 upstream.failed：%v", err)
	}
	// 文章不存在（LLM 启用时）→ post.not_found
	if _, err := svc2.Posts.SummaryGenerate(ctx, "nope"); err == nil ||
		!strings.Contains(err.Error(), "post.not_found") {
		t.Errorf("不存在应 404：%v", err)
	}
}

func summaryCfgUnreachable() config.SummaryConfig {
	return config.SummaryConfig{
		Enabled: true, Endpoint: "http://127.0.0.1:1/v1/chat",
		Model: "m", APIKey: "k",
		Timeout: config.Duration(2 * time.Second), Cooldown: config.Duration(time.Minute),
	}
}

func configDuration(d time.Duration) config.Duration { return config.Duration(d) }

func mailCfgEmpty() config.MailConfig { return config.MailConfig{} }

func mailSiteCfg() config.SiteConfig {
	return config.SiteConfig{Name: "测试站", URL: "https://blog.example",
		AdminPanelURL: "https://blog.example/admin"}
}

func mailFlinkCfg() config.FlinkConfig { return config.FlinkConfig{} }

func newTestJWT() *auth.JWT { return auth.New("s", time.Hour) }

// nil2Store 造一个空 fake store（邮件降级用例只关心 mailer 行为）
func nil2Store(t *testing.T) *fake.Store { return fake.New() }

func TestMailTemplatesAndDegrade(t *testing.T) {
	// SMTP 未配置：所有通知返回 false（不 panic）
	cfg := serviceConfig()
	cfg.Mail = mailCfgEmpty()
	cfg.Flink.AdminEmail = "admin@example.com"
	svc := NewSet(cfg, nil2Store(t))
	ctx := context.Background()

	appSvc := NewApplicationService(cfg, nil2Store(t))
	if appSvc.notifyNew("站", "https://x.example", "a@b.cc", 0, "", "") {
		t.Error("SMTP 未配置时 mailSent 应为 false")
	}
	if appSvc.SendDecisionMail(DecisionNotice{Name: "站", Link: "https://x.example", Email: "a@b.cc",
		Decision: model.ApplicationStatusApproved}) {
		t.Error("SMTP 未配置时审批邮件应为 false")
	}
	// rejected 且无理由：不发（即使 SMTP 可用也返回 false）
	if appSvc.SendDecisionMail(DecisionNotice{Email: "a@b.cc", Decision: model.ApplicationStatusRejected}) {
		t.Error("拒绝且无理由应不发")
	}
	// triggerScreening：未启用 → false
	if appSvc.triggerScreening("id", "https://x.example", "站", "") {
		t.Error("初筛未启用应为 false")
	}
	_ = svc
	_ = ctx
}

func TestAttachLogin(t *testing.T) {
	svc, st := newService(t)
	svc.AttachLogin(st.Users(), newTestJWT())
	if svc.Login == nil {
		t.Fatal("AttachLogin 后 Login 不应为 nil")
	}
}

func TestMailTemplatesRenderEscapes(t *testing.T) {
	tpl := platform.NewMailTemplates(mailSiteCfg(), mailFlinkCfg())
	// XSS 输入必须被转义（邮件里出现原始 <script> 就是漏洞）
	name := `<script>alert(1)</script>`
	_, _, html := tpl.ApplicationReceived(name, "https://x.example", false, "", "")
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Error("邮件模板未转义用户输入")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Error("应转义成 HTML 实体")
	}
	// 四类邮件都产出非空 HTML 与主题（样式复刻的冒烟）
	_, _, h1 := tpl.ApplicationApproved("站", "https://x.example", "友达", false, "", "")
	_, _, h2 := tpl.ApplicationRejected("站", "https://x.example", "理由", "")
	_, _, h3 := tpl.AdminNewApplication("站", "https://x.example", "a@b.cc", true, "", "")
	for i, h := range []string{html, h1, h2, h3} {
		if !strings.Contains(h, "linear-gradient(135deg,#f79dbc,#ef859d)") {
			t.Errorf("第 %d 封邮件缺粉色渐变头部（样式复刻失败）", i+1)
		}
		if !strings.Contains(h, "border-radius:18px") {
			t.Errorf("第 %d 封邮件缺圆角卡片", i+1)
		}
	}
}
