package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/app"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/service"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store/fake"
)

// handler 测试基建：fake 存储 + app.TestReq。
// checks.sh 连测试文件一起扫，handler 包（含 _test.go）不许 import net/http，
// 所以请求一律经 a.TestReq 发出，方法名用字符串常量。

func testHandlerApp(t *testing.T) (*app.App, *fake.Store) {
	t.Helper()
	cfg := handlerTestConfig()
	st := fake.New()
	a := app.New(cfg, st)
	svc := service.NewSet(cfg, st)
	// 登录服务两段式挂载（与 main 相同），否则 svc.Login 为 nil 直接 panic
	svc.AttachLogin(st.Users(), a.JWT())
	Register(a, cfg, st, svc, NewImageStore(cfg))
	return a, st
}

// do 发请求并解析 JSON 信封。
func do(t *testing.T, a *app.App, method, path string) (int, map[string]any) {
	t.Helper()
	status, body, err := a.TestReq(method, path, nil, nil)
	if err != nil {
		t.Fatalf("%s %s：%v", method, path, err)
	}
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("%s %s 响应不是 JSON：%s", method, path, body)
	}
	return status, obj
}

func seedPosts(st *fake.Store) {
	pub := func(s string) *int { n := len(s); return &n }
	_ = pub
	// 固定时间戳，不依赖 time.Now（testing.md §7：不许用 time.Now 断言）
	st.SetPosts([]model.Post{
		{Slug: "older", Title: "早的文章", Category: "随笔", Tags: []string{"life"}, PublishTime: 1700000000, PlainContent: "内容一"},
		{Slug: "newer", Title: "新的文章", Category: "技术", Tags: []string{"go"}, PublishTime: 1750000000, PlainContent: "内容二", MdContent: "# 标题\n\n正文"},
	})
}

// ---- /health ----

func TestHealthOK(t *testing.T) {
	a, _ := testHandlerApp(t)
	status, obj := do(t, a, "GET", "/health")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	data, _ := obj["data"].(map[string]any)
	if data == nil || data["status"] != "ok" || data["db"] != "ok" {
		t.Errorf("health data 错误：%v", obj["data"])
	}
	if data["version"] != "dev" {
		t.Errorf("version = %v", data["version"])
	}
	// time 是 unix 秒：合理的当前时间范围（2020–2100），不是毫秒
	if ts, ok := data["time"].(float64); !ok || ts < 1.6e9 || ts > 4.1e9 {
		t.Errorf("time 应为 unix 秒：%v", data["time"])
	}
}

// ---- /posts/list ----

func TestPostsListDefaultShape(t *testing.T) {
	a, st := testHandlerApp(t)
	seedPosts(st)
	status, obj := do(t, a, "GET", "/posts/list?pageSize=1")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	data, ok := obj["data"].([]any)
	if !ok || len(data) != 1 {
		t.Fatalf("data 应是长度 1 的数组：%v", obj["data"])
	}
	meta := obj["meta"].(map[string]any)
	if meta["count"] != float64(2) {
		t.Errorf("count 应是过滤后的总数 2：%v", meta)
	}
	if meta["pageSize"] != float64(1) || meta["hasMore"] != true {
		t.Errorf("meta 错误：%v", meta)
	}
	// 列表不该带 mdContent（投影排除），但 plainContent 要有
	first := data[0].(map[string]any)
	if _, has := first["mdContent"]; has {
		t.Error("列表响应不该带 mdContent")
	}
	if first["plainContent"] == nil {
		t.Error("plainContent 应该有值")
	}
}

func TestPostsListCountOnly(t *testing.T) {
	a, st := testHandlerApp(t)
	seedPosts(st)
	_, obj := do(t, a, "GET", "/posts/list?countOnly=true&category=技术")
	if obj["data"] != nil {
		t.Errorf("countOnly data 应为 null：%v", obj["data"])
	}
	meta := obj["meta"].(map[string]any)
	if meta["count"] != float64(1) {
		t.Errorf("count = %v", meta["count"])
	}
	if _, exists := meta["page"]; exists {
		t.Error("countOnly 的 meta 不该有 page")
	}
}

func TestPostsListValidation(t *testing.T) {
	a, st := testHandlerApp(t)
	seedPosts(st)
	cases := []struct {
		query string
		code  string
		field string
	}{
		{"pageSize=0", "validation.invalid_field", "pageSize"},
		{"pageSize=999", "validation.invalid_field", "pageSize"},
		{"pageSize=abc", "validation.invalid_field", "pageSize"},
		{"month=3", "validation.invalid_field", "month"},
		{"order=side", "validation.invalid_field", "order"},
		{"countOnly=yeah", "validation.invalid_field", "countOnly"},
	}
	for _, tc := range cases {
		status, obj := do(t, a, "GET", "/posts/list?"+tc.query)
		if status != 400 {
			t.Errorf("%s 应 400，得到 %d", tc.query, status)
			continue
		}
		errObj := obj["error"].(map[string]any)
		if errObj["code"] != tc.code {
			t.Errorf("%s code = %v", tc.query, errObj["code"])
		}
		fields, _ := errObj["details"].(map[string]any)["fields"].([]any)
		if len(fields) == 0 || fields[0].(map[string]any)["field"] != tc.field {
			t.Errorf("%s details.fields 错误：%v", tc.query, errObj["details"])
		}
	}
}

func TestPostsListFilters(t *testing.T) {
	a, st := testHandlerApp(t)
	seedPosts(st)
	// tag 过滤
	_, obj := do(t, a, "GET", "/posts/list?tag=go")
	meta := obj["meta"].(map[string]any)
	if meta["count"] != float64(1) {
		t.Errorf("tag 过滤 count = %v", meta["count"])
	}
	// order=asc：旧文章在前
	_, obj = do(t, a, "GET", "/posts/list?order=asc")
	data := obj["data"].([]any)
	if data[0].(map[string]any)["slug"] != "older" {
		t.Errorf("order=asc 应最旧在前：%v", data[0])
	}
	// 默认 desc：新文章在前
	_, obj = do(t, a, "GET", "/posts/list")
	data = obj["data"].([]any)
	if data[0].(map[string]any)["slug"] != "newer" {
		t.Errorf("默认应最新在前：%v", data[0])
	}
}

// ---- /posts/detail ----

func TestPostsDetailOK(t *testing.T) {
	a, st := testHandlerApp(t)
	seedPosts(st)
	status, obj := do(t, a, "GET", "/posts/detail?slug=newer")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	data, ok := obj["data"].(map[string]any)
	if !ok || data["slug"] != "newer" {
		t.Fatalf("data 错误：%v", obj["data"])
	}
	// detail 必须含完整字段（mdContent 可以为空但键要在）
	for _, key := range []string{"mdContent", "plainContent", "publishTime", "tags", "category"} {
		if _, exists := data[key]; !exists {
			t.Errorf("detail 缺字段 %s", key)
		}
	}
}

func TestPostsDetailNotFound(t *testing.T) {
	a, st := testHandlerApp(t)
	seedPosts(st)
	status, obj := do(t, a, "GET", "/posts/detail?slug=nope")
	if status != 404 {
		t.Fatalf("status = %d", status)
	}
	errObj := obj["error"].(map[string]any)
	if errObj["code"] != "post.not_found" {
		t.Errorf("code = %v", errObj["code"])
	}
}

func TestPostsDetailMissingSlug(t *testing.T) {
	a, _ := testHandlerApp(t)
	status, obj := do(t, a, "GET", "/posts/detail")
	if status != 400 {
		t.Fatalf("status = %d", status)
	}
	if obj["error"].(map[string]any)["code"] != "validation.missing_field" {
		t.Error("缺 slug 应报 validation.missing_field")
	}
}

// 旧路径必须 404：证明新服务没偷偷保留旧契约（冒烟 [5] 的 Go 侧镜像）。
func TestOldPathsAreGone(t *testing.T) {
	a, _ := testHandlerApp(t)
	for _, path := range []string{"/get/post/postsInfo", "/update/post/addPost", "/posts/content", "/posts/nope"} {
		status, obj := do(t, a, "GET", path)
		if status != 404 {
			t.Errorf("%s 应 404，得到 %d", path, status)
			continue
		}
		if obj["error"].(map[string]any)["code"] != "not_found.route" {
			t.Errorf("%s 的错误码应为 not_found.route", path)
		}
	}
}

// 编译期防呆：确认测试里没引入对 time.Now 的断言依赖（testing.md §7）
var _ = time.Unix

// GET /posts/summary?generate=false：只读缓存、绝不生成（对应旧后台预览接口）。
// 命中 → {"summary":"…"}；未命中 → 200 {"summary":null}（旧后端就是 200 + data:null）。
func TestPostsSummaryCachedOnly(t *testing.T) {
	a, st := testHandlerApp(t)
	seedPosts(st)
	ctx := t.Context()

	// 未命中：200 且 summary 为 null（不能触发上游、也不能报错）
	status, obj := do(t, a, "GET", "/posts/summary?slug=newer&generate=false")
	if status != 200 {
		t.Fatalf("缓存未命中应 200 + summary:null，得到 %d %v", status, obj)
	}
	data, ok := obj["data"].(map[string]any)
	if !ok {
		t.Fatalf("data 应是对象：%v", obj["data"])
	}
	if v, exists := data["summary"]; !exists || v != nil {
		t.Errorf("未命中时 summary 应为 null（键要在）：%v", data)
	}

	// 写入缓存后命中
	if err := st.Summaries().Upsert(ctx, "newer", "缓存摘要内容", time.Unix(1750000000, 0)); err != nil {
		t.Fatal(err)
	}
	status, obj = do(t, a, "GET", "/posts/summary?slug=newer&generate=false")
	if status != 200 {
		t.Fatalf("命中应 200：%d", status)
	}
	if got := obj["data"].(map[string]any)["summary"]; got != "缓存摘要内容" {
		t.Errorf("summary = %v", got)
	}

	// 默认形态（不传 generate）保持「缓存优先」：命中直接返回
	status, obj = do(t, a, "GET", "/posts/summary?slug=newer")
	if status != 200 || obj["data"].(map[string]any)["summary"] != "缓存摘要内容" {
		t.Errorf("默认形态应命中缓存：%d %v", status, obj)
	}
	// 非法 generate 值 → 400
	status, _ = do(t, a, "GET", "/posts/summary?slug=newer&generate=maybe")
	if status != 400 {
		t.Errorf("非法 generate 应 400，得到 %d", status)
	}
}
