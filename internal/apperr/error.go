package apperr

import (
	"context"
	"errors"
	"fmt"
)

// Error 携带对外错误码与中文 message。实现 error 接口，
// 可以一路 %w 包装；渲染层（internal/app/render.go）负责把它变成信封。
type Error struct {
	code    Code
	message string
	details any
	wrapped error
}

// New 构造一个对外错误。details 传 nil 或符合 api-draft §0.2 固定形态的对象。
func New(code Code, msg string, details any) *Error {
	return &Error{code: code, message: msg, details: details}
}

// Wrap 在保留原始错误的前提下构造对外错误：
// code 与 message 照旧，原始错误串进 details.cause（排查效率优先，见 design-notes §1.2）。
func Wrap(code Code, msg string, err error) *Error {
	e := &Error{code: code, message: msg}
	if err != nil {
		e.wrapped = err
		e.details = CauseDetails{Cause: err.Error()}
	}
	return e
}

func (e *Error) Error() string {
	if e.wrapped != nil {
		return fmt.Sprintf("%s: %s（%v）", e.code, e.message, e.wrapped)
	}
	return fmt.Sprintf("%s: %s", e.code, e.message)
}

func (e *Error) Unwrap() error { return e.wrapped }

func (e *Error) Code() Code      { return e.code }
func (e *Error) Message() string { return e.message }
func (e *Error) Details() any    { return e.details }
func (e *Error) HTTPStatus() int { return e.code.HTTPStatus() }

// From 把任意 error 归一成 *apperr.Error：
//   - 已经是 *Error → 原样返回；
//   - context.DeadlineExceeded → timeout.exceeded（请求超时统一走 504）；
//   - 其它 → internal.error，原始错误串进 cause。
//
// 渲染层只信这个函数，不再自己猜错误类型。
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return New(TimeoutExceeded, "服务处理超时，请稍后重试", CauseDetails{Cause: err.Error()})
	}
	return Wrap(InternalError, "服务内部错误，请稍后重试", err)
}

// InvalidField 是字段校验失败的标准构造：details 固定为
// {"fields":[{"field":..., "message":...}]}，一次一个字段；
// 多字段校验请在 service/validate 层攒齐后用 New 自行构造。
func InvalidField(field, msg string) *Error {
	return New(ValidationInvalidField, msg,
		FieldDetails{Fields: []FieldError{{Field: field, Message: msg}}})
}
