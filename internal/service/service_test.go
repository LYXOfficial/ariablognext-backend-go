package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store/fake"
)

func serviceConfig() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{Timezone: "Asia/Shanghai"},
		List:   config.ListConfig{DefaultPageSize: 16, MaxPageSize: 100, MaxFullItems: 3},
		Search: config.SearchConfig{MaxKeywordLength: 64, ContextRadius: 50, TitleBoost: true},
		Flink: config.FlinkConfig{
			MaxName: 50, MaxDescription: 200, MaxURL: 300,
			PendingLimit: 20, RetentionDays: 14,
		},
		Site: config.SiteConfig{URL: "https://blog.example", Name: "测试站",
			AdminPanelURL: "https://blog.example/admin"},
	}
}

func newService(t *testing.T) (*Set, *fake.Store) {
	t.Helper()
	cfg := serviceConfig()
	st := fake.New()
	return NewSet(cfg, st), st
}

func TestListFullCap(t *testing.T) {
	svc, st := newService(t)
	st.SetPosts([]model.Post{
		{Slug: "a", PublishTime: 1}, {Slug: "b", PublishTime: 2},
		{Slug: "c", PublishTime: 3}, {Slug: "d", PublishTime: 4},
	})
	ctx := context.Background()
	// 4 条 > maxFullItems(3)：full 被拦，报 validation.invalid_field 且文案带条数
	_, err := svc.Posts.List(ctx, store.PostFilter{Full: true})
	if err == nil {
		t.Fatal("full 超上限必须报错")
	}
	if !strings.Contains(err.Error(), "4") || !strings.Contains(err.Error(), "3") {
		t.Errorf("文案应包含真实条数与上限：%v", err)
	}
	// countOnly 的 full 不受条数上限拦（纯计数很轻，契约没有禁止）
	res, err := svc.Posts.List(ctx, store.PostFilter{Full: true, CountOnly: true})
	if err != nil || res.Count != 4 {
		t.Errorf("countOnly full 应放行：count=%d err=%v", res.Count, err)
	}
}

func TestListTitleBoost(t *testing.T) {
	svc, st := newService(t)
	// "go" 在三条标题/正文里都有：标题命中的排最前（即使发布时间最早）。
	// 正文放 MdContent：搜索按「剥 Markdown 后的全文」匹配，plainContent 会截断。
	st.SetPosts([]model.Post{
		{Slug: "body-hit", Title: "其它", MdContent: "讲 go 的文章", PublishTime: 300},
		{Slug: "title-hit", Title: "go 指南", MdContent: "内容", PublishTime: 100},
		{Slug: "none", Title: "无关", MdContent: "没有关键词", PublishTime: 200},
	})
	res, err := svc.Posts.List(context.Background(), store.PostFilter{Page: 1, PageSize: 10, Keyword: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("命中 2 条：%d", len(res.Items))
	}
	if res.Items[0].Slug != "title-hit" {
		t.Errorf("标题命中应排最前：got %s", res.Items[0].Slug)
	}
	// count 是过滤后总数
	if res.Count != 2 {
		t.Errorf("count = %d", res.Count)
	}
}

func TestNavigationDirection(t *testing.T) {
	// 旧接口 previous/next 名实相反是老 bug 点：newer 必须是更新的那篇
	svc, st := newService(t)
	st.SetPosts([]model.Post{
		{Slug: "old", PublishTime: 100},
		{Slug: "mid", PublishTime: 200},
		{Slug: "new", PublishTime: 300},
	})
	ctx := context.Background()
	nav, err := svc.Posts.Navigation(ctx, "mid")
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := nav["newer"].(*model.PostBrief); !ok || b.Slug != "new" {
		t.Errorf("newer 应是更新的 new：%v", nav["newer"])
	}
	if b, ok := nav["older"].(*model.PostBrief); !ok || b.Slug != "old" {
		t.Errorf("older 应是更旧的 old：%v", nav["older"])
	}
	// 边界文章：一端为 null
	nav, _ = svc.Posts.Navigation(ctx, "new")
	// 注意：仓储返回的是「类型化 nil」（*PostBrief)(nil)，接口 != nil 但实际为空，
	// 断言要走类型转换（这个坑 JSON 序列化时没问题——nil 指针会序列化成 null）
	if b, _ := nav["newer"].(*model.PostBrief); b != nil {
		t.Errorf("最新的文章没有 newer：%v", nav["newer"])
	}
	if b, ok := nav["older"].(*model.PostBrief); !ok || b.Slug != "mid" {
		t.Errorf("older = %v", nav["older"])
	}
	// 不存在 → post.not_found
	if _, err := svc.Posts.Navigation(ctx, "nope"); err == nil ||
		!strings.Contains(err.Error(), "post.not_found") {
		t.Errorf("不存在的文章应报 post.not_found：%v", err)
	}
}

func TestSummaryCacheFirst(t *testing.T) {
	svc, st := newService(t)
	st.SetPosts([]model.Post{{Slug: "a"}})
	ctx := context.Background()
	// 无缓存且摘要服务未启用：upstream.failed（本机 config.local 关闭）
	if _, err := svc.Posts.SummaryGet(ctx, "a"); err == nil ||
		!strings.Contains(err.Error(), "upstream.failed") {
		t.Errorf("无缓存应报 upstream.failed：%v", err)
	}
	// 有缓存：直接返回
	if err := st.Summaries().Upsert(ctx, "a", "这是缓存摘要", fixedTime()); err != nil {
		t.Fatal(err)
	}
	out, err := svc.Posts.SummaryGet(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if out["summary"] != "这是缓存摘要" {
		t.Errorf("summary = %v", out["summary"])
	}
	// 文章不存在：post.not_found
	if _, err := svc.Posts.SummaryGet(ctx, "nope"); err == nil ||
		!strings.Contains(err.Error(), "post.not_found") {
		t.Errorf("应报 post.not_found：%v", err)
	}
}

func TestSiteUpdateTimeCustomTimestamp(t *testing.T) {
	// 语义修正：支持自定义时间戳；缺省取当前；不再耦合 refreshCache 通知
	svc, st := newService(t)
	ctx := context.Background()
	out, err := svc.Sites.UpdateTime(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	first := out["latestUpdateTime"].(int64)
	if first <= 1.7e9 || first >= 4.1e9 {
		t.Errorf("缺省应写当前 unix 秒：%v", first)
	}
	// 自定义时间戳原样落库
	custom := int64(1234567890)
	out, err = svc.Sites.UpdateTime(ctx, &custom)
	if err != nil {
		t.Fatal(err)
	}
	if out["latestUpdateTime"].(int64) != custom {
		t.Errorf("自定义时间戳未生效：%v", out["latestUpdateTime"])
	}
	// Info 读回来也是自定义值
	info, _ := svc.Sites.Info(ctx)
	if info["latestUpdateTime"].(int64) != custom {
		t.Errorf("Info 读回错误：%v", info["latestUpdateTime"])
	}
	_ = st
}

// fixedTime 固定时间戳（testing.md §7：不许用 time.Now 断言）。
func fixedTime() time.Time { return time.Unix(1750000000, 0) }

func TestSplitKeywordsServiceSide(t *testing.T) {
	if got := splitKeywords("go  云端"); len(got) != 2 || got[1] != "云端" {
		t.Errorf("splitKeywords = %#v", got)
	}
}

// 搜索保真度（2026-10-27 修）：关键词必须按「剥 Markdown 后的全文」匹配。
//
//	· 正文 201 字之后的词要能搜到（plainContent 被截断，靠它匹配会漏）；
//	· Markdown 结构（<Folding> 等外挂标签）里的词不能命中（旧后端正则的毛病）。
func TestSearchUsesFullTextNotTruncatedPlainContent(t *testing.T) {
	svc, st := newService(t)
	long := strings.Repeat("前导正文。", 60) // 远超 201 字
	st.SetPosts([]model.Post{
		{Slug: "deep", Title: "深水区", PublishTime: 300,
			PlainContent: "前导正文。", // 模拟库里被截断的 plainContent
			MdContent:    long + "这段英文的意思大"},
		{Slug: "markup-only", Title: "只有外挂结构", PublishTime: 200,
			MdContent: "<Folding summary=\"点开\">正文</Folding>"},
		{Slug: "plain-prose", Title: "正文里有 fold", PublishTime: 100,
			MdContent: "正文真的提到 fold 这个词"},
	})
	ctx := context.Background()

	// ① 正文中段的词能搜到
	res, err := svc.Posts.List(ctx, store.PostFilter{Page: 1, PageSize: 10, Keyword: "这段英文"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Items[0].Slug != "deep" {
		t.Errorf("正文中段关键词应命中 deep：%v", slugsOf(res.Items))
	}
	// ② 外挂标签结构里的 fold 不命中；正文里的 fold 命中
	res, err = svc.Posts.List(ctx, store.PostFilter{Page: 1, PageSize: 10, Keyword: "fold"})
	if err != nil {
		t.Fatal(err)
	}
	got := slugsOf(res.Items)
	if len(got) != 1 || got[0] != "plain-prose" {
		t.Errorf("fold 应只命中正文（不含 <Folding> 结构），得到 %v", got)
	}
	// ③ countOnly 与列表用同一套匹配
	cnt, err := svc.Posts.List(ctx, store.PostFilter{CountOnly: true, Keyword: "fold"})
	if err != nil {
		t.Fatal(err)
	}
	if cnt.Count != 1 {
		t.Errorf("countOnly 的 count 应为 1，得到 %d", cnt.Count)
	}
}

func slugsOf(items []model.Post) []string {
	out := make([]string, 0, len(items))
	for _, p := range items {
		out = append(out, p.Slug)
	}
	return out
}
