package apperr

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// 错误码 → HTTP 状态逐条对齐 api-draft.md §0.3，一个码一行，改动契约时这里必须同步。
func TestCodeHTTPStatusTable(t *testing.T) {
	cases := []struct {
		code Code
		want int
	}{
		{ValidationInvalidField, 400},
		{ValidationMissingField, 400},
		{AuthMissingToken, 401},
		{AuthInvalidToken, 401},
		{AuthLocked, 403},
		{NotFoundRoute, 404},
		{PostNotFound, 404},
		{PostSlugConflict, 409},
		{DraftNotFound, 404},
		{DraftSlugConflict, 409},
		{LinkNotFound, 404},
		{LinkGroupConflict, 409},
		{ApplicationNotFound, 404},
		{ApplicationDuplicate, 409},
		{ApplicationNoChange, 400},
		{ApplicationFull, 429},
		{ImageUnsupportedType, 415},
		{ImageTooLarge, 413},
		{ImageUnreachable, 502},
		{SearchNotImplemented, 501},
		{StorageReadOnly, 503},
		{RateLimitExceeded, 429},
		{TimeoutExceeded, 504},
		{UpstreamFailed, 502},
		{InternalError, 500},
	}
	for _, c := range cases {
		if got := c.code.HTTPStatus(); got != c.want {
			t.Errorf("%s → %d，应为 %d", c.code, got, c.want)
		}
	}
}

func TestWrapKeepsCodeAndUnwrap(t *testing.T) {
	inner := fmt.Errorf("connection refused")
	e := Wrap(PostNotFound, "文章不存在", inner)
	if e.Code() != PostNotFound {
		t.Errorf("code = %s", e.Code())
	}
	if !errors.Is(e, inner) {
		t.Error("Unwrap 链断了")
	}
	d, ok := e.Details().(CauseDetails)
	if !ok || d.Cause != inner.Error() {
		t.Errorf("details.cause 应保留原始错误串：%#v", e.Details())
	}
	// 原始错误串不许进 message：message 是给人看的，堆栈是给排查用的
	if e.Message() != "文章不存在" {
		t.Errorf("message = %q", e.Message())
	}
}

func TestFromUnknownErrorBecomesInternal(t *testing.T) {
	inner := fmt.Errorf("boom")
	e := From(inner)
	if e.Code() != InternalError {
		t.Errorf("code = %s", e.Code())
	}
	if e.HTTPStatus() != 500 {
		t.Errorf("status = %d", e.HTTPStatus())
	}
	if d, ok := e.Details().(CauseDetails); !ok || d.Cause != "boom" {
		t.Errorf("details = %#v", e.Details())
	}
}

func TestFromApperrPassThrough(t *testing.T) {
	orig := New(PostSlugConflict, "这个 slug 已经有文章了", nil)
	if From(orig) != orig {
		t.Error("*apperr.Error 应原样透传")
	}
}

func TestFromDeadlineExceeded(t *testing.T) {
	e := From(context.DeadlineExceeded)
	if e.Code() != TimeoutExceeded || e.HTTPStatus() != 504 {
		t.Errorf("超时应映射 timeout.exceeded/504，得到 %s/%d", e.Code(), e.HTTPStatus())
	}
}

func TestUnknownCodeFallback500(t *testing.T) {
	// 未知 Code 必须兜底 500，不能 panic
	Code("not.a.code").HTTPStatus()
	if got := Code("not.a.code").HTTPStatus(); got != 500 {
		t.Errorf("未知码应兜底 500，得到 %d", got)
	}
}

func TestNilFrom(t *testing.T) {
	if From(nil) != nil {
		t.Error("From(nil) 应返回 nil")
	}
}
