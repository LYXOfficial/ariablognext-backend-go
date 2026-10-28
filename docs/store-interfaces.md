# 存储层接口契约（照抄即可）

> 这份文件里的类型签名**必须逐字实现**。`internal/store` 定义接口，
> `internal/store/fake`（测试用）和 `internal/store/mongo`（生产用）各实现一遍。
> 两个实现能在同一套测试下通过，说明抽象层没有被 Mongo 语义污染。

## 0. 铁律

1. `internal/store` 顶层包**只允许** `context`、`time`、`encoding/json` 和 `internal/model`。
   **不许出现** `bson`、`primitive`、`mongo-driver` 的任何符号 `scripts/checks.sh` 会拦。
2. 所有方法第一个参数都是 `ctx context.Context`，并且必须真的传给驱动。
3. 返回切片时，**没有数据也要返回空切片 `[]T{}`**，不要返回 `nil`（否则 JSON 出 `null`）。
4. 时间统一用 unix 秒 `int64` 在接口里流转；只有 `Application.createdAt/decidedAt` 用 `time.Time`
   （因为它们要按 RFC3339 输出、还要参与 TTL 清理）。
5. ObjectID 在接口层一律是 `string`，转换只在 mongo 适配器内部发生。

---

## 1. 公共类型（`internal/store/filter.go`）

```go
package store

// Page 是列表结果。注意 Count 是「过滤后的总数」，不是本页条数。
type Page[T any] struct {
	Items   []T
	Count   int64
	HasMore bool
}

// PostFilter 同时服务 list 与 count：CountOnly=true 时实现方必须跳过查询主体。
type PostFilter struct {
	Page        int
	PageSize    int   // 具体条数；Full=true 时忽略
	Full        bool  // pageSize=full
	CountOnly   bool
	Category    string
	Tag         string
	Year        int   // 0 表示不限；与 Month 组合时按 loc 计算
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

type ListFilter struct {
	Page      int
	PageSize  int
	CountOnly bool
}

type LinkPatch struct {
	Name        string
	URL         string
	Avatar      string
	Description string
	Color       string
}

type LatencyUpdate struct {
	ID      string
	Latency float64
}

// ApplicationIdentity 是判重用的归一化字段组，归一化在 service 层做，仓储只负责比较。
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
```

---

## 2. 聚合入口（`internal/store/store.go`）

```go
package store

import "context"

type Store interface {
	Posts() PostRepository
	Drafts() DraftRepository
	Summaries() SummaryRepository
	Speaks() SpeakRepository
	Links() LinkRepository
	Applications() ApplicationRepository
	Users() UserRepository
	Sites() SiteRepository

	Ping(ctx context.Context) error
	Close(ctx context.Context) error
}

// RawExporter 是「可选能力」：只有能整库枚举的实现才提供。
// 全库导出走这里，SQL 适配器不实现也不影响其他接口。
type RawExporter interface {
	ExportAll(ctx context.Context) (map[string][]json.RawMessage, error)
}
```

---

## 3. 文章

```go
type PostRepository interface {
	// Page 一次返回列表与总数；CountOnly=true 时 Items 必须为 nil 且不查主体。
	Page(ctx context.Context, f PostFilter, loc *time.Location) (Page[model.Post], error)

	BySlug(ctx context.Context, slug string) (*model.Post, error) // 不存在返回 (nil, store.ErrNotFound)

	Slugs(ctx context.Context) ([]string, error)

	// Neighbours 返回更新的一篇与更旧的一篇，任一不存在返回 nil。
	Neighbours(ctx context.Context, slug string) (newer, older *model.PostBrief, err error)

	// Related 按共有标签随机取最多 limit 篇，排除自身；无标签时返回空切片。
	Related(ctx context.Context, slug string, limit int) ([]model.PostBrief, error)

	TotalWordCount(ctx context.Context) (int64, error)

	Create(ctx context.Context, p model.Post) error // slug 冲突返回 store.ErrDuplicate

	UpdateMeta(ctx context.Context, slug string, m model.PostMeta) error // 不存在返回 store.ErrNotFound

	UpdateContent(ctx context.Context, slug string, c model.PostContent) error

	Delete(ctx context.Context, slug string) (bool, error) // 幂等：不存在返回 (false, nil)
}

type DraftRepository interface {
	Page(ctx context.Context, f DraftFilter) (Page[model.Draft], error)
	BySlug(ctx context.Context, slug string) (*model.Draft, error)
	Slugs(ctx context.Context) ([]string, error)
	Count(ctx context.Context) (int64, error)
	Create(ctx context.Context, d model.Draft) error
	UpdateMeta(ctx context.Context, slug string, m model.PostMeta) error
	UpdateContent(ctx context.Context, slug string, c model.PostContent) error
	Delete(ctx context.Context, slug string) (bool, error)
}

type SummaryRepository interface {
	// Get 返回摘要与最后刷新时间；没有缓存返回 ("", time.Time{}, store.ErrNotFound)
	Get(ctx context.Context, slug string) (summary string, refreshedAt time.Time, err error)
	Upsert(ctx context.Context, slug, summary string, at time.Time) error
}
```

> `PostFilter.Year/Month` 的时区由调用方传入的 `loc` 决定，**仓储内部不得使用 `time.Local`**。

---

## 4. 分类 / 标签 / 归档

```go
type TaxonomyRepository interface {
	Categories(ctx context.Context) ([]model.CategoryCount, error)
	Tags(ctx context.Context) ([]model.TagCount, error)
	// ArchiveBuckets 按 loc 划分年月并倒序；实现方可以自己在 Go 里分组（推荐），
	// 不要在 MongoDB 里用 $toDate + $year/$month（那是 UTC，会和月界不一致）
	ArchiveBuckets(ctx context.Context, loc *time.Location) ([]model.ArchiveBucket, error)
}
```

`PostRepository` 的实现结构体可以顺带实现 `TaxonomyRepository`（两者都基于 Posts 集合）。

---

## 5. 说说

```go
type SpeakRepository interface {
	Page(ctx context.Context, f ListFilter) (Page[model.Speak], error) // 按 time 倒序
	Count(ctx context.Context) (int64, error)
	Create(ctx context.Context, s model.Speak) error
	Update(ctx context.Context, time int64, content, plainContent string) (bool, error)
	Delete(ctx context.Context, time int64) (bool, error)
}
```

---

## 6. 友链

```go
type LinkRef struct {
	Group string
	Link  model.Link
}

type LinkRepository interface {
	// Groups 必须按 order 升序返回，缺失 order 视为 0；排序由数据库完成
	Groups(ctx context.Context) ([]model.LinkGroup, error)

	GroupByName(ctx context.Context, name string) (*model.LinkGroup, error) // 不存在返回 ErrNotFound

	// LinkCount 是「所有分组里链接条目数之和」，不是分组数
	LinkCount(ctx context.Context) (int64, error)

	// FindByURL 兼容「结尾有没有斜杠」两种写法
	FindByURL(ctx context.Context, url string) (*LinkRef, error)

	AddLink(ctx context.Context, group string, l model.Link) error

	// UpdateLink 返回 matched；分组或链接不存在返回 (false, nil)
	UpdateLink(ctx context.Context, group, id string, patch LinkPatch) (bool, error)

	// MoveLink 必须是原子的：实现方自己决定用事务还是「push 后 pull + 失败回滚」。
	// 源分组或链接不存在返回 ErrNotFound。
	MoveLink(ctx context.Context, fromGroup, toGroup, id string) error

	DeleteLink(ctx context.Context, group, id string) (bool, error)

	AddGroup(ctx context.Context, g model.LinkGroup) error // 重名返回 ErrDuplicate
	UpdateGroup(ctx context.Context, oldName string, g model.LinkGroup) (bool, error)
	DeleteGroup(ctx context.Context, name string) (bool, error)

	SetLatency(ctx context.Context, updates []LatencyUpdate) (int64, error) // 返回实际更新条数
}
```

---

## 7. 友链申请

```go
type ApplicationRepository interface {
	Create(ctx context.Context, a model.Application) (id string, err error)
	ByID(ctx context.Context, id string) (*model.Application, error)
	CountPending(ctx context.Context) (int64, error)
	ListPending(ctx context.Context, limit int) ([]model.Application, error) // 按 createdAt 升序

	// FindIdentical 在待审队列里找内容完全相同的申请（归一化后的字段全等）
	// 队列上限 20 条，实现方全量取出在内存比较即可
	FindIdentical(ctx context.Context, ident ApplicationIdentity) (*model.Application, error)

	// FindByContact 按邮箱或站点名（大小写不敏感精确匹配）查最近 limit 条
	FindByContact(ctx context.Context, email, name string, limit int) ([]model.Application, error)

	SetScreening(ctx context.Context, id string, s model.AIScreening) (bool, error) // 只更新 pending 状态
	Decide(ctx context.Context, id, status, reason string, at time.Time) (bool, error)

	// DeleteDecidedByEmail 用于「同一邮箱重新提交时清掉旧的已出结果记录」
	DeleteDecidedByEmail(ctx context.Context, email string) (int64, error)

	// PurgeDecidedBefore 清理超过保留期的已出结果记录
	PurgeDecidedBefore(ctx context.Context, cutoff time.Time) (int64, error)
}
```

---

## 8. 用户与站点信息

```go
type UserRepository interface {
	// ByCredentials 用 user + sha256(password) 的十六进制去匹配
	ByCredentials(ctx context.Context, user, passwordHex string) (*model.User, error)
}

type SiteRepository interface {
	// GetInt 读 webSiteInfo 里某个 key 的整数值；不存在返回 ErrNotFound
	GetInt(ctx context.Context, key string) (int64, error)
	// SetNow 把某 key 的值写成当前时间（upsert）
	SetNow(ctx context.Context, key string) (int64, error)
}
```

---

## 9. 错误哨兵（`internal/store/errors.go`）

```go
var (
	ErrNotFound  = errors.New("store: not found")
	ErrDuplicate = errors.New("store: duplicate key")
	// ErrReadOnly 由适配器在 database.readOnly=true 时返回。
	// 这是「连生产库只读验证」的强制闸门：不是约定，是每个写方法开头的一行判断。
	ErrReadOnly  = errors.New("store: read-only mode")
)
```

**只读模式的实现方式**（每条写方法的第一行，一个都不能漏）：

```go
func (r *postRepo) Create(ctx context.Context, p model.Post) error {
    if r.ro { return store.ErrReadOnly }
    ...
}
```

`r.ro` 来自 `cfg.Database.ReadOnly`。service 层不需要知道这件事——
它只会拿到 `ErrReadOnly`，由 `apperr` 统一映射成 `503 storage.read_only`。

**mongo 适配器必须做转换**：

```go
if mongo.IsDuplicateKeyError(err) {
    return store.ErrDuplicate
}
if errors.Is(err, mongo.ErrNoDocuments) {
    return store.ErrNotFound
}
```

service 层只认这三个哨兵，**不许**出现 `mongo.ErrNoDocuments`。

---

## 10. 实现清单（两个实现要覆盖同样的东西）

| 包 | 内容 |
|---|---|
| `internal/store/fake` | 纯内存 map 实现，供 handler / service 单测用；必须实现全部接口 + `RawExporter` |
| `internal/store/mongo` | 真实实现；`open.go` 里按配置建客户端、建索引（幂等）、健康检查 |

`internal/store/open.go` 是唯一允许 import mongo 驱动的非适配器文件：

```go
func Open(ctx context.Context, cfg *config.Config) (Store, error) {
    switch cfg.Database.Type {
    case "mongo":
        return mongostore.Open(ctx, cfg)
    default:
        return nil, fmt.Errorf("不支持的 DB_TYPE %q（目前只支持 mongo）", cfg.Database.Type)
    }
}
```

加新数据库时只需要：新建 `internal/store/pgstore`，实现上面的接口，在这里加一个 `case`。
**任何情况下都不许为了适配新库去改接口**——改接口说明抽象没做对。


## EmbeddingRepository（2026-10-28 增补）

语义搜索的向量存储，单开 `PostEmbeddings` 集合（不进 Posts 主文档——列表投影零改动）。

```go
type EmbeddingRepository interface {
	Upsert(ctx context.Context, doc EmbeddingDoc) error          // slug 为 _id，整体替换
	DeleteBySlug(ctx context.Context, slug string) (bool, error) // 幂等
	ListAll(ctx context.Context) ([]EmbeddingDoc, error)         // 全量，service 层有 5min TTL 缓存
}
```

> 注：design-notes §2.6 原预留 `SemanticSearcher`（store 内做检索），
> 实现时改为 service 层内存暴力余弦 + RRF——83 篇规模下更简单且精度相同，
> 接口面也更小。换 Atlas Vector Search 时再把检索下沉回 store。
