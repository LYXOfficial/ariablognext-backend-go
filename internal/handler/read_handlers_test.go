package handler

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

// 只读域 handler 的补测：posts 其余、tags/categories/archives、links、speaks、site。

func seedReadData(st interface {
	SetPosts([]model.Post)
	SetSpeaks([]model.Speak)
	SetGroups([]model.LinkGroup)
	SetSiteInfo(map[string]int64)
}) {
}

func TestPostsReadHandlers(t *testing.T) {
	a, st := testHandlerApp(t)
	seedPosts(st)
	st.SetSiteInfo(map[string]int64{"latestUpdateTime": 1750000200})

	// statistics
	status, obj := do(t, a, "GET", "/posts/statistics")
	if status != 200 || obj["data"].(map[string]any)["totalWordCount"] == nil {
		t.Errorf("statistics 异常：%d %v", status, obj)
	}
	// slugs
	status, obj = do(t, a, "GET", "/posts/slugs")
	if status != 200 || len(obj["data"].([]any)) != 2 {
		t.Errorf("slugs 异常：%d %v", status, obj)
	}
	// navigation：newer/older 方向
	status, obj = do(t, a, "GET", "/posts/navigation?slug=newer")
	if status != 200 {
		t.Fatalf("navigation 异常：%d %v", status, obj)
	}
	data := obj["data"].(map[string]any)
	if data["newer"] != nil {
		t.Errorf("最新文章不该有 newer：%v", data["newer"])
	}
	if older := data["older"].(map[string]any); older["slug"] != "older" {
		t.Errorf("older 指向错误：%v", older)
	}
	// navigation 不存在 → 404
	status, obj = do(t, a, "GET", "/posts/navigation?slug=nope")
	if status != 404 || obj["error"].(map[string]any)["code"] != "post.not_found" {
		t.Errorf("不存在应 404：%d %v", status, obj)
	}
	// related：无标签返回 []
	status, obj = do(t, a, "GET", "/posts/related?slug=older")
	if status != 200 {
		t.Errorf("related 异常：%d %v", status, obj)
	}
	if _, ok := obj["data"].([]any); !ok {
		t.Errorf("related data 应是数组：%v", obj["data"])
	}
	// related limit 越界
	status, _ = do(t, a, "GET", "/posts/related?slug=older&limit=99")
	if status != 400 {
		t.Errorf("limit 越界应 400：%d", status)
	}
	// summary：缓存未命中且未启用 → upstream.failed
	status, obj = do(t, a, "GET", "/posts/summary?slug=newer")
	if status != 502 || obj["error"].(map[string]any)["code"] != "upstream.failed" {
		t.Errorf("未启用摘要应 502：%d %v", status, obj)
	}
	// semantic：开关关闭 = 关键词回退（不再是 501），空库 → data 空数组
	status, obj = do(t, a, "GET", "/posts/semantic?query=x")
	if status != 200 || obj["ok"] != true {
		t.Errorf("semantic 关闭时应回退关键词搜索 200：%d %v", status, obj)
	}
	// withContent=true 时列表带 mdContent
	status, obj = do(t, a, "GET", "/posts/list?withContent=true")
	if status != 200 {
		t.Fatal(status)
	}
	first := obj["data"].([]any)[0].(map[string]any)
	if _, has := first["mdContent"]; !has {
		t.Error("withContent=true 应返回 mdContent")
	}
}

func TestTaxonomyAndLinksReadHandlers(t *testing.T) {
	a, st := testHandlerApp(t)
	seedPosts(st)
	st.SetGroups([]model.LinkGroup{
		{Name: "友达", Order: 0, Links: []model.Link{{ID: "l1", Name: "站", URL: "https://l.example"}}},
	})

	// tags：列表 + countOnly
	status, obj := do(t, a, "GET", "/tags/list")
	if status != 200 || len(obj["data"].([]any)) == 0 {
		t.Errorf("tags 异常：%d %v", status, obj)
	}
	status, obj = do(t, a, "GET", "/tags/list?countOnly=true")
	if status != 200 || obj["data"] != nil || obj["meta"].(map[string]any)["count"] == nil {
		t.Errorf("tags countOnly 异常：%d %v", status, obj)
	}
	// categories
	status, obj = do(t, a, "GET", "/categories/list")
	if status != 200 || len(obj["data"].([]any)) == 0 {
		t.Errorf("categories 异常：%d %v", status, obj)
	}
	// archives：倒序
	status, obj = do(t, a, "GET", "/archives/list")
	if status != 200 || len(obj["data"].([]any)) == 0 {
		t.Errorf("archives 异常：%d %v", status, obj)
	}
	// links：data + meta.count（条目数不是分组数）
	status, obj = do(t, a, "GET", "/links/list")
	if status != 200 {
		t.Fatal(status)
	}
	if obj["meta"].(map[string]any)["count"] != float64(1) {
		t.Errorf("links meta.count 应为条目数 1：%v", obj["meta"])
	}
	status, obj = do(t, a, "GET", "/links/list?countOnly=true")
	if status != 200 || obj["data"] != nil {
		t.Errorf("links countOnly 异常：%d %v", status, obj)
	}
	// theme-color：非法 URL 400
	status, obj = do(t, a, "GET", "/links/theme-color?url=ftp://x/y.png")
	if status != 400 {
		t.Errorf("非法协议应 400：%d %v", status, obj)
	}
	// theme-color 缺 url
	status, _ = do(t, a, "GET", "/links/theme-color")
	if status != 400 {
		t.Errorf("缺 url 应 400：%d", status)
	}
	// applications/status：email 命中 / 只给 name / 非法邮箱
	status, obj = do(t, a, "GET", "/links/applications/status?email=a@b.cc")
	if status != 200 {
		t.Errorf("status 查询应 200：%d %v", status, obj)
	}
	status, _ = do(t, a, "GET", "/links/applications/status?name=%E7%AB%99")
	if status != 200 {
		t.Errorf("按名查询应 200：%d", status)
	}
}

func TestSpeaksAndSiteHandlers(t *testing.T) {
	a, st := testHandlerApp(t)
	st.SetSpeaks([]model.Speak{{Time: 1750000000, Content: "说说", PlainContent: "说说"}})
	st.SetSiteInfo(map[string]int64{"latestUpdateTime": 1750000200})

	status, obj := do(t, a, "GET", "/speaks/list?pageSize=1")
	if status != 200 || len(obj["data"].([]any)) != 1 {
		t.Errorf("speaks 列表异常：%d %v", status, obj)
	}
	status, obj = do(t, a, "GET", "/speaks/list?countOnly=true")
	if status != 200 || obj["data"] != nil {
		t.Errorf("speaks countOnly 异常：%d %v", status, obj)
	}
	// site/info
	status, obj = do(t, a, "GET", "/site/info")
	if status != 200 || obj["data"].(map[string]any)["latestUpdateTime"] != float64(1750000200) {
		t.Errorf("site info 异常：%d %v", status, obj)
	}
}

func TestMaintenanceAndSitemap(t *testing.T) {
	a, st := testHandlerApp(t)
	seedPosts(st)

	// export：无内部令牌 401，有令牌 200 且 collections 是对象
	status, obj := do(t, a, "POST", "/maintenance/export")
	if status != 401 {
		t.Errorf("export 无令牌应 401：%d %v", status, obj)
	}
	status, raw, err := a.TestReq("POST", "/maintenance/export", nil,
		map[string]string{"X-Internal-Token": "test-secret"})
	if err != nil || status != 200 {
		t.Fatalf("export 应 200：%d %v", status, err)
	}
	var resp map[string]any
	_ = json.Unmarshal(raw, &resp)
	collections, ok := resp["data"].(map[string]any)["collections"].(map[string]any)
	if !ok {
		t.Fatalf("collections 应是对象：%v", resp["data"])
	}
	if _, has := collections["Posts"]; !has {
		t.Errorf("导出应含 Posts：%v", collections)
	}

	// latency：内部令牌 + items
	payload := `{"items":[{"id":"nope","latency":12.5}]}`
	status, raw, err = a.TestReq("POST", "/links/latency", bytes.NewBufferString(payload),
		map[string]string{"X-Internal-Token": "test-secret", "Content-Type": "application/json"})
	if err != nil || status != 200 {
		t.Fatalf("latency 应 200：%d %v %s", status, err, raw)
	}
	// 空 items → 400
	status, _ = doAuth(t, a, "POST", "/links/latency", bytes.NewBufferString(`{"items":[]}`),
		map[string]string{"X-Internal-Token": "test-secret", "Content-Type": "application/json"})
	if status != 400 {
		t.Errorf("空 items 应 400：%d", status)
	}

	// sitemap：XML 且含 urlset
	reqStatus, raw, err := a.TestReq("GET", "/sitemap", nil, nil)
	if err != nil || reqStatus != 200 {
		t.Fatalf("sitemap 应 200：%d %v", reqStatus, err)
	}
	body := string(raw)
	if !strings.Contains(body, "<urlset") || !strings.Contains(body, "/posts/older") {
		t.Errorf("sitemap 内容异常：%s", body[:min(200, len(body))])
	}

	// links/check：未启用链路检查 → upstream.failed
	seedUsers(st)
	_, loginResp := login(t, a, testUser, testPassword)
	token := loginResp["data"].(map[string]any)["token"].(string)
	status, obj = doAuth(t, a, "POST", "/links/check", bytes.NewBufferString("{}"),
		map[string]string{"Authorization": token, "Content-Type": "application/json"})
	if status != 502 || obj["error"].(map[string]any)["code"] != "upstream.failed" {
		t.Errorf("未启用应 502：%d %v", status, obj)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// 哨兵构造函数（避免在测试里直接依赖 store 包名太长）
func storeErrNotFound() error  { return store.ErrNotFound }
func storeErrDuplicate() error { return store.ErrDuplicate }
func storeErrReadOnly() error  { return store.ErrReadOnly }

// sitemap 的 lastmod 必须取每篇的 lastUpdatedTime（与旧后端一致），
// 并按时区格式化；缺 lastUpdatedTime 时退回 publishTime。
func TestSitemapPerPostLastmod(t *testing.T) {
	a, st := testHandlerApp(t)
	// 2026-01-02 08:00 +08:00 = 1767312000；2025-12-31 23:30 +08:00 = 1767195000（UTC 仍是 12-31）
	st.SetPosts([]model.Post{
		{Slug: "new", Title: "新", PublishTime: 1767312000, LastUpdatedTime: 1767312000},
		{Slug: "old-r", Title: "旧", PublishTime: 1767195000, LastUpdatedTime: 1767195000},
		{Slug: "no-lastmod", Title: "只有发布时间", PublishTime: 1767312000},
	})
	status, raw, err := a.TestReq("GET", "/sitemap", nil, nil)
	if err != nil || status != 200 {
		t.Fatalf("sitemap 应 200：%d %v", status, err)
	}
	body := string(raw)
	// 每篇用各自日期，不是统一的「今天」
	if !strings.Contains(body, "<loc>https://blog.yaria.top/posts/new</loc><lastmod>2026-01-02</lastmod>") {
		t.Errorf("new 的 lastmod 应为 2026-01-02：\n%s", body[:min(400, len(body))])
	}
	if !strings.Contains(body, "<loc>https://blog.yaria.top/posts/old-r</loc><lastmod>2025-12-31</lastmod>") {
		t.Errorf("old-r 的 lastmod 应为上海时区的 2025-12-31（UTC 也是 12-31，这里验证不漂移）")
	}
	// 缺 lastUpdatedTime → 退回 publishTime
	if !strings.Contains(body, "<loc>https://blog.yaria.top/posts/no-lastmod</loc><lastmod>2026-01-02</lastmod>") {
		t.Errorf("缺 lastUpdatedTime 应退回 publishTime")
	}
	// 固定页面仍在
	for _, want := range []string{"<loc>https://blog.yaria.top</loc>", "/speaks</loc>", "/links</loc>", "/about</loc>"} {
		if !strings.Contains(body, want) {
			t.Errorf("固定页面缺失：%s", want)
		}
	}
}

// 契约 §3.3：tags/categories/archives 的列表形态也要带 meta.count（去重后的种类数）。
// 之前漏了 meta，靠 golden 与这条测试钉住。
func TestTaxonomyListsCarryCountMeta(t *testing.T) {
	a, st := testHandlerApp(t)
	st.SetPosts([]model.Post{
		{Slug: "a", Category: "技术", Tags: []string{"go", "测试"}, PublishTime: 1767312000},
		{Slug: "b", Category: "技术", Tags: []string{"go"}, PublishTime: 1750000000},
		{Slug: "c", Category: "随笔", PublishTime: 1767312000},
	})
	cases := []struct {
		path      string
		wantCount float64
	}{
		{"/tags/list", 2},       // go / 测试
		{"/categories/list", 2}, // 技术 / 随笔
		{"/archives/list", 2},   // 2026-01 / 2025-06 两个月桶
	}
	for _, c := range cases {
		status, obj := do(t, a, "GET", c.path)
		if status != 200 {
			t.Fatalf("%s 应 200，得到 %d", c.path, status)
		}
		meta, ok := obj["meta"].(map[string]any)
		if !ok {
			t.Errorf("%s 缺 meta（契约 §3.3 要求 meta.count）：%v", c.path, obj)
			continue
		}
		if meta["count"] != c.wantCount {
			t.Errorf("%s meta.count = %v，应为 %v", c.path, meta["count"], c.wantCount)
		}
		if _, hasPage := meta["page"]; hasPage {
			t.Errorf("%s 的 meta 不该带分页字段：%v", c.path, meta)
		}
	}
}

// tags 缺失时不能输出 null（否则前端 .map 炸）：存储层 Normalize 兜底后应为 []。
func TestPostTagsNeverNull(t *testing.T) {
	a, st := testHandlerApp(t)
	st.SetPosts([]model.Post{{Slug: "no-tags", Title: "无标签", PublishTime: 1767312000}})
	status, raw, err := a.TestReq("GET", "/posts/list", nil, nil)
	if err != nil || status != 200 {
		t.Fatalf("列表应 200：%d %v", status, err)
	}
	if strings.Contains(string(raw), `"tags":null`) {
		t.Errorf("tags 不能是 null：%s", raw)
	}
	status, raw, _ = a.TestReq("GET", "/posts/detail?slug=no-tags", nil, nil)
	if status != 200 || strings.Contains(string(raw), `"tags":null`) {
		t.Errorf("detail 的 tags 也不能是 null：%d %s", status, raw)
	}
}
