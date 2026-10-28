package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store/fake"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/textutil"
)

// 写接口与申请全流程测试（fake 存储）。
// 邮件/初筛/图床等外部依赖在测试配置里全部关闭，靠返回值断言降级语义。

func TestPostWriteFlow(t *testing.T) {
	svc, st := newService(t)
	ctx := context.Background()

	// create：只写元信息，正文为空，searchText 从标题生成
	out, err := svc.Posts.CreatePost(ctx, model.Post{
		Slug: "p1", Title: "标题 Go", Category: "技术", PublishTime: 1750000000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["slug"] != "p1" {
		t.Errorf("create 返回 %v", out)
	}
	p, _ := st.Posts().BySlug(ctx, "p1")
	if p.PlainContent != "" || p.MdContent != "" {
		t.Errorf("新建文章不该有正文：plain=%q md=%q", p.PlainContent, p.MdContent)
	}
	if p.SearchText == "" || !strings.Contains(p.SearchText, "go") {
		t.Errorf("searchText 应包含标题（小写）：%q", p.SearchText)
	}

	// 重复 slug → post.slug_conflict
	_, err = svc.Posts.CreatePost(ctx, model.Post{Slug: "p1", Title: "x"})
	if err == nil || !strings.Contains(err.Error(), "post.slug_conflict") {
		t.Errorf("重复 slug 应冲突：%v", err)
	}
	// 缺 slug → validation.missing_field
	if _, err := svc.Posts.CreatePost(ctx, model.Post{Title: "x"}); err == nil ||
		!strings.Contains(err.Error(), "validation.missing_field") {
		t.Errorf("缺 slug 应校验失败：%v", err)
	}

	// updateMeta：局部更新（只改 title，其它不动）
	if err := svc.Posts.UpdatePostMeta(ctx, "p1", model.PostMeta{Title: strPtrT("新标题")}); err != nil {
		t.Fatal(err)
	}
	p, _ = st.Posts().BySlug(ctx, "p1")
	if p.Title != "新标题" || p.Category != "技术" {
		t.Errorf("局部更新异常：%+v", p)
	}
	// 不存在 → post.not_found
	if err := svc.Posts.UpdatePostMeta(ctx, "nope", model.PostMeta{}); err == nil ||
		!strings.Contains(err.Error(), "post.not_found") {
		t.Errorf("不存在应 404：%v", err)
	}

	// updateContent：重算派生字段
	out, err = svc.Posts.UpdatePostContent(ctx, "p1", "# 新标题\n\n**正文** Go 内容。")
	if err != nil {
		t.Fatal(err)
	}
	wc := out["wordCount"].(int)
	if wc <= 0 {
		t.Errorf("wordCount = %d", wc)
	}
	p, _ = st.Posts().BySlug(ctx, "p1")
	if p.MdContent == "" || strings.Contains(p.PlainContent, "**") {
		t.Errorf("正文/纯文本异常：md=%q plain=%q", p.MdContent, p.PlainContent)
	}
	if !strings.Contains(p.SearchText, "新标题") {
		t.Errorf("searchText 应含标题：%q", p.SearchText)
	}
	// 不存在的文章写正文 → 404
	if _, err := svc.Posts.UpdatePostContent(ctx, "nope", "x"); err == nil ||
		!strings.Contains(err.Error(), "post.not_found") {
		t.Errorf("不存在应 404：%v", err)
	}

	// delete：幂等
	if _, err := svc.Posts.DeletePost(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	out, err = svc.Posts.DeletePost(ctx, "p1")
	if err != nil || out["deleted"] != true {
		t.Errorf("幂等删除异常：%v %v", out, err)
	}
}

func TestDraftServiceFlow(t *testing.T) {
	svc, st := newService(t)
	ctx := context.Background()

	out, err := svc.Drafts.CreateDraft(ctx, model.Draft{Slug: "d1", Title: "草稿", PublishTime: 1750000000})
	if err != nil || out["slug"] != "d1" {
		t.Fatalf("建草稿失败：%v %v", out, err)
	}
	// 重复 → draft.slug_conflict
	if _, err := svc.Drafts.CreateDraft(ctx, model.Draft{Slug: "d1"}); err == nil ||
		!strings.Contains(err.Error(), "draft.slug_conflict") {
		t.Errorf("重复草稿应冲突：%v", err)
	}
	// 缺 slug
	if _, err := svc.Drafts.CreateDraft(ctx, model.Draft{}); err == nil ||
		!strings.Contains(err.Error(), "validation.missing_field") {
		t.Errorf("缺 slug 应校验：%v", err)
	}
	// 详情 / 列表 / slugs
	if d, err := svc.Drafts.Detail(ctx, "d1"); err != nil || d.Slug != "d1" {
		t.Errorf("详情异常：%v %v", d, err)
	}
	if _, err := svc.Drafts.Detail(ctx, "nope"); err == nil ||
		!strings.Contains(err.Error(), "draft.not_found") {
		t.Errorf("不存在应 404：%v", err)
	}
	page, err := svc.Drafts.List(ctx, store.DraftFilter{Page: 1, PageSize: 10, CountOnly: true})
	if err != nil || page.Count != 1 {
		t.Errorf("countOnly 列表异常：%+v %v", page, err)
	}
	if slugs, err := svc.Drafts.Slugs(ctx); err != nil || len(slugs) != 1 {
		t.Errorf("slugs 异常：%v %v", slugs, err)
	}
	// 更新元信息 / 正文 / 删除
	if err := svc.Drafts.UpdateDraftMeta(ctx, "d1", model.PostMeta{Title: strPtrT("改")}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Drafts.UpdateDraftContent(ctx, "d1", "草稿正文"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Drafts.UpdateDraftContent(ctx, "nope", "x"); err == nil ||
		!strings.Contains(err.Error(), "draft.not_found") {
		t.Errorf("不存在写正文应 404：%v", err)
	}
	if _, err := svc.Drafts.DeleteDraft(ctx, "d1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Drafts.DeleteDraft(ctx, "d1"); err != nil {
		t.Errorf("幂等删除异常：%v", err)
	}
	_ = st
}

func TestSpeakServiceFlow(t *testing.T) {
	svc, st := newService(t)
	ctx := context.Background()

	// 不传 time → 当前时间
	if err := svc.Speaks.CreateSpeak(ctx, "**新**说说", nil); err != nil {
		t.Fatal(err)
	}
	page, _ := svc.Speaks.List(ctx, store.ListFilter{Page: 1, PageSize: 10})
	if len(page.Items) != 1 {
		t.Fatalf("说说条数 = %d", len(page.Items))
	}
	when := page.Items[0].Time
	// 说说的 plainContent 用旧 replace_and_remove_tags 口径：只剥 HTML 标签，
	// Markdown 标记（**）原样保留——与文章的 CleanMarkdown 是两套口径
	if page.Items[0].PlainContent != "**新**说说" {
		t.Errorf("plainContent 应保留 Markdown 标记：%q", page.Items[0].PlainContent)
	}
	// 显式 time + 重复 time → validation.invalid_field
	ts := int64(1750000000)
	if err := svc.Speaks.CreateSpeak(ctx, "指定时间", &ts); err != nil {
		t.Fatal(err)
	}
	if err := svc.Speaks.CreateSpeak(ctx, "重复", &ts); err == nil ||
		!strings.Contains(err.Error(), "validation.invalid_field") {
		t.Errorf("重复时间应拒绝：%v", err)
	}
	// 空内容
	if err := svc.Speaks.CreateSpeak(ctx, "", nil); err == nil ||
		!strings.Contains(err.Error(), "validation.missing_field") {
		t.Errorf("空内容应拒绝：%v", err)
	}
	// 更新 / 不存在 / 删除
	if err := svc.Speaks.UpdateSpeak(ctx, when, "改过"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Speaks.UpdateSpeak(ctx, 12345, "x"); err == nil ||
		!strings.Contains(err.Error(), "validation.invalid_field") {
		t.Errorf("不存在应报错：%v", err)
	}
	if err := svc.Speaks.DeleteSpeak(ctx, when); err != nil {
		t.Fatal(err)
	}
	_ = st
}

func TestLinkWriteAndDecideFlow(t *testing.T) {
	svc, st := newService(t)
	ctx := context.Background()
	seed := func() {
		_ = st.Links().AddGroup(ctx, model.LinkGroup{Name: "友达", Order: 0})
		_ = st.Links().AddLink(ctx, "友达", model.Link{ID: "l1", Name: "站", URL: "https://l1.example", Color: "#f08"})
	}
	seed()

	// 加友链：分组不存在 → link.not_found
	if err := svc.Links.AddLink(ctx, "无此组", model.Link{ID: "x", Name: "n", URL: "https://x.example"}); err == nil ||
		!strings.Contains(err.Error(), "link.not_found") {
		t.Errorf("分组不存在应报错：%v", err)
	}
	// 缺字段
	if err := svc.Links.AddLink(ctx, "友达", model.Link{ID: "y"}); err == nil ||
		!strings.Contains(err.Error(), "validation.missing_field") {
		t.Errorf("缺字段应校验：%v", err)
	}
	// 正常 + 重复 URL
	if err := svc.Links.AddLink(ctx, "友达", model.Link{ID: "l2", Name: "站2", URL: "https://l2.example"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Links.AddLink(ctx, "友达", model.Link{ID: "l3", Name: "站3", URL: "https://l2.example"}); err == nil {
		t.Error("重复 URL 应拒绝")
	}
	// 更新：存在 / 不存在
	if err := svc.Links.UpdateLink(ctx, "友达", "l2", store.LinkPatch{Name: "改名"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Links.UpdateLink(ctx, "友达", "nope", store.LinkPatch{Name: "x"}); err == nil ||
		!strings.Contains(err.Error(), "link.not_found") {
		t.Errorf("不存在应报错：%v", err)
	}
	// 分组：建/重名/更新/改名冲突/删除
	if err := st.Links().AddGroup(ctx, model.LinkGroup{Name: "空组", Order: 1}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Links.AddGroup(ctx, model.LinkGroup{Name: "空组"}); err == nil ||
		!strings.Contains(err.Error(), "link.group_conflict") {
		t.Errorf("重名建组应冲突：%v", err)
	}
	if err := svc.Links.AddGroup(ctx, model.LinkGroup{}); err == nil ||
		!strings.Contains(err.Error(), "validation.missing_field") {
		t.Errorf("缺组名应校验：%v", err)
	}
	if err := svc.Links.UpdateGroup(ctx, "空组", model.LinkGroup{Name: "空组二", Order: 2}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Links.UpdateGroup(ctx, "友达", model.LinkGroup{Name: "空组二"}); err == nil ||
		!strings.Contains(err.Error(), "link.group_conflict") {
		t.Errorf("改名为已存在组应冲突：%v", err)
	}
	if err := svc.Links.UpdateGroup(ctx, "无此组", model.LinkGroup{Name: "x"}); err == nil ||
		!strings.Contains(err.Error(), "link.not_found") {
		t.Errorf("更新不存在组应报错：%v", err)
	}
	// 移动：正常 / 源不存在
	if err := svc.Links.AddGroup(ctx, model.LinkGroup{Name: "目标组"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Links.MoveLink(ctx, "友达", "目标组", "l1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Links.MoveLink(ctx, "友达", "目标组", "nope"); err == nil ||
		!strings.Contains(err.Error(), "link.not_found") {
		t.Errorf("移动不存在应报错：%v", err)
	}
	if err := svc.Links.MoveLink(ctx, "", "目标组", "l1"); err == nil ||
		!strings.Contains(err.Error(), "validation.missing_field") {
		t.Errorf("缺参数应校验：%v", err)
	}
	// 延迟上报
	if n, err := svc.Links.SetLatency(ctx, []store.LatencyUpdate{{ID: "l1", Latency: 12.5}}); err != nil || n != 1 {
		t.Errorf("延迟更新异常：%d %v", n, err)
	}
	if n, err := svc.Links.SetLatency(ctx, nil); err != nil || n != 0 {
		t.Errorf("空上报应 0：%d %v", n, err)
	}
	// 删除（幂等）
	if out, err := svc.Links.DeleteLink(ctx, "目标组", "l1"); err != nil || out["deleted"] != true {
		t.Errorf("删除异常：%v %v", out, err)
	}
	if out, err := svc.Links.DeleteGroup(ctx, "目标组"); err != nil || out["deleted"] != true {
		t.Errorf("删组异常：%v %v", out, err)
	}
}

func TestApplicationFlow(t *testing.T) {
	svc, st := newService(t)
	ctx := context.Background()
	// 已收录一条友链，用于「已收录」与「修改申请」用例
	_ = st.Links().AddGroup(ctx, model.LinkGroup{Name: "友达"})
	_ = st.Links().AddLink(ctx, "友达", model.Link{
		ID: "existing", Name: "老站", URL: "https://old.example",
		Avatar: "https://old.example/a.png", Description: "旧描述", Color: "#888888",
	})

	apply := func(in ApplyInput) (map[string]any, error) {
		return svc.Applications.Apply(ctx, in)
	}
	base := ApplyInput{
		Name: "新站", Link: "https://new.example", Avatar: "https://new.example/a.png",
		Descr: "介绍", Email: "a@b.cc", Color: "#f08", Type: 0, IP: "1.2.3.4",
	}

	// 正常（外部依赖全关：两个 false）
	out, err := apply(base)
	if err != nil {
		t.Fatal(err)
	}
	if out["mailSent"] != false || out["screeningTriggered"] != false {
		t.Errorf("关闭外部依赖应为 false：%v", out)
	}
	// 完全相同的重复 → duplicate
	if _, err := apply(base); err == nil || !strings.Contains(err.Error(), "application.duplicate") {
		t.Errorf("重复应冲突：%v", err)
	}
	// 已收录网址 → duplicate
	dup := base
	dup.Name, dup.Link = "另一个", "https://old.example/"
	if _, err := apply(dup); err == nil || !strings.Contains(err.Error(), "application.duplicate") {
		t.Errorf("已收录应冲突：%v", err)
	}
	// 校验失败（缺必填）
	bad := ApplyInput{}
	if _, err := apply(bad); err == nil || !strings.Contains(err.Error(), "validation.invalid_field") {
		t.Errorf("缺字段应校验：%v", err)
	}
	// XSS
	xss := base
	xss.Name = `<script>alert(1)</script>`
	if _, err := apply(xss); err == nil || !strings.Contains(err.Error(), "validation.invalid_field") {
		t.Errorf("XSS 应拒绝：%v", err)
	}

	// 修改申请：目标不存在 → link.not_found
	mod := base
	mod.Type, mod.TargetURL = 1, "https://nope.example"
	if _, err := apply(mod); err == nil || !strings.Contains(err.Error(), "link.not_found") {
		t.Errorf("目标不存在应 404：%v", err)
	}
	// 修改申请：内容与现状完全一致 → no_change
	same := ApplyInput{
		Type: 1, TargetURL: "https://old.example", Name: "老站",
		Link: "https://old.example", Avatar: "https://old.example/a.png",
		Descr: "旧描述", Color: "#888888", Email: "a@b.cc", IP: "1.2.3.4",
	}
	if _, err := apply(same); err == nil || !strings.Contains(err.Error(), "application.no_change") {
		t.Errorf("无变化应拒绝：%v", err)
	}
	// 修改申请：确有变化 → 成功
	same.Descr = "新描述"
	if _, err := apply(same); err != nil {
		t.Fatalf("有变化的修改申请应成功：%v", err)
	}
	// 队列满 → application.full
	for i := 0; i < 25; i++ {
		_, _ = apply(ApplyInput{
			Name: "灌水站" + itoaT(i), Link: "https://q" + itoaT(i) + ".example",
			Avatar: "https://q.example/a.png", Email: "q@b.cc", Type: 0, IP: "9.9.9.9",
		})
	}
	if _, err := apply(ApplyInput{
		Name: "溢出站", Link: "https://overflow.example", Avatar: "https://o.example/a.png",
		Email: "o@b.cc", Type: 0, IP: "9.9.9.9",
	}); err == nil || !strings.Contains(err.Error(), "application.full") {
		t.Errorf("队列满应 429：%v", err)
	}
	// 自助查询
	list, err := svc.Links.ApplicationsByContact(ctx, "q@b.cc", "", 5)
	if err != nil || len(list) == 0 {
		t.Errorf("自助查询异常：%d %v", len(list), err)
	} else if list[0].CreatedAt.Location() == time.UTC {
		t.Errorf("时间应转成配置时区：%v", list[0].CreatedAt)
	}
}

func TestDecisionFlow(t *testing.T) {
	svc, st := newService(t)
	ctx := context.Background()
	_ = st.Links().AddGroup(ctx, model.LinkGroup{Name: "友达"})
	_ = st.Links().AddGroup(ctx, model.LinkGroup{Name: "收藏"})

	// 造一条新增申请
	id, err := st.Applications().Create(ctx, model.LinkApplication{
		Type: 0, Name: "待审站", Link: "https://pending.example",
		Avatar: "https://p.example/a.png", Descr: "d", Color: "#f08",
		Email: "p@b.cc", Status: model.ApplicationStatusPending, CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// 非法 decision
	if _, err := svc.Links.DecideApplication(ctx, id, "maybe", "友达", "", nil); err == nil ||
		!strings.Contains(err.Error(), "validation.invalid_field") {
		t.Errorf("非法 decision 应校验：%v", err)
	}
	// 不存在
	if _, err := svc.Links.DecideApplication(ctx, "000000000000000000000000", "approved", "友达", "", nil); err == nil ||
		!strings.Contains(err.Error(), "application.not_found") {
		t.Errorf("不存在应 404：%v", err)
	}
	// approved 但没给分组
	if _, err := svc.Links.DecideApplication(ctx, id, "approved", "", "", nil); err == nil ||
		!strings.Contains(err.Error(), "validation.missing_field") {
		t.Errorf("缺分组应校验：%v", err)
	}
	// approved 目标分组不存在
	if _, err := svc.Links.DecideApplication(ctx, id, "approved", "无此组", "", nil); err == nil ||
		!strings.Contains(err.Error(), "link.not_found") {
		t.Errorf("分组不存在应报错：%v", err)
	}
	// approved 正常 + overrides
	out, err := svc.Links.DecideApplication(ctx, id, "approved", "友达", "", &store.LinkPatch{Name: "覆盖名", Color: "#abc"})
	if err != nil {
		t.Fatal(err)
	}
	if out["mailSent"] != false {
		t.Errorf("未配置邮件应 false：%v", out)
	}
	ref, err := st.Links().FindByURL(ctx, "https://pending.example")
	if err != nil || ref.Link.Name != "覆盖名" || ref.Group != "友达" {
		t.Errorf("审批后友链异常：%+v %v", ref, err)
	}
	// 已处理过的申请再决定 → not_found
	if _, err := svc.Links.DecideApplication(ctx, id, "approved", "友达", "", nil); err == nil ||
		!strings.Contains(err.Error(), "application.not_found") {
		t.Errorf("已处理应 404：%v", err)
	}

	// rejected 带理由 / 不带理由（都不发邮件，但状态要写）
	id2, _ := st.Applications().Create(ctx, model.LinkApplication{
		Type: 0, Name: "拒绝站", Link: "https://reject.example",
		Email: "r@b.cc", Status: model.ApplicationStatusPending, CreatedAt: time.Now(),
	})
	if _, err := svc.Links.DecideApplication(ctx, id2, "rejected", "", "内容不够", nil); err != nil {
		t.Fatal(err)
	}
	app, _ := st.Applications().ByID(ctx, id2)
	if app.Status != model.ApplicationStatusRejected || app.Reason != "内容不够" {
		t.Errorf("拒绝结果异常：%+v", app)
	}
	// 待审列表
	if list, n, err := svc.Links.PendingApplications(ctx); err != nil || n != 0 || len(list) != 0 {
		t.Errorf("待审列表异常：%d %d %v", len(list), n, err)
	}
	// screening 回写：非法 verdict / 不存在 / 正常
	id3, _ := st.Applications().Create(ctx, model.LinkApplication{
		Type: 0, Name: "初筛", Link: "https://screen.example",
		Status: model.ApplicationStatusPending, CreatedAt: time.Now(),
	})
	if err := svc.Applications.Screening(ctx, id3, "meh", "", "", "", ""); err == nil ||
		!strings.Contains(err.Error(), "validation.invalid_field") {
		t.Errorf("非法 verdict 应校验：%v", err)
	}
	if err := svc.Applications.Screening(ctx, "000000000000000000000000", "pass", "", "", "", ""); err == nil ||
		!strings.Contains(err.Error(), "application.not_found") {
		t.Errorf("不存在应 404：%v", err)
	}
	if err := svc.Applications.Screening(ctx, id3, "pass", "https://x", "正常", "", "m"); err != nil {
		t.Fatal(err)
	}
}

func TestSummaryAndTaxonomyForwarders(t *testing.T) {
	svc, st := newService(t)
	ctx := context.Background()
	st.SetPosts([]model.Post{{Slug: "a", Title: "A", Category: "技术", Tags: []string{"go"}, PublishTime: 1750000000}})

	// 分类/标签/归档转发
	if cats, err := svc.Posts.Categories(ctx); err != nil || len(cats) != 1 {
		t.Errorf("分类异常：%v %v", cats, err)
	}
	if tags, err := svc.Posts.Tags(ctx); err != nil || len(tags) != 1 {
		t.Errorf("标签异常：%v %v", tags, err)
	}
	if arch, err := svc.Posts.Archives(ctx); err != nil || len(arch) == 0 {
		t.Errorf("归档异常：%v %v", arch, err)
	}
	// summary：未启用 LLM → upstream.failed；有缓存 → 命中
	if _, err := svc.Posts.SummaryGenerate(ctx, "a"); err == nil ||
		!strings.Contains(err.Error(), "upstream.failed") {
		t.Errorf("未启用应 upstream.failed：%v", err)
	}
	_ = st.Summaries().Upsert(ctx, "a", "缓存摘要", time.Unix(1750000000, 0))
	if out, err := svc.Posts.SummaryGet(ctx, "a"); err != nil || out["summary"] != "缓存摘要" {
		t.Errorf("缓存命中异常：%v %v", out, err)
	}
	// statistics / slugs
	if out, err := svc.Posts.Statistics(ctx); err != nil || out["totalWordCount"] == nil {
		t.Errorf("统计异常：%v %v", out, err)
	}
	if slugs, err := svc.Posts.Slugs(ctx); err != nil || len(slugs) != 1 {
		t.Errorf("slugs 异常：%v %v", slugs, err)
	}
	// site info 未写入 → ErrNotFound 透传
	if _, err := svc.Sites.Info(ctx); err == nil {
		t.Error("site info 未写入应报错")
	}
}

func TestApplicationsByContactLocalizesTime(t *testing.T) {
	svc, st := newService(t)
	ctx := context.Background()
	_, _ = st.Applications().Create(ctx, model.LinkApplication{
		Type: 0, Name: "时区站", Link: "https://tz.example", Email: "tz@b.cc",
		Status: model.ApplicationStatusPending, CreatedAt: time.Unix(1750000000, 0).UTC(),
	})
	list, err := svc.Links.ApplicationsByContact(ctx, "tz@b.cc", "", 5)
	if err != nil || len(list) != 1 {
		t.Fatalf("查询异常：%d %v", len(list), err)
	}
	// 上海时区偏移 +08:00
	_, offset := list[0].CreatedAt.Zone()
	if offset != 8*3600 {
		t.Errorf("时区偏移 = %d，应为 +08:00", offset)
	}
}

func strPtrT(s string) *string { return &s }

func itoaT(n int) string {
	if n == 0 {
		return "0"
	}
	buf := []byte{}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}

// 标题变更必须连带重算 searchText：否则关键词索引停在旧标题，
// 改完标题搜不到新标题（2026-10-28 修复的回归钉）。
func TestUpdatePostMetaTitleRefreshesSearchText(t *testing.T) {
	cfg := serviceConfig()
	st := fake.New()
	svc := NewSet(cfg, st)
	st.SetPosts([]model.Post{
		{Slug: "a", Title: "旧标题", Category: "随笔", MdContent: "正文内容", PublishTime: 100,
			SearchText: textutil.BuildSearchText("旧标题", "正文内容")},
	})
	if err := svc.Posts.UpdatePostMeta(context.Background(), "a",
		model.PostMeta{Title: ptrStr("新标题.txt")}); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	res, err := svc.Posts.List(context.Background(), store.PostFilter{Page: 1, Keyword: "新标题", PageSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Items[0].Slug != "a" {
		t.Errorf("改标题后应能搜到新标题：%+v", res.Items)
	}
	res, err = svc.Posts.List(context.Background(), store.PostFilter{Page: 1, Keyword: "旧标题", PageSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 0 {
		t.Errorf("旧标题索引应已更新：%+v", res.Items)
	}
}

func ptrStr(s string) *string { return &s }

// 站点更新时间联动（2026-10-28 用户定论）：
// 文章/说说/友链的写入要 touch；友链申请「拒绝」不算内容更新，不能 touch。
func TestContentWritesTouchSiteTime(t *testing.T) {
	cfg := serviceConfig()
	st := fake.New()
	svc := NewSet(cfg, st)
	ctx := context.Background()

	siteTime := func() int64 {
		t.Helper()
		info, err := svc.Sites.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return info["latestUpdateTime"].(int64)
	}
	resetTo := func(v int64) { st.SetSiteInfo(map[string]int64{"latestUpdateTime": v}) }

	// 文章：create → content → delete 各刷一次
	if _, err := svc.Posts.CreatePost(ctx, model.Post{Slug: "a", Title: "标题", PublishTime: 1}); err != nil {
		t.Fatal(err)
	}
	if siteTime() <= 100 {
		t.Fatalf("create 后站点时间应更新，得到 %d", siteTime())
	}
	resetTo(100)
	if _, err := svc.Posts.UpdatePostContent(ctx, "a", "正文"); err != nil {
		t.Fatal(err)
	}
	if siteTime() <= 100 {
		t.Fatal("改正文后站点时间应更新")
	}
	resetTo(100)
	if _, err := svc.Posts.DeletePost(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if siteTime() <= 100 {
		t.Fatal("删文章后站点时间应更新")
	}

	// 说说：create 也要刷
	resetTo(100)
	if err := svc.Speaks.CreateSpeak(ctx, "一条说说", nil); err != nil {
		t.Fatal(err)
	}
	if siteTime() <= 100 {
		t.Fatal("发说说过后站点时间应更新")
	}
}

// 申请被拒绝 = 站点内容没变 → 不 touch。
func TestRejectedApplicationDoesNotTouchSite(t *testing.T) {
	cfg := serviceConfig()
	st := fake.New()
	svc := NewSet(cfg, st)
	ctx := context.Background()

	id, err := st.Applications().Create(ctx, model.LinkApplication{
		Name: "某站", Link: "https://x.example", Email: "a@b.c",
		Status: model.ApplicationStatusPending, Type: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	st.SetSiteInfo(map[string]int64{"latestUpdateTime": 100})
	if _, err := svc.Links.DecideApplication(ctx, id, model.ApplicationStatusRejected, "", "理由", nil); err != nil {
		t.Fatal(err)
	}
	info, _ := svc.Sites.Info(ctx)
	if got := info["latestUpdateTime"].(int64); got != 100 {
		t.Errorf("拒绝申请不应 touch 站点时间，期望 100 得到 %d", got)
	}
}
