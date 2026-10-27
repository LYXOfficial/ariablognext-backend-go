package store

import (
	"context"
	"encoding/json"
)

// Page 是列表结果。Count 是「过滤后的总数」，不是本页条数。
type Page[T any] struct {
	Items   []T
	Count   int64
	HasMore bool
}

// PostFilter 同时服务 list 与 count：CountOnly=true 时实现方必须完全跳过查询主体。
type PostFilter struct {
	Page        int
	PageSize    int  // 具体条数；Full=true 时忽略
	Full        bool // pageSize=full
	CountOnly   bool
	Category    string
	Tag         string
	Year        int // 0 表示不限；与 Month 组合时按 loc 计算月界
	Month       int
	Keyword     string
	TitleOnly   bool
	WithContent bool
	OrderAsc    bool
}

// DraftFilter 与 PostFilter 同构，只是没有 category/tag/year/month。
type DraftFilter struct {
	Page        int
	PageSize    int
	Full        bool
	CountOnly   bool
	Keyword     string
	WithContent bool
	OrderAsc    bool
}

// ListFilter 是 speaks 这类简单列表用的过滤器。
type ListFilter struct {
	Page      int
	PageSize  int
	CountOnly bool
}

// LinkPatch 是友链条目的可改字段。
type LinkPatch struct {
	Name        string
	URL         string
	Avatar      string
	Description string
	Color       string
}

// LatencyUpdate 是链路检查上报的单条延迟。
type LatencyUpdate struct {
	ID      string
	Latency float64
}

// ApplicationIdentity 是友链申请判重用的归一化字段组。
// 归一化（trim、去尾斜杠、颜色缺省、邮箱小写）在 service 层做，仓储只负责比较。
type ApplicationIdentity struct {
	Type     int
	Name     string
	Link     string
	Avatar   string
	Descr    string
	Color    string
	Email    string
	TargetID string // 修改申请的目标友链 id，新增申请为空
}

// RawExporter 是「可选能力」：只有能整库枚举的实现才提供。
// 全库导出（备份 Action）走这里；SQL 适配器不实现也不影响其他接口。
type RawExporter interface {
	ExportAll(ctx context.Context) (map[string][]json.RawMessage, error)
}
