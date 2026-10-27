package app

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"runtime/debug"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/app/middleware"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/auth"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"github.com/gofiber/fiber/v3"
)

// App 是组装完成的 HTTP 服务：配置 + 存储 + fiber 实例 + 路由表。
// 依赖全部显式注入（main 里 config → store → app），没有全局变量。
type App struct {
	cfg        *config.Config
	st         store.Store
	fiber      *fiber.App
	routes     []Route
	jwt        *auth.JWT
	limiter    *middleware.RouteLimiters
	rateByPath map[string]struct {
		l        *middleware.RateLimiter
		requests int
	} // 路由级限流登记
}

// New 组装 fiber 实例并挂全局中间件（requestId → CORS）。
// 每条路由级的超时 / recover / 鉴权在 Add 的包装闭包里，按路由生效。
func New(cfg *config.Config, st store.Store) *App {
	a := &App{
		cfg:     cfg,
		st:      st,
		jwt:     auth.New(cfg.Auth.JWTSecret, cfg.Auth.JWTTTL.Std()),
		limiter: middleware.NewRouteLimiters(cfg),
		rateByPath: map[string]struct {
			l        *middleware.RateLimiter
			requests int
		}{},
	}
	// 路由级限流登记（契约 §0.7）
	a.rateByPath["/auth/login"] = struct {
		l        *middleware.RateLimiter
		requests int
	}{a.limiter.Login, cfg.RateLimit.Login.Requests}
	a.rateByPath["/links/applications/apply"] = struct {
		l        *middleware.RateLimiter
		requests int
	}{a.limiter.LinkApply, cfg.RateLimit.LinkApply.Requests}
	a.rateByPath["/links/theme-color"] = struct {
		l        *middleware.RateLimiter
		requests int
	}{a.limiter.ThemeColor, cfg.RateLimit.ThemeColor.Requests}
	a.fiber = fiber.New(fiber.Config{
		// 框架层 BodyLimit 设成 6MB，比我们自己的最大档位（图片 4MB）更宽：
		// 否则超限请求会先被 fasthttp 用纯文本拒掉，绕过统一信封；
		// 真正的档位判断在 routes.go 的 CheckBodySize（返回 image.too_large / validation 信封）。
		BodyLimit: 6 * 1024 * 1024,
		// JSON 序列化失败的兜底也走统一信封
		ErrorHandler: a.handleError,
		// Vercel 后面有代理，信任 X-Forwarded-For 才能拿到真实客户端 IP（限流键）
		EnableIPValidation: true,
		TrustProxy:         true,
		AppName:            "ariablognext-backend-go",
	})
	// requestId 在最外层：404 / panic 的响应也要带 requestId（冒烟测试会查）
	a.fiber.Use(middleware.RequestID())
	a.fiber.Use(middleware.Cors(cfg))
	return a
}

// Fiber 暴露底层实例给 main（监听用）；除 main 外不应该有人用它。
func (a *App) Fiber() *fiber.App { return a.fiber }

// Config 暴露配置（handler 注册时要用列表参数、时区等）。
func (a *App) Config() *config.Config { return a.cfg }

// JWT 暴露签发器（auth handler 用）。
func (a *App) JWT() *auth.JWT { return a.jwt }

// Store 暴露存储（handler 注册时取仓储）。
func (a *App) Store() store.Store { return a.st }

// Routes 返回路由表快照（测试断言 56 条路由全注册）。
func (a *App) Routes() []Route {
	out := make([]Route, len(a.routes))
	copy(out, a.routes)
	return out
}

// contextWithTimeout 给请求 ctx 派生超时。超时错误统一映射 504 timeout.exceeded
// （apperr.From 认 context.DeadlineExceeded）。
func contextWithTimeout(fc fiber.Ctx, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(fc.Context(), d)
}

// timeoutFor 查路由级超时：timeoutOverrides 命中路径用覆盖值，否则默认 8s。
func timeoutFor(cfg *config.Config, path string) time.Duration {
	if d, ok := cfg.Server.TimeoutOverrides[path]; ok && d.Std() > 0 {
		return d.Std()
	}
	return cfg.Server.RequestTimeout.Std()
}

// middlewareRequestID 从 fiber locals 里取 requestId。
func middlewareRequestID(fc fiber.Ctx) string {
	return middleware.FromRequestID(fc)
}

// logPanic 记录 panic 堆栈。panic 值原样打进日志（恢复现场用），
// 对外只暴露 internal.error，不泄露内部细节。
func logPanic(r any, c *Ctx) {
	// 栈只进日志，不回给客户端（契约规定不返回栈信息）
	slog.Error("handler panic",
		"path", c.Path(),
		"panic", r,
		"stack", string(debug.Stack()))
}

// isNilReader 判断 io.Reader 是否为 typed-nil 指针。
func isNilReader(r io.Reader) bool {
	if r == nil {
		return true
	}
	v := reflect.ValueOf(r)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return v.IsNil()
	default:
		return false
	}
}

// Test 是 fiber 自带的内存测试入口（完整 http.Request 控制）。
func (a *App) Test(req *http.Request) (*http.Response, error) {
	return a.fiber.Test(req)
}

// TestReq 是给 handler 单测用的便捷封装：不接触 net/http 也能发请求
// （handler 包被 checks.sh 禁止 import net/http，连测试文件也不行）。
// 返回状态码与响应体字节，测试自行 json.Unmarshal。
func (a *App) TestReq(method, target string, body io.Reader, header map[string]string) (int, []byte, error) {
	// typed-nil 的 io.Reader（如 (*bytes.Buffer)(nil)）不等于 nil，直接传给
	// http.NewRequest 会在读 body 时 panic；这里显式判空。
	if isNilReader(body) {
		body = nil
	}
	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequest(method, target, body)
	} else {
		req, err = http.NewRequest(method, target, nil)
	}
	if err != nil {
		return 0, nil, err
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	// 测试超时放宽到 10s：S3/LLM 这类外部调用会重试，1s 的默认值会让测试误判成死锁
	resp, err := a.fiber.Test(req, fiber.TestConfig{Timeout: 10 * time.Second, FailOnTimeout: true})
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, resp.Body); err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, buf.Bytes(), nil
}
