package store

import (
	"context"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
)

// Store 是全部仓储的聚合入口。签名与 docs/store-interfaces.md 逐字一致，
// 觉得签名有问题停下来问，不要自己改接口。
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

// LinkRef 定位到「某分组里的某条友链」，供判重按 URL 查找。
type LinkRef struct {
	Group string
	Link  model.Link
}

// ---------------- 文章 / 草稿 ----------------

type PostRepository interface {
	// Page 一次返回列表与总数；CountOnly=true 时 Items 必须为 nil 且不查主体。
	Page(ctx context.Context, f PostFilter, loc *time.Location) (Page[model.Post], error)

	// BySlug 不存在返回 (nil, ErrNotFound)。
	BySlug(ctx context.Context, slug string) (*model.Post, error)

	Slugs(ctx context.Context) ([]string, error)

	// Neighbours 返回更新的一篇与更旧的一篇，任一不存在返回 nil。
	// 新接口用 newer/older 命名，修正旧接口 previous/next 名实相反的问题。
	Neighbours(ctx context.Context, slug string) (newer, older *model.PostBrief, err error)

	// Related 按共有标签随机取最多 limit 篇，排除自身；无标签时返回空切片。
	Related(ctx context.Context, slug string, limit int) ([]model.PostBrief, error)

	TotalWordCount(ctx context.Context) (int64, error)

	// Create 在 slug 冲突时返回 ErrDuplicate——靠 unique index + 重复键判断，
	// 不许先 Count 再 Insert（那是竞态，旧后端就是这么写坏的）。
	Create(ctx context.Context, p model.Post) error

	// UpdateMeta 不存在返回 ErrNotFound。
	UpdateMeta(ctx context.Context, slug string, m model.PostMeta) error

	UpdateContent(ctx context.Context, slug string, c model.PostContent) error

	// Delete 幂等：不存在返回 (false, nil)。
	Delete(ctx context.Context, slug string) (bool, error)
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
	// Get 没有缓存返回 ("", time.Time{}, ErrNotFound)。
	Get(ctx context.Context, slug string) (summary string, refreshedAt time.Time, err error)
	Upsert(ctx context.Context, slug, summary string, at time.Time) error
}

// TaxonomyRepository：分类/标签/归档。与 Posts 同集合，
// mongo 适配器里由 postRepo 顺带实现。
type TaxonomyRepository interface {
	Categories(ctx context.Context) ([]model.CategoryCount, error)
	Tags(ctx context.Context) ([]model.TagCount, error)

	// ArchiveBuckets 按 loc 划分年月并倒序。实现方在 Go 里分组（推荐），
	// 不要在 MongoDB 里用 $toDate + $year/$month——那是 UTC，和本地月界不一致。
	ArchiveBuckets(ctx context.Context, loc *time.Location) ([]model.ArchiveBucket, error)
}

// ---------------- 说说 ----------------

type SpeakRepository interface {
	// Page 按 time 倒序。
	Page(ctx context.Context, f ListFilter) (Page[model.Speak], error)
	Count(ctx context.Context) (int64, error)
	Create(ctx context.Context, s model.Speak) error
	Update(ctx context.Context, time int64, content, plainContent string) (bool, error)
	Delete(ctx context.Context, time int64) (bool, error)
}

// ---------------- 友链 ----------------

type LinkRepository interface {
	// Groups 必须按 order 升序返回，缺失 order 视为 0；排序由数据库完成。
	Groups(ctx context.Context) ([]model.LinkGroup, error)

	// GroupByName 不存在返回 ErrNotFound。
	GroupByName(ctx context.Context, name string) (*model.LinkGroup, error)

	// LinkCount 是「所有分组里链接条目数之和」，不是分组数。
	LinkCount(ctx context.Context) (int64, error)

	// FindByURL 兼容「结尾有没有斜杠」两种写法。
	FindByURL(ctx context.Context, url string) (*LinkRef, error)

	AddLink(ctx context.Context, group string, l model.Link) error

	// UpdateLink 返回 matched；分组或链接不存在返回 (false, nil)。
	UpdateLink(ctx context.Context, group, id string, patch LinkPatch) (bool, error)

	// MoveLink 必须是原子的：实现方自己决定用事务还是「push 后 pull + 失败回滚」。
	// 源分组或链接不存在返回 ErrNotFound。
	MoveLink(ctx context.Context, fromGroup, toGroup, id string) error

	DeleteLink(ctx context.Context, group, id string) (bool, error)

	// AddGroup 重名返回 ErrDuplicate。
	AddGroup(ctx context.Context, g model.LinkGroup) error

	UpdateGroup(ctx context.Context, oldName string, g model.LinkGroup) (bool, error)

	DeleteGroup(ctx context.Context, name string) (bool, error)

	// SetLatency 返回实际更新条数。
	SetLatency(ctx context.Context, updates []LatencyUpdate) (int64, error)
}

// ---------------- 友链申请 ----------------

type ApplicationRepository interface {
	// Create 返回新生成的 ObjectID 字符串。
	Create(ctx context.Context, a model.LinkApplication) (id string, err error)

	ByID(ctx context.Context, id string) (*model.LinkApplication, error)

	CountPending(ctx context.Context) (int64, error)

	// ListPending 按 createdAt 升序（最早的先审）。
	ListPending(ctx context.Context, limit int) ([]model.LinkApplication, error)

	// FindIdentical 在待审队列里找内容完全相同的申请（归一化后的字段全等）。
	// 待审队列上限 20 条，实现方全量取出在内存比较即可。
	FindIdentical(ctx context.Context, ident ApplicationIdentity) (*model.LinkApplication, error)

	// FindByContact 按邮箱或站点名（大小写不敏感精确匹配）查最近 limit 条。
	FindByContact(ctx context.Context, email, name string, limit int) ([]model.LinkApplication, error)

	// SetScreening 只更新 pending 状态的申请。
	SetScreening(ctx context.Context, id string, s model.AIScreening) (bool, error)

	Decide(ctx context.Context, id, status, reason string, at time.Time) (bool, error)

	// DeleteDecidedByEmail 用于「同一邮箱重新提交时清掉旧的已出结果记录」。
	DeleteDecidedByEmail(ctx context.Context, email string) (int64, error)

	// PurgeDecidedBefore 清理超过保留期的已出结果记录（pending 不删）。
	PurgeDecidedBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// ---------------- 用户与站点信息 ----------------

type UserRepository interface {
	// ByCredentials 用 user + sha256(password) 的十六进制去匹配。
	ByCredentials(ctx context.Context, user, passwordHex string) (*model.User, error)
}

type SiteRepository interface {
	// GetInt 读 webSiteInfo 里某个 key 的整数值；不存在返回 ErrNotFound。
	GetInt(ctx context.Context, key string) (int64, error)

	// SetNow 把某 key 的值写成当前时间（upsert），返回写入的时间戳。
	SetNow(ctx context.Context, key string) (int64, error)

	// SetInt 把某 key 写成指定整数（upsert）。dashboard 改历史时间戳用；
	// 语义修正见 service.SiteService 注释（写 latestUpdateTime 的只有胶水）。
	SetInt(ctx context.Context, key string, value int64) (int64, error)
}

// SitemapEntry 是 sitemap 需要的最小字段（slug + 最后更新时间）。
type SitemapEntry struct {
	Slug            string
	LastUpdatedTime int64
}

// SitemapSource 是可选能力：一次给出全站文章的 slug 与 lastUpdatedTime，
// 供 /sitemap 标注每篇的 lastmod（旧后端就是每篇取 lastUpdatedTime，别退化成「今天」）。
type SitemapSource interface {
	SitemapEntries(ctx context.Context) ([]SitemapEntry, error)
}

// SearchTextReader 是可选能力：读出每篇文档现有的 searchText，
// 供 cmd/backfill-searchtext 的 dry-run 精确统计「需要回填多少条」。
type SearchTextReader interface {
	// SearchTexts 返回 slug → 当前库里的 searchText（缺字段的值为空串）。
	SearchTexts(ctx context.Context) (map[string]string, error)
}

// UserWriter 是「写用户凭据」的可选能力：只有种子工具需要。
// 生产用户由人工管理，业务接口永远不暴露用户写入。
type UserWriter interface {
	UpsertUser(ctx context.Context, user, passwordHex string) error
}
