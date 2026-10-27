// Package apperr 是对外错误的唯一出口。
//
// 规则（docs/api-draft.md §0.2/§0.3）：
//   - code 面向程序、稳定不变；message 面向人（中文，说清「发生了什么 + 怎么办」）；
//   - 原始错误串不许塞进 message，只能放 details.cause——把堆栈或驱动报错直接怼给
//     前端展示层会暴露实现，也会让 message 失去可读性。
package apperr

// Code 是稳定的错误码，全部对齐 docs/api-draft.md §0.3，一个不许多一个不许少。
type Code string

const (
	ValidationInvalidField Code = "validation.invalid_field"
	ValidationMissingField Code = "validation.missing_field"
	AuthMissingToken       Code = "auth.missing_token"
	AuthInvalidToken       Code = "auth.invalid_token"
	AuthLocked             Code = "auth.locked"
	NotFoundRoute          Code = "not_found.route"
	PostNotFound           Code = "post.not_found"
	PostSlugConflict       Code = "post.slug_conflict"
	DraftNotFound          Code = "draft.not_found"
	DraftSlugConflict      Code = "draft.slug_conflict"
	LinkNotFound           Code = "link.not_found"
	LinkGroupConflict      Code = "link.group_conflict"
	ApplicationNotFound    Code = "application.not_found"
	ApplicationDuplicate   Code = "application.duplicate"
	ApplicationNoChange    Code = "application.no_change"
	ApplicationFull        Code = "application.full"
	ImageUnsupportedType   Code = "image.unsupported_type"
	ImageTooLarge          Code = "image.too_large"
	ImageUnreachable       Code = "image.unreachable"
	SearchNotImplemented   Code = "search.not_implemented"
	StorageReadOnly        Code = "storage.read_only"
	RateLimitExceeded      Code = "rate_limit.exceeded"
	TimeoutExceeded        Code = "timeout.exceeded"
	UpstreamFailed         Code = "upstream.failed"
	InternalError          Code = "internal.error"
)

// httpStatus 逐条对齐 api-draft.md §0.3 的错误码表。
// 查不到的 Code 一律兜底 500（见 HTTPStatus）——宁可状态码保守，也不能 panic。
var httpStatus = map[Code]int{
	ValidationInvalidField: 400,
	ValidationMissingField: 400,
	AuthMissingToken:       401,
	AuthInvalidToken:       401,
	AuthLocked:             403,
	NotFoundRoute:          404,
	PostNotFound:           404,
	PostSlugConflict:       409,
	DraftNotFound:          404,
	DraftSlugConflict:      409,
	LinkNotFound:           404,
	LinkGroupConflict:      409,
	ApplicationNotFound:    404,
	ApplicationDuplicate:   409,
	ApplicationNoChange:    400,
	ApplicationFull:        429,
	ImageUnsupportedType:   415,
	ImageTooLarge:          413,
	ImageUnreachable:       502,
	SearchNotImplemented:   501,
	StorageReadOnly:        503,
	RateLimitExceeded:      429,
	TimeoutExceeded:        504,
	UpstreamFailed:         502,
	InternalError:          500,
}

// HTTPStatus 返回错误码对应的 HTTP 状态码；未知码兜底 500。
func (c Code) HTTPStatus() int {
	if s, ok := httpStatus[c]; ok {
		return s
	}
	return 500
}

// FieldError 是字段校验失败时 details.fields 里的一个条目。
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// FieldDetails 包装 details 的固定形态 {"fields":[...]}。
type FieldDetails struct {
	Fields []FieldError `json:"fields"`
}

// CauseDetails 包装内部错误的固定形态 {"cause":"..."}。
type CauseDetails struct {
	Cause string `json:"cause"`
}

// UpstreamDetails 是上游错误形态：真实原因 + 哪个上游 + 上游自己的状态码。
type UpstreamDetails struct {
	Cause    string `json:"cause"`
	Upstream string `json:"upstream"`
	Status   int    `json:"status,omitempty"`
}
