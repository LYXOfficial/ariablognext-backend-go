package app

import (
	"crypto/subtle"
	"strconv"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/app/middleware"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/gofiber/fiber/v3"
)

// Auth 是路由的鉴权级别。
//   - Public：任何人可访问；
//   - Admin：后台接口，要求 Authorization: Bearer <jwt>（P2 实现，先占位）；
//   - Internal：服务间调用（GitHub Actions / 备份），要求 X-Internal-Token。
type Auth int

const (
	Public Auth = iota
	Admin
	Internal
)

// Handler 是 handler 的唯一合法签名。
// 返回 (data, error)，响应由 Render 统一写出——写不进别的格式。
type Handler func(*Ctx) (any, error)

// Route 是一条路由的声明。全部路由集中列在 handler/register.go，
// app.Add 负责包中间件（超时、recover、鉴权、渲染）。
type Route struct {
	Method  string
	Path    string
	Auth    Auth
	Handler Handler
}

// Add 注册一条路由并包上统一中间件链。
// handler 的 panic 在这里被转成 internal.error，进程不退出；
// 每条路由的 ctx 带独立超时（timeoutOverrides 可按路径放宽）。
func (a *App) Add(r Route) {
	timeout := timeoutFor(a.cfg, r.Path)
	a.routes = append(a.routes, r)

	a.fiber.Add([]string{r.Method}, r.Path, func(fc fiber.Ctx) error {
		// ① 全局限流（每 IP 300/min）+ 路由级限流（login/apply/themeColor）
		ip := fc.IP()
		if !a.limiter.Global.Allow(ip, a.cfg.RateLimit.Global.Requests) {
			return a.rateLimited(fc, a.limiter.Global.RetryAfter(ip))
		}
		if rule, ok := a.rateByPath[r.Path]; ok {
			if !rule.l.Allow(ip, rule.requests) {
				return a.rateLimited(fc, rule.l.RetryAfter(ip))
			}
		}
		// ② 请求体上限（按路由档位，Content-Length 预检）
		if err := middleware.CheckBodySize(a.cfg, r.Path, int64(fc.Request().Header.ContentLength())); err != nil {
			return a.renderErrorDirect(fc, err)
		}

		ctx, cancel := contextWithTimeout(fc, timeout)
		defer cancel()

		c := &Ctx{
			req:       fc,
			cfg:       a.cfg,
			ctx:       ctx,
			RequestID: middlewareRequestID(fc),
			// 路由档位上限交给 Ctx 复核（覆盖 chunked 无 Content-Length 的情况）
			bodyLimit: int64(middleware.BodyLimitFor(a.cfg, r.Path).Int64()),
		}

		// ③ 鉴权：Admin = Bearer JWT；Internal = X-Internal-Token
		if err := a.authorize(c, r.Auth); err != nil {
			return a.Render(c, nil, err)
		}

		// ④ Safe 把 panic 转错误；handler 内部错误全部走 Render
		data, err := safeCall(r.Handler, c)
		return a.Render(c, data, err)
	})
}

// authorize 校验路由鉴权级别。
// Admin：Authorization: Bearer <jwt>（兼容裸 token，auth.Verify 内剥前缀）；
// Internal：X-Internal-Token == SECRET（服务间门牌，与 JWT 共用密钥——契约如此）。
func (a *App) authorize(c *Ctx, level Auth) error {
	switch level {
	case Public:
		return nil
	case Admin:
		claims, err := a.jwt.Verify(c.req.Get("Authorization"))
		if err != nil {
			return err
		}
		c.User = claims.Sub
		return nil
	case Internal:
		token := c.req.Get("X-Internal-Token")
		if token == "" {
			return apperr.New(apperr.AuthMissingToken, "缺少服务间调用凭证（X-Internal-Token）", nil)
		}
		// 常量时间比较：`!=` 会按首个不同字节提前返回，理论上可被逐字节试探出令牌。
		// 个人项目风险很低，但这是一行的事，没有理由留侧信道。
		if subtle.ConstantTimeCompare([]byte(token), []byte(a.cfg.Auth.InternalToken)) != 1 {
			return apperr.New(apperr.AuthInvalidToken, "服务间调用凭证无效", nil)
		}
		return nil
	default:
		return apperr.New(apperr.InternalError, "未知的鉴权级别", nil)
	}
}

// rateLimited 写 429 + Retry-After。Retry-After 头由这一层直接挂
// （Render 的错误路径不带 header 能力，限流是横切关注点，放这里合适）。
func (a *App) rateLimited(fc fiber.Ctx, retryAfter int) error {
	fc.Set("Retry-After", strconv.Itoa(retryAfter))
	return a.renderErrorDirect(fc, apperr.New(apperr.RateLimitExceeded,
		"请求太频繁了，请稍后再试", nil))
}

// renderErrorDirect 供中间件层（还没有 *Ctx 时）直接写错误信封。
func (a *App) renderErrorDirect(fc fiber.Ctx, err error) error {
	e := apperr.From(err)
	return writeError(fc, middleware.FromRequestID(fc), e)
}

// safeCall 执行 handler，把 panic 转成 panicError（渲染层映射 internal.error/500）。
func safeCall(h Handler, c *Ctx) (data any, err error) {
	defer func() {
		if r := recover(); r != nil {
			logPanic(r, c)
			data = nil
			err = panicError{r: r}
		}
	}()
	return h(c)
}

// panicError 携带 panic 值；字符串化交给渲染层（panic 值可能是任何类型）。
type panicError struct{ r any }

func (p panicError) Error() string { return "handler panic" }
