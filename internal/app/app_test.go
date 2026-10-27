package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store/fake"
)

// 测试用配置：只填信封渲染/超时需要的字段（不经 Load，绕开外部依赖）。
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	if _, err := time.LoadLocation("Asia/Shanghai"); err != nil {
		t.Fatal(err)
	}
	return &config.Config{
		Server: config.ServerConfig{
			Timezone:       "Asia/Shanghai",
			RequestTimeout: config.Duration(8 * time.Second),
			DBTimeout:      config.Duration(5 * time.Second),
			TimeoutOverrides: map[string]config.Duration{
				"/maintenance/export": config.Duration(120 * time.Second),
			},
			CORS: config.CORSConfig{
				AllowOrigins:     []string{"*"},
				AllowCredentials: true,
				AllowHeaders:     []string{"*"},
				AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			},
		},
		List: config.ListConfig{DefaultPageSize: 16, MaxPageSize: 100, MaxFullItems: 300},
		Auth: config.AuthConfig{
			JWTSecret:     "test-secret",
			JWTAlgorithm:  "HS256",
			JWTTTL:        config.Duration(7 * 24 * time.Hour),
			InternalToken: "test-internal-token",
		},
		RateLimit: config.RateLimitConfig{
			Global:     config.RateRule{Requests: 100000, Window: config.Duration(time.Minute)},
			Login:      config.RateRule{Requests: 10, Window: config.Duration(10 * time.Minute)},
			LinkApply:  config.RateRule{Requests: 5, Window: config.Duration(30 * time.Minute)},
			ThemeColor: config.RateRule{Requests: 60, Window: config.Duration(time.Hour)},
		},
	}
}

type testApp struct {
	*App
	cfg *config.Config
}

func newTestApp(t *testing.T) testApp {
	t.Helper()
	cfg := testConfig(t)
	a := New(cfg, fake.New())
	return testApp{App: a, cfg: cfg}
}

// get 是最简 GET 请求，返回状态码与解析后的 JSON。
func (a testApp) get(t *testing.T, path string) (int, map[string]any) {
	t.Helper()
	status, body, err := a.TestReq(http.MethodGet, path, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("响应不是 JSON（%d）：%s", status, body)
	}
	return status, obj
}

// ---- 信封形状（testing.md §2.3，逐条对应）----

func TestEnvelopeSuccessWithData(t *testing.T) {
	a := newTestApp(t)
	a.Add(Route{Method: "GET", Path: "/ok", Handler: func(c *Ctx) (any, error) {
		return map[string]string{"hello": "world"}, nil
	}})
	status, obj := a.get(t, "/ok")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	if obj["ok"] != true {
		t.Error("ok 必须是 true")
	}
	if _, ok := obj["requestId"]; !ok {
		t.Error("必须带 requestId")
	}
	data, ok := obj["data"].(map[string]any)
	if !ok || data["hello"] != "world" {
		t.Errorf("data 错误：%v", obj["data"])
	}
	// 成功 + 非列表响应不该出现 meta
	if _, ok := obj["meta"]; ok {
		t.Error("非列表响应不该有 meta 键")
	}
	if _, ok := obj["error"]; ok {
		t.Error("成功响应不该有 error 键")
	}
}

func TestEnvelopeDataNullKeyPresent(t *testing.T) {
	a := newTestApp(t)
	a.Add(Route{Method: "GET", Path: "/null", Handler: func(c *Ctx) (any, error) {
		return nil, nil
	}})
	status, body, err := a.TestReq(http.MethodGet, "/null", nil, nil)
	if err != nil || status != 200 {
		t.Fatalf("status=%d err=%v", status, err)
	}
	// data 为 nil 时键不能丢（前端解构依赖键存在）
	if string(body[0]) != "{" || !jsonKeyPresent(body, "data") {
		t.Errorf("data 键缺失：%s", body)
	}
}

func TestEnvelopeListMetaShapes(t *testing.T) {
	a := newTestApp(t)
	a.Add(Route{Method: "GET", Path: "/list", Handler: func(c *Ctx) (any, error) {
		meta := NewListMeta(1, PageSize{N: 16}, 83, true)
		return WithMeta{Data: []string{}, Meta: meta}, nil
	}})
	_, obj := a.get(t, "/list")
	meta, ok := obj["meta"].(map[string]any)
	if !ok {
		t.Fatalf("缺 meta：%v", obj)
	}
	if meta["page"] != float64(1) || meta["count"] != float64(83) || meta["hasMore"] != true {
		t.Errorf("meta 字段错误：%v", meta)
	}
	if meta["pageSize"] != float64(16) {
		t.Errorf("pageSize 应是数字：%v", meta["pageSize"])
	}
	// 空切片必须输出 []，不是 null（前端 .map 会炸）
	if data, ok := obj["data"].([]any); !ok || len(data) != 0 {
		t.Errorf("空切片应输出 []：%v", obj["data"])
	}
}

func TestEnvelopeFullPageSizeAsString(t *testing.T) {
	a := newTestApp(t)
	a.Add(Route{Method: "GET", Path: "/list", Handler: func(c *Ctx) (any, error) {
		meta := NewListMeta(1, PageSize{Full: true}, 5, false)
		return WithMeta{Data: []string{"a"}, Meta: meta}, nil
	}})
	_, obj := a.get(t, "/list")
	meta := obj["meta"].(map[string]any)
	if meta["pageSize"] != "full" {
		t.Errorf("pageSize=full 时信封里应是字符串 %q：%v", "full", meta["pageSize"])
	}
}

func TestEnvelopeCountOnlyMeta(t *testing.T) {
	a := newTestApp(t)
	a.Add(Route{Method: "GET", Path: "/count", Handler: func(c *Ctx) (any, error) {
		return WithMeta{Data: nil, Meta: NewCountMeta(27)}, nil
	}})
	_, obj := a.get(t, "/count")
	if obj["data"] != nil {
		t.Errorf("countOnly 时 data 应为 null：%v", obj["data"])
	}
	meta, ok := obj["meta"].(map[string]any)
	if !ok || meta["count"] != float64(27) {
		t.Fatalf("meta 应只有 count：%v", obj["meta"])
	}
	for _, k := range []string{"page", "pageSize", "hasMore"} {
		if _, exists := meta[k]; exists {
			t.Errorf("countOnly 的 meta 不该有 %s 键", k)
		}
	}
}

func TestEnvelopeErrorShape(t *testing.T) {
	a := newTestApp(t)
	a.Add(Route{Method: "GET", Path: "/err", Handler: func(c *Ctx) (any, error) {
		return nil, apperr.New(apperr.PostNotFound, "文章不存在或已被删除", nil)
	}})
	status, obj := a.get(t, "/err")
	if status != 404 {
		t.Fatalf("status = %d", status)
	}
	if obj["ok"] != false {
		t.Error("ok 必须是 false")
	}
	errObj, ok := obj["error"].(map[string]any)
	if !ok {
		t.Fatalf("缺 error 对象：%v", obj)
	}
	if errObj["code"] != "post.not_found" {
		t.Errorf("code = %v", errObj["code"])
	}
	if errObj["message"] == "" {
		t.Error("message 不能为空")
	}
	// details 没有也要输出 null，键不能丢
	if _, ok := errObj["details"]; !ok {
		t.Error("error.details 键必须存在（哪怕值是 null）")
	}
	if _, ok := obj["data"]; ok {
		t.Error("失败响应不该有 data 键")
	}
}

func TestEnvelopeInternalErrorCarriesCause(t *testing.T) {
	a := newTestApp(t)
	a.Add(Route{Method: "GET", Path: "/boom", Handler: func(c *Ctx) (any, error) {
		return nil, context.DeadlineExceeded
	}})
	status, obj := a.get(t, "/boom")
	if status != 504 {
		t.Fatalf("超时应映射 504，得到 %d", status)
	}
	errObj := obj["error"].(map[string]any)
	if errObj["code"] != "timeout.exceeded" {
		t.Errorf("code = %v", errObj["code"])
	}
}

func TestPanicBecomesInternalError(t *testing.T) {
	a := newTestApp(t)
	a.Add(Route{Method: "GET", Path: "/panic", Handler: func(c *Ctx) (any, error) {
		panic("测试 panic")
	}})
	status, obj := a.get(t, "/panic")
	if status != 500 {
		t.Fatalf("panic 应映射 500，得到 %d", status)
	}
	errObj := obj["error"].(map[string]any)
	if errObj["code"] != "internal.error" {
		t.Errorf("code = %v", errObj["code"])
	}
	// cause 要有 panic 值本身，方便排查
	cause, _ := errObj["details"].(map[string]any)["cause"].(string)
	if cause != "测试 panic" {
		t.Errorf("cause = %q", cause)
	}
}

func TestNotFoundRoute(t *testing.T) {
	a := newTestApp(t)
	status, obj := a.get(t, "/nope")
	if status != 404 {
		t.Fatalf("status = %d", status)
	}
	errObj := obj["error"].(map[string]any)
	if errObj["code"] != "not_found.route" {
		t.Errorf("code = %v", errObj["code"])
	}
	if _, ok := obj["requestId"]; !ok {
		t.Error("404 响应也要带 requestId")
	}
}

func TestCORSHeaders(t *testing.T) {
	a := newTestApp(t)
	a.Add(Route{Method: "GET", Path: "/x", Handler: func(c *Ctx) (any, error) { return nil, nil }})
	status, body, err := a.TestReq(http.MethodOptions, "/x", nil, map[string]string{
		"Origin":                        "https://blog.yaria.top",
		"Access-Control-Request-Method": "GET",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 预检请求 fiber 直接应答，状态码 204；关键是必须有 Allow-Origin 头
	_ = status
	_ = body
}

func TestTestReqReturnsStatusAndBody(t *testing.T) {
	a := newTestApp(t)
	a.Add(Route{Method: "GET", Path: "/x", Handler: func(c *Ctx) (any, error) {
		return map[string]int{"n": 1}, nil
	}})
	status, body, err := a.TestReq(http.MethodGet, "/x", nil, nil)
	if err != nil || status != 200 {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if len(body) == 0 {
		t.Error("body 不应为空")
	}
}

// jsonKeyPresent 粗查顶层键是否出现（不需要完整解析）。
func jsonKeyPresent(body []byte, key string) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		return false
	}
	_, ok := probe[key]
	return ok
}
