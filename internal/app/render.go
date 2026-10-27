package app

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"github.com/gofiber/fiber/v3"
)

// successBody 与 errorBody 分开定义，绝不共用一个带 omitempty 的结构体——
// 成功响应不该有 error 键、失败响应不该有 data 键，这是契约（api-draft §0.2）。
type successBody struct {
	OK        bool   `json:"ok"`
	RequestID string `json:"requestId"`
	Data      any    `json:"data"`
	Meta      Meta   `json:"meta,omitempty"`
}

type errorBody struct {
	OK        bool      `json:"ok"`
	RequestID string    `json:"requestId"`
	Error     errorItem `json:"error"`
}

type errorItem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"` // 没有 details 也要输出 null，键不能丢（冒烟测试会查）
}

// WithHeader 是 handler 需要「附带响应头」时的载体
// （theme-color 的 Cache-Control、限流的 Retry-After 都走这里——
// handler 自己仍然不碰 fiber，头由 Render 统一写出）。
type WithHeader struct {
	Data   any
	Header map[string]string
}

// Raw 是「非 JSON 响应」的唯一通道（目前只有 sitemap XML）。
// 仍然由 Render 统一写出——契约规定它是唯一的非信封例外。
type Raw struct {
	ContentType string
	Body        []byte
}

// WithStatus 是需要非 200 成功状态码时的包装（契约：POST create → 201）。
// Body 里还可以再包 WithMeta / WithHeader。
type WithStatus struct {
	Status int
	Body   any
}

// Render 是全服务唯一的响应出口。
//   - data 是 WithMeta 时信封带 meta；
//   - err 非 nil 时按 *apperr.Error 渲染失败信封；其它错误一律 internal.error，
//     原始错误串进 details.cause（个人项目排查效率优先，见 design-notes §1.2）。
func (a *App) Render(c *Ctx, data any, err error) error {
	// Raw 响应：跳过信封，直接写字节（sitemap XML）
	if raw, ok := data.(Raw); ok {
		c.req.Set(fiber.HeaderContentType, raw.ContentType)
		return c.req.Status(fiber.StatusOK).Send(raw.Body)
	}
	status := fiber.StatusOK
	if ws, ok := data.(WithStatus); ok {
		status = ws.Status
		data = ws.Body
	}
	if err != nil {
		// store.ErrReadOnly 是全局语义（只读验证模式），在渲染层统一映射成
		// 503 storage.read_only——service/handler 各自判断必然漏，漏一个就是 500。
		if errors.Is(err, store.ErrReadOnly) {
			err = apperr.New(apperr.StorageReadOnly,
				"服务正以只读模式运行（生产只读验证），写操作不可用", nil)
		}
		// panicError 单独归一：cause 要写 panic 值本身，不能只有 "handler panic"
		var pe panicError
		if errors.As(err, &pe) {
			err = apperr.Wrap(apperr.InternalError, "服务内部错误，请稍后重试",
				fmt.Errorf("%v", pe.r))
		}
		e := apperr.From(err)
		// 打日志：状态码、错误码、真实原因都在，前端报告问题时日志能直接对上
		slog.Warn("请求失败",
			"method", c.Method(), "path", c.Path(),
			"status", e.HTTPStatus(), "code", e.Code(),
			"cause", causeOf(e))
		return writeError(c.req, c.RequestID, e)
	}

	body := successBody{OK: true, RequestID: c.RequestID, Data: nil}
	if wm, ok := data.(WithMeta); ok {
		body.Data = wm.Data
		body.Meta = wm.Meta
	} else if wh, ok := data.(WithHeader); ok {
		for k, v := range wh.Header {
			c.req.Set(k, v)
		}
		body.Data = wh.Data
	} else {
		body.Data = data
	}
	return c.req.Status(status).JSON(body)
}

// writeError 写失败信封。fiber 错误处理器（404 等无 handler 场景）也复用它。
func writeError(fc fiber.Ctx, requestID string, e *apperr.Error) error {
	body := errorBody{
		OK:        false,
		RequestID: requestID,
		Error: errorItem{
			Code:    string(e.Code()),
			Message: e.Message(),
			Details: e.Details(),
		},
	}
	return fc.Status(e.HTTPStatus()).JSON(body)
}

// handleError 是 fiber 级错误处理器：处理没进 Render 的错误——
// 未匹配路由（fiber.ErrNotFound / ErrMethodNotAllowed）、中间件抛错、JSON 序列化失败。
func (a *App) handleError(fc fiber.Ctx, err error) error {
	if err == nil {
		return nil
	}
	requestID := middlewareRequestID(fc)
	// 未匹配路由 → not_found.route（含旧路径，冒烟测试验证它们没有被偷偷保留）
	if errors.Is(err, fiber.ErrNotFound) || errors.Is(err, fiber.ErrMethodNotAllowed) {
		return writeError(fc, requestID, apperr.New(
			apperr.NotFoundRoute, "接口不存在，请检查路径与方法（旧后端路径已全部下线）", nil))
	}
	// 其余错误统一 internal.error；真实原因进 cause
	e := apperr.From(err)
	slog.Error("未处理错误", "path", fc.Path(), "cause", err.Error())
	return writeError(fc, requestID, e)
}

// causeOf 提取 details.cause 供日志使用，避免日志里只有 code 看不到真实原因。
func causeOf(e *apperr.Error) string {
	switch d := e.Details().(type) {
	case apperr.CauseDetails:
		return d.Cause
	case apperr.UpstreamDetails:
		return fmt.Sprintf("%s (upstream=%s status=%d)", d.Cause, d.Upstream, d.Status)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%+v", d)
	}
}
