// Package app 组装 HTTP 层：路由表、统一信封渲染、中间件。
//
// 信封是编译期约束（design-notes §1）：
//   - handler 签名固定 func(*Ctx) (any, error)，Ctx 只有读能力；
//   - 全服务唯一的响应出口是 Render；谁绕过它写响应，checks.sh 会拦。
package app

// PageSize 是解析后的 pageSize 参数。
// Value() 的返回值可能是 int（数字分页）或字符串 "full"（全量），
// 信封里两种形态都要原样出现，前端靠类型区分。
type PageSize struct {
	N    int
	Full bool
}

func (p PageSize) Value() any {
	if p.Full {
		return "full"
	}
	return p.N
}

// Meta 是信封里的 meta 字段。两种形状（列表 / 纯计数）字段集合不同，
// 用带私有方法的接口收口，外部造不出第三种形状。
type Meta interface{ metaShape() }

// ListMeta：{page, pageSize, count, hasMore}。hasMore=false 也要出现在 JSON 里
// （前端靠它判断「没有下一页」），所以不能用 omitempty。
type ListMeta struct {
	Page     int   `json:"page"`
	PageSize any   `json:"pageSize"`
	Count    int64 `json:"count"`
	HasMore  bool  `json:"hasMore"`
}

func (ListMeta) metaShape() {}

// CountMeta：countOnly=true 时只有 {count}，绝不带 page/pageSize/hasMore。
type CountMeta struct {
	Count int64 `json:"count"`
}

func (CountMeta) metaShape() {}

// NewListMeta 组装列表分页元信息。
func NewListMeta(page int, ps PageSize, count int64, hasMore bool) Meta {
	return ListMeta{Page: page, PageSize: ps.Value(), Count: count, HasMore: hasMore}
}

// NewCountMeta 组装纯计数元信息。
func NewCountMeta(count int64) Meta {
	return CountMeta{Count: count}
}

// WithMeta 是 handler 返回「列表 + 元信息」的载体。
// 裸返回其它类型时信封不带 meta。
type WithMeta struct {
	Data any
	Meta Meta
}
