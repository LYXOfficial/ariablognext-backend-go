package app

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/app/middleware"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

// Ctx 的读取能力逐项覆盖（它们是所有 handler 的参数入口，错一个全错）。

func newCtxApp(t *testing.T) testApp {
	return newTestApp(t)
}

func TestCtxQueryHelpers(t *testing.T) {
	a := newCtxApp(t)
	a.Add(Route{Method: "GET", Path: "/q", Handler: func(c *Ctx) (any, error) {
		out := map[string]any{
			"plain":   c.Query("x"),
			"def":     c.QueryDefault("y", "fallback"),
			"header":  c.Header("X-Test"),
			"method":  c.Method(),
			"path":    c.Path(),
			"ip":      c.IP(),
			"bodyNil": len(mustBody(c)) == 0,
		}
		return out, nil
	}})
	status, raw, err := a.TestReq("GET", "/q?x=1", nil, map[string]string{"X-Test": "h"})
	if err != nil || status != 200 {
		t.Fatalf("status=%d err=%v", status, err)
	}
	body := string(raw)
	if !strings.Contains(body, `"plain":"1"`) || !strings.Contains(body, `"def":"fallback"`) ||
		!strings.Contains(body, `"header":"h"`) || !strings.Contains(body, `"method":"GET"`) ||
		!strings.Contains(body, `"path":"/q"`) {
		t.Errorf("Ctx 读取能力异常：%s", body)
	}
}

func mustBody(c *Ctx) []byte {
	b, _ := c.RawBody()
	return b
}

func TestCtxIntBoolPageSize(t *testing.T) {
	a := newCtxApp(t)
	a.Add(Route{Method: "GET", Path: "/n", Handler: func(c *Ctx) (any, error) {
		page, err := c.Int("page", 1, 1, 100)
		if err != nil {
			return nil, err
		}
		limit, _ := c.Int("limit", 6, 1, 20)
		flag, err := c.Bool("flag", false)
		if err != nil {
			return nil, err
		}
		ps, err := c.PageSize()
		if err != nil {
			return nil, err
		}
		return map[string]any{"page": page, "limit": limit, "flag": flag, "ps": ps.Value(), "full": ps.Full}, nil
	}})

	// 正常值
	status, raw, _ := a.TestReq("GET", "/n?page=2&limit=10&flag=true&pageSize=full", nil, nil)
	if status != 200 || !strings.Contains(string(raw), `"page":2`) ||
		!strings.Contains(string(raw), `"ps":"full"`) || !strings.Contains(string(raw), `"flag":true`) {
		t.Errorf("正常参数解析异常：%d %s", status, raw)
	}
	// 缺省值
	status, raw, _ = a.TestReq("GET", "/n", nil, nil)
	if status != 200 || !strings.Contains(string(raw), `"ps":16`) {
		t.Errorf("缺省值异常：%d %s", status, raw)
	}
	// 越界/非法 → 400 + 字段名
	for _, q := range []string{"page=0", "page=abc", "page=999", "flag=maybe", "pageSize=0", "pageSize=999"} {
		status, raw, _ = a.TestReq("GET", "/n?"+q, nil, nil)
		if status != 400 {
			t.Errorf("%s 应 400，得到 %d", q, status)
			continue
		}
		var obj map[string]any
		_ = jsonUnmarshal(raw, &obj)
		e := obj["error"].(map[string]any)
		if e["code"] != "validation.invalid_field" {
			t.Errorf("%s code = %v", q, e["code"])
		}
		if !strings.Contains(string(raw), "fields") {
			t.Errorf("%s 缺 details.fields：%s", q, raw)
		}
	}
}

func TestCtxRequiredQueryAndBind(t *testing.T) {
	a := newCtxApp(t)
	a.Add(Route{Method: "POST", Path: "/b", Handler: func(c *Ctx) (any, error) {
		slug, err := c.RequiredQuery("slug")
		if err != nil {
			return nil, err
		}
		var body struct {
			N int `json:"n"`
		}
		if err := c.Bind(&body); err != nil {
			return nil, err
		}
		return map[string]any{"slug": slug, "n": body.N}, nil
	}})
	// 缺必填
	status, raw, _ := a.TestReq("POST", "/b", strings.NewReader(`{"n":1}`), jsonHeaderTest())
	if status != 400 || !strings.Contains(string(raw), "validation.missing_field") {
		t.Errorf("缺必填参数应 400：%d %s", status, raw)
	}
	// 正常
	status, raw, _ = a.TestReq("POST", "/b?slug=x", strings.NewReader(`{"n":7}`), jsonHeaderTest())
	if status != 200 || !strings.Contains(string(raw), `"n":7`) {
		t.Errorf("Bind 异常：%d %s", status, raw)
	}
	// 非法 JSON → validation.invalid_field
	status, raw, _ = a.TestReq("POST", "/b?slug=x", strings.NewReader(`{bad`), jsonHeaderTest())
	if status != 400 || !strings.Contains(string(raw), "validation.invalid_field") {
		t.Errorf("非法 JSON 应 400：%d %s", status, raw)
	}
}

func jsonHeaderTest() map[string]string {
	return map[string]string{"Content-Type": "application/json"}
}

func TestRenderRawAndStatusAndHeader(t *testing.T) {
	a := newCtxApp(t)
	a.Add(Route{Method: "GET", Path: "/raw", Handler: func(c *Ctx) (any, error) {
		return Raw{ContentType: "application/xml; charset=utf-8", Body: []byte("<x>1</x>")}, nil
	}})
	status, raw, _ := a.TestReq("GET", "/raw", nil, nil)
	if status != 200 || string(raw) != "<x>1</x>" {
		t.Errorf("Raw 响应异常：%d %s", status, raw)
	}

	a.Add(Route{Method: "POST", Path: "/created", Handler: func(c *Ctx) (any, error) {
		return WithStatus{Status: http.StatusCreated, Body: map[string]any{"slug": "x"}}, nil
	}})
	status, raw, _ = a.TestReq("POST", "/created", strings.NewReader("{}"), jsonHeaderTest())
	if status != 201 || !strings.Contains(string(raw), `"ok":true`) {
		t.Errorf("WithStatus 异常：%d %s", status, raw)
	}

	a.Add(Route{Method: "GET", Path: "/hdr", Handler: func(c *Ctx) (any, error) {
		return WithHeader{Data: map[string]any{"color": "#fff"}, Header: map[string]string{"Cache-Control": "public, max-age=86400"}}, nil
	}})
	// 需要看响应头：用 a.Test 直接拿 http.Response
	req, _ := http.NewRequest("GET", "/hdr", nil)
	resp, err := a.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Cache-Control") != "public, max-age=86400" {
		t.Errorf("WithHeader 未生效：%v", resp.Header)
	}
}

func TestRenderRateLimitAndReadOnly(t *testing.T) {
	a := newCtxApp(t)
	a.Add(Route{Method: "GET", Path: "/limited", Handler: func(c *Ctx) (any, error) {
		return nil, apperr.New(apperr.RateLimitExceeded, "太频繁", nil)
	}})
	// 限流错误 + Retry-After 由 wrapper 写；handler 内直接返回时至少状态码对
	status, raw, _ := a.TestReq("GET", "/limited", nil, nil)
	if status != 429 {
		t.Errorf("429 期望，得到 %d %s", status, raw)
	}
}

func TestAuthLevels(t *testing.T) {
	a := newCtxApp(t)
	a.Add(Route{Method: "GET", Path: "/admin", Auth: Admin, Handler: func(c *Ctx) (any, error) {
		return map[string]any{"user": c.User}, nil
	}})
	a.Add(Route{Method: "GET", Path: "/internal", Auth: Internal, Handler: func(c *Ctx) (any, error) {
		return map[string]any{"ok": true}, nil
	}})

	// 无 token
	status, raw, _ := a.TestReq("GET", "/admin", nil, nil)
	if status != 401 || !strings.Contains(string(raw), "auth.missing_token") {
		t.Errorf("后台无 token 应 401：%d %s", status, raw)
	}
	// 正确 JWT → 通过，c.User 填充
	token, err := a.JWT().Issue("tester")
	if err != nil {
		t.Fatal(err)
	}
	status, raw, _ = a.TestReq("GET", "/admin", nil, map[string]string{"Authorization": "Bearer " + token})
	if status != 200 || !strings.Contains(string(raw), `"user":"tester"`) {
		t.Errorf("合法 JWT 应通过：%d %s", status, raw)
	}
	// 非法 JWT
	status, raw, _ = a.TestReq("GET", "/admin", nil, map[string]string{"Authorization": "Bearer bad.token.here"})
	if status != 401 || !strings.Contains(string(raw), "auth.invalid_token") {
		t.Errorf("非法 JWT 应 401：%d %s", status, raw)
	}
	// 内部令牌：缺 / 错 / 对
	status, raw, _ = a.TestReq("GET", "/internal", nil, nil)
	if status != 401 || !strings.Contains(string(raw), "auth.missing_token") {
		t.Errorf("内部无令牌应 401：%d %s", status, raw)
	}
	status, raw, _ = a.TestReq("GET", "/internal", nil, map[string]string{"X-Internal-Token": "wrong"})
	if status != 401 || !strings.Contains(string(raw), "auth.invalid_token") {
		t.Errorf("内部错令牌应 401：%d %s", status, raw)
	}
	status, _, _ = a.TestReq("GET", "/internal", nil, map[string]string{"X-Internal-Token": a.Config().Auth.InternalToken})
	if status != 200 {
		t.Errorf("内部正确令牌应 200：%d", status)
	}
	// 未知鉴权级别 → 500（fail-closed）
	a.Add(Route{Method: "GET", Path: "/weird", Auth: Auth(99), Handler: func(c *Ctx) (any, error) {
		return nil, nil
	}})
	status, raw, _ = a.TestReq("GET", "/weird", nil, nil)
	if status != 500 || !strings.Contains(string(raw), "internal.error") {
		t.Errorf("未知鉴权级别应 fail-closed：%d %s", status, raw)
	}
}

func TestFormFileAndValue(t *testing.T) {
	a := newCtxApp(t)
	a.Add(Route{Method: "POST", Path: "/form", Handler: func(c *Ctx) (any, error) {
		data, name, err := c.FormFile("file")
		if err != nil {
			return nil, err
		}
		return map[string]any{"len": len(data), "name": name, "extra": c.FormValue("extra")}, nil
	}})
	// 构造 multipart 请求体
	body, contentType := multipartBody(t, "file", "a.png", []byte("png-bytes"), "extra", "v1")
	status, raw, err := a.TestReq("POST", "/form", body, map[string]string{"Content-Type": contentType})
	if err != nil || status != 200 {
		t.Fatalf("表单解析失败：%d %v %s", status, err, raw)
	}
	if !strings.Contains(string(raw), `"len":9`) || !strings.Contains(string(raw), `"extra":"v1"`) {
		t.Errorf("表单字段异常：%s", raw)
	}
	// 缺文件字段 → validation.missing_field
	body2, ct2 := multipartBody(t, "other", "x", []byte("z"), "extra", "v")
	status, raw, _ = a.TestReq("POST", "/form", body2, map[string]string{"Content-Type": ct2})
	if status != 400 || !strings.Contains(string(raw), "validation.missing_field") {
		t.Errorf("缺文件字段应 400：%d %s", status, raw)
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	// 收紧全局限流：第 2 次请求应 429 + Retry-After
	a := newTestApp(t)
	a.cfg.RateLimit.Global = config.RateRule{Requests: 1, Window: config.Duration(time.Minute)}
	a.limiter = middleware.NewRouteLimiters(a.cfg)
	a.Add(Route{Method: "GET", Path: "/rl", Handler: func(c *Ctx) (any, error) {
		return map[string]any{"ok": true}, nil
	}})
	status, _, _ := a.TestReq("GET", "/rl", nil, nil)
	if status != 200 {
		t.Fatalf("首次应 200：%d", status)
	}
	status, raw, _ := a.TestReq("GET", "/rl", nil, nil)
	if status != 429 || !strings.Contains(string(raw), "rate_limit.exceeded") {
		t.Fatalf("第二次应 429：%d %s", status, raw)
	}
}

func TestBodyLimitMiddleware(t *testing.T) {
	a := newTestApp(t)
	a.cfg.Server.BodyLimit = config.BodyLimitConfig{Default: 10, Markdown: 100, Image: 4096}
	a.Add(Route{Method: "POST", Path: "/small", Handler: func(c *Ctx) (any, error) {
		return map[string]any{"ok": true}, nil
	}})
	big := strings.Repeat("x", 50)
	status, raw, _ := a.TestReq("POST", "/small", strings.NewReader(big), jsonHeaderTest())
	if status != 400 || !strings.Contains(string(raw), "validation.invalid_field") {
		t.Fatalf("超过默认上限应 400：%d %s", status, raw)
	}
}

func TestHandleErrorNil(t *testing.T) {
	a := newTestApp(t)
	if err := a.handleError(nil, nil); err != nil {
		t.Errorf("nil 错误应返回 nil：%v", err)
	}
}

func TestWithMetaAndRoutesAccessors(t *testing.T) {
	a := newTestApp(t)
	a.Add(Route{Method: "GET", Path: "/x", Handler: func(c *Ctx) (any, error) { return nil, nil }})
	if len(a.Routes()) != 1 || a.Routes()[0].Path != "/x" {
		t.Errorf("Routes() 异常：%+v", a.Routes())
	}
	if a.Fiber() == nil || a.Store() == nil || a.Config() == nil || a.JWT() == nil {
		t.Error("访问器不应返回 nil")
	}
}

func TestCtxContextAndRouteLimit(t *testing.T) {
	a := newTestApp(t)
	a.Add(Route{Method: "GET", Path: "/ctx", Handler: func(c *Ctx) (any, error) {
		if c.Context() == nil {
			return nil, apperr.New(apperr.InternalError, "ctx 为空", nil)
		}
		if _, ok := c.Context().Deadline(); !ok {
			return nil, apperr.New(apperr.InternalError, "ctx 没有超时", nil)
		}
		return map[string]any{"ok": true}, nil
	}})
	if status, raw, _ := a.TestReq("GET", "/ctx", nil, nil); status != 200 {
		t.Fatalf("请求上下文应带超时：%d %s", status, raw)
	}

	// 路由级限流（login 规则）到点后 429
	a2 := newTestApp(t)
	a2.cfg.RateLimit.Login = config.RateRule{Requests: 2, Window: config.Duration(time.Minute)}
	a2.limiter = middleware.NewRouteLimiters(a2.cfg)
	a2.rateByPath["/auth/login"] = struct {
		l        *middleware.RateLimiter
		requests int
	}{a2.limiter.Login, 2}
	a2.Add(Route{Method: "POST", Path: "/auth/login", Handler: func(c *Ctx) (any, error) {
		return map[string]any{"token": "t"}, nil
	}})
	for i := 0; i < 2; i++ {
		if status, _, _ := a2.TestReq("POST", "/auth/login", strings.NewReader("{}"), jsonHeaderTest()); status != 200 {
			t.Fatalf("第 %d 次应 200：%d", i+1, status)
		}
	}
	status, raw, _ := a2.TestReq("POST", "/auth/login", strings.NewReader("{}"), jsonHeaderTest())
	if status != 429 {
		t.Fatalf("路由级限流应 429：%d %s", status, raw)
	}
}

func TestRenderReadOnlyErrMapsTo503(t *testing.T) {
	// 只读验证模式下所有写路径都可能拿到 store.ErrReadOnly：
	// 必须统一映射成 503 storage.read_only（不是 500 internal.error）。
	a := newTestApp(t)
	a.Add(Route{Method: "POST", Path: "/write", Handler: func(c *Ctx) (any, error) {
		return nil, store.ErrReadOnly
	}})
	status, raw, _ := a.TestReq("POST", "/write", strings.NewReader("{}"), jsonHeaderTest())
	if status != 503 {
		t.Fatalf("只读模式应 503，得到 %d：%s", status, raw)
	}
	if !strings.Contains(string(raw), "storage.read_only") {
		t.Errorf("错误码应为 storage.read_only：%s", raw)
	}
}

// BindOptional：空 body 视为 {}（对应 /site/update-time 这类「body 整体可选」的接口）。
// 这条是 app 覆盖率从 89.9% 回到 90%+ 的关键分支之一，也钉住了 G-001 的底层能力。
func TestCtxBindOptional(t *testing.T) {
	a := newTestApp(t)
	a.Add(Route{Method: "POST", Path: "/opt", Handler: func(c *Ctx) (any, error) {
		var body struct {
			Time *int64 `json:"time"`
		}
		if err := c.BindOptional(&body); err != nil {
			return nil, err
		}
		return map[string]any{"hasTime": body.Time != nil}, nil
	}})

	// ① 完全无 body → 不报错，字段保持零值
	status, raw, err := a.TestReq("POST", "/opt", nil, nil)
	if err != nil || status != 200 || !strings.Contains(string(raw), `"hasTime":false`) {
		t.Fatalf("空 body 应视为 {}：%d %v %s", status, err, raw)
	}
	// ② 有 body → 正常解析
	status, raw, _ = a.TestReq("POST", "/opt", strings.NewReader(`{"time":1234567890}`), jsonHeaderTest())
	if status != 200 || !strings.Contains(string(raw), `"hasTime":true`) {
		t.Errorf("有 body 应正常解析：%d %s", status, raw)
	}
	// ③ 非法 body → 仍然报错（不能因为 optional 就吞掉格式错误）
	status, raw, _ = a.TestReq("POST", "/opt", strings.NewReader(`{bad`), jsonHeaderTest())
	if status != 400 || !strings.Contains(string(raw), "validation.invalid_field") {
		t.Errorf("非法 body 应 400：%d %s", status, raw)
	}
	// ④ 空 body 但带 Content-Type 也不报错
	status, _, _ = a.TestReq("POST", "/opt", nil, jsonHeaderTest())
	if status != 200 {
		t.Errorf("空 body + JSON header 应 200：%d", status)
	}
}

// RawBody 在空请求体时返回空切片（不是 nil），供 P3 的原始转发/multipart 用。
func TestCtxRawBodyEmpty(t *testing.T) {
	a := newTestApp(t)
	a.Add(Route{Method: "POST", Path: "/raw", Handler: func(c *Ctx) (any, error) {
		b, err := c.RawBody()
		return map[string]any{"len": len(b), "nil": b == nil}, err
	}})
	status, raw, _ := a.TestReq("POST", "/raw", nil, nil)
	if status != 200 || !strings.Contains(string(raw), `"nil":false`) {
		t.Errorf("空 body 的 RawBody 应是空切片不是 nil：%d %s", status, raw)
	}
}

// Meta 的两种形状只能经构造函数产生（私有方法收口），这里把两种都断言一遍。
func TestMetaConstructors(t *testing.T) {
	var list Meta = NewListMeta(2, PageSize{N: 16}, 40, true)
	var count Meta = NewCountMeta(7)
	if _, ok := list.(ListMeta); !ok {
		t.Errorf("NewListMeta 应返回 ListMeta：%T", list)
	}
	if _, ok := count.(CountMeta); !ok {
		t.Errorf("NewCountMeta 应返回 CountMeta：%T", count)
	}
	// full 形态
	if got := (PageSize{Full: true}).Value(); got != "full" {
		t.Errorf("full 的 pageSize 应是字符串 full：%v", got)
	}
	if got := (PageSize{N: 9}).Value(); got != 9 {
		t.Errorf("数字 pageSize 应是 int：%v", got)
	}
}

// 分块传输（无 Content-Length）也要受路由档位限制：
// 中间件的预检只看 Content-Length，chunked 请求没有这个头，只靠框架 6MB 兜底会放过
// 1MB 档位的 2MB 请求。这里用「长度未知的 reader」构造 chunked 请求验证。
type unknownLenReader struct{ r *strings.Reader }

func (u unknownLenReader) Read(p []byte) (int, error) { return u.r.Read(p) }
func (u unknownLenReader) Close() error               { return nil }

func TestChunkedBodyRespectsRouteLimit(t *testing.T) {
	a := newTestApp(t)
	a.cfg.Server.BodyLimit = config.BodyLimitConfig{Default: 1 * config.KB, Markdown: 2 * config.KB, Image: 4 * config.KB}
	a.Add(Route{Method: "POST", Path: "/small", Handler: func(c *Ctx) (any, error) {
		var body map[string]any
		if err := c.Bind(&body); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	}})

	// 起真实监听：fiber.Test 会自己拼 dump（还会写出 Content-Length: -1），
	// 只有真 socket 才能发出合法的 chunked 请求（无 Content-Length）。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = a.Fiber().Listener(ln) }()
	defer func() { _ = a.Fiber().Shutdown() }()

	payload := `{"x":"` + strings.Repeat("a", 2*1024) + `"}`
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte(payload))
		_ = pw.Close()
	}()
	req, err := http.NewRequest("POST", "http://"+ln.Addr().String()+"/small", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	// io.Pipe 的长度未知 → net/http 会自动用 Transfer-Encoding: chunked
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 400 {
		t.Fatalf("chunked 超限应 400，得到 %d：%s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "请求体超过上限") {
		t.Errorf("文案应说明体积超限：%s", raw)
	}
}

// 内部令牌比较用常量时间：错令牌必须 401（行为不变），且不同长度的令牌也不会 panic。
func TestInternalTokenComparison(t *testing.T) {
	a := newTestApp(t)
	a.Add(Route{Method: "GET", Path: "/internal", Auth: Internal, Handler: func(c *Ctx) (any, error) {
		return map[string]any{"ok": true}, nil
	}})
	good := a.Config().Auth.InternalToken
	cases := []struct {
		token string
		want  int
	}{
		{good, 200},
		{"x", 401},                // 长度不同
		{good + "x", 401},         // 前缀相同但更长
		{good[:len(good)-1], 401}, // 少一个字符
	}
	for _, c := range cases {
		status, raw, _ := a.TestReq("GET", "/internal", nil, map[string]string{"X-Internal-Token": c.token})
		if status != c.want {
			t.Errorf("token=%q 期望 %d，得到 %d：%s", c.token, c.want, status, raw)
		}
	}
}
