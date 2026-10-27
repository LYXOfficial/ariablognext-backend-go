package app

import (
	"context"
	"io"
	"strconv"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/gofiber/fiber/v3"
)

// Ctx 是 handler 的请求上下文：只有读能力。
// 没有写响应的任何手段——这是信封统一的结构性保证，不是自觉。
type Ctx struct {
	req       fiber.Ctx
	cfg       *config.Config
	ctx       context.Context
	RequestID string
	User      string // 鉴权后由 auth 中间件填充（P2）
	// bodyLimit 是本条路由的请求体上限（字节）。由 Add 按路由档位写入，
	// 用于兜住「分块传输不带 Content-Length」时绕过中间件预检的情况。
	bodyLimit int64
}

// Query 返回 query 参数原值；缺省为空串。
func (c *Ctx) Query(name string) string { return c.req.Query(name) }

// QueryDefault 返回 query 参数，缺省用 def。
func (c *Ctx) QueryDefault(name, def string) string {
	if v := c.req.Query(name); v != "" {
		return v
	}
	return def
}

// RequiredQuery 必填参数；缺失返回 validation.missing_field。
func (c *Ctx) RequiredQuery(name string) (string, error) {
	v := c.req.Query(name)
	if v == "" {
		return "", apperr.New(apperr.ValidationMissingField,
			"缺少必填参数 "+name, apperr.FieldDetails{Fields: []apperr.FieldError{
				{Field: name, Message: "必填"},
			}})
	}
	return v, nil
}

// Int 解析整型 query 参数，带默认值与取值范围。
// 超范围 / 非数字都归 validation.invalid_field，details 里带字段名。
func (c *Ctx) Int(name string, def, min, max int) (int, error) {
	raw := c.req.Query(name)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, apperr.InvalidField(name, "必须是整数")
	}
	if n < min || n > max {
		return 0, apperr.InvalidField(name, "取值范围是 "+strconv.Itoa(min)+"–"+strconv.Itoa(max))
	}
	return n, nil
}

// Bool 解析布尔 query 参数。只接受 true/false/1/0；
// 其余写法报错而不是静默当 false（拼错参数名/值该被立刻发现）。
func (c *Ctx) Bool(name string, def bool) (bool, error) {
	raw := c.req.Query(name)
	if raw == "" {
		return def, nil
	}
	switch raw {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return false, apperr.InvalidField(name, "必须是 true 或 false")
	}
}

// PageSize 解析并校验 pageSize 参数：数字（1–max）或 "full"。
// full 的 300 条上限由 service 层在 count 之后拦（需要先知道总数才能报
// 「共 N 条，超过上限」），这里只做格式校验。
func (c *Ctx) PageSize() (PageSize, error) {
	raw := c.req.Query("pageSize")
	if raw == "" {
		return PageSize{N: c.cfg.List.DefaultPageSize}, nil
	}
	if raw == "full" {
		return PageSize{Full: true}, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return PageSize{}, apperr.InvalidField("pageSize", "必须是数字或 full")
	}
	if n < 1 || n > c.cfg.List.MaxPageSize {
		return PageSize{}, apperr.InvalidField("pageSize",
			"取值范围是 1–"+strconv.Itoa(c.cfg.List.MaxPageSize)+"，或 full")
	}
	return PageSize{N: n}, nil
}

// checkBodySize 在真正读 body 之后复核体积：中间件的 Content-Length 预检对
// Transfer-Encoding: chunked 无效（没有长度头），只靠框架的 6MB 大兜底会放过
// 1MB/2MB 档位的超额请求，所以读完再量一次。
func (c *Ctx) checkBodySize(n int) error {
	if c.bodyLimit > 0 && int64(n) > c.bodyLimit {
		return apperr.New(apperr.ValidationInvalidField, "请求体超过上限，请减小内容后重试",
			apperr.FieldDetails{Fields: []apperr.FieldError{{Field: "body", Message: "超过路由请求体上限"}}})
	}
	return nil
}

// Bind 把 JSON 请求体解析进 dst。解析失败（含非法 JSON、类型不符）
// 归 validation.invalid_field；P3 的 multipart 用 RawBody 单独处理。
func (c *Ctx) Bind(dst any) error {
	if err := c.checkBodySize(c.req.Request().Header.ContentLength()); err != nil {
		// Content-Length 声明超限：不读 body 直接拒（省内存）
		return err
	}
	if err := c.checkBodySize(len(c.req.Body())); err != nil {
		return err
	}
	if err := c.req.Bind().Body(dst); err != nil {
		return apperr.New(apperr.ValidationInvalidField,
			"请求体格式不对，请检查 JSON 结构与字段类型",
			apperr.CauseDetails{Cause: err.Error()})
	}
	return nil
}

// BindOptional 允许「空请求体」：空 body 等同 {}，不当作格式错误。
// 只给「body 整体可选」的接口用（如 POST /site/update-time：不传 = 取当前时间）；
// 其余接口继续用 Bind，缺 body 仍应是 validation.invalid_field。
func (c *Ctx) BindOptional(dst any) error {
	if len(c.req.Body()) == 0 {
		return nil
	}
	return c.Bind(dst)
}

// Context 返回带超时的请求上下文；取消会一路传播到数据库驱动。
func (c *Ctx) Context() context.Context { return c.ctx }

// IP 返回客户端 IP（限流用）。
func (c *Ctx) IP() string { return c.req.IP() }

// RawBody 读取原始请求体（图片上传 multipart、原始转发用）。
func (c *Ctx) RawBody() ([]byte, error) {
	if err := c.checkBodySize(len(c.req.Body())); err != nil {
		return nil, err
	}
	b := c.req.Body()
	if len(b) == 0 {
		return []byte{}, nil
	}
	return append([]byte(nil), b...), nil
}

// Header 读请求头（auth/verify 取 Authorization 拿 exp 用）。
func (c *Ctx) Header(name string) string { return c.req.Get(name) }

// FormFile 读取 multipart 表单里的文件字段，返回（内容, 文件名, 错误）。
// 图片上传用；apperr 化的解析失败信息给人看。
func (c *Ctx) FormFile(name string) ([]byte, string, error) {
	if err := c.checkBodySize(len(c.req.Body())); err != nil {
		return nil, "", err
	}
	header, err := c.req.FormFile(name)
	if err != nil {
		return nil, "", apperr.New(apperr.ValidationMissingField,
			"缺少上传文件字段 "+name,
			apperr.FieldDetails{Fields: []apperr.FieldError{{Field: name, Message: "必填"}}})
	}
	file, err := header.Open()
	if err != nil {
		return nil, "", apperr.Wrap(apperr.InternalError, "读取上传文件失败", err)
	}
	defer file.Close()
	src, err := io.ReadAll(file)
	if err != nil {
		return nil, "", apperr.Wrap(apperr.InternalError, "读取上传文件失败", err)
	}
	return src, header.Filename, nil
}

// FormValue 读 multipart 里的文本字段。
func (c *Ctx) FormValue(name string) string { return c.req.FormValue(name) }

// Method / Path 供日志使用。
func (c *Ctx) Method() string { return c.req.Method() }
func (c *Ctx) Path() string   { return c.req.Path() }
