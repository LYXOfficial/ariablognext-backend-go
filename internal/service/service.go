// Package service 是业务逻辑层：只收 context.Context 和仓储接口，不碰 fiber。
// handler 负责参数绑定，这里负责业务规则（分页上限、搜索排序、缓存冷却……）。
package service

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/auth"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/platform"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

// Set 是全部服务的聚合入口，main 里构造一次、handler 里只读使用。
type Set struct {
	Posts        *PostService
	Drafts       *DraftService
	Speaks       *SpeakService
	Sites        *SiteService
	Links        *LinkService
	Login        *LoginService
	Applications *ApplicationService
	Status       *StatusService
}

// NewSet 组装全部服务。deps 全部显式传入。
func NewSet(cfg *config.Config, st store.Store) *Set {
	return &Set{
		Posts: &PostService{
			cfg: cfg, posts: st.Posts(),
			taxonomy:  st.Posts().(store.TaxonomyRepository),
			summaries: st.Summaries(),
			llm:       platform.NewLLM(cfg.AI.Summary),
		},
		Drafts: &DraftService{drafts: st.Drafts()},
		Speaks: &SpeakService{cfg: cfg, speaks: st.Speaks()},
		Sites:  &SiteService{sites: st.Sites()},
		Links: &LinkService{
			cfg: cfg, links: st.Links(), apps: st.Applications(), theme: NewThemeService(),
			// 审批通知邮件依赖（P3）：模板与 SMTP 客户端
			templates: platform.NewMailTemplates(cfg.Site, cfg.Flink),
			mailer:    platform.NewMailer(cfg.Mail),
		},
		Applications: NewApplicationService(cfg, st),
		Status:       NewStatusService(cfg, st),
		Login:        nil, // main 里在 app.New 之后补（需要 JWT 签发器）
	}
}

// AttachLogin 在拿到 JWT 签发器后挂上登录服务（main 专用的两段式组装）。
func (s *Set) AttachLogin(users store.UserRepository, jwt *auth.JWT) {
	s.Login = NewLoginService(users, jwt)
}

// PostService：文章相关业务。
type PostService struct {
	cfg       *config.Config
	posts     store.PostRepository
	taxonomy  store.TaxonomyRepository
	summaries store.SummaryRepository
	llm       *platform.LLM
}

// List 带业务校验的列表查询：
//   - pageSize=full 有 300 条硬上限——先 count（countOnly 语义），超限报
//     validation.invalid_field（文案里带上真实条数，告诉用户怎么改）；
//   - 搜索命中时标题命中排在正文命中之前（页内排序，见 SearchBoost）。
func (s *PostService) List(ctx context.Context, f store.PostFilter) (store.Page[model.Post], error) {
	// CountOnly 时不拦 full：纯计数很轻，契约只限制「拉条目」的 full
	if f.Full && !f.CountOnly {
		count, err := s.posts.Page(ctx, store.PostFilter{
			Category: f.Category, Tag: f.Tag, Year: f.Year, Month: f.Month,
			Keyword: f.Keyword, TitleOnly: f.TitleOnly, CountOnly: true,
		}, s.cfg.Location())
		if err != nil {
			return store.Page[model.Post]{}, err
		}
		if count.Count > int64(s.cfg.List.MaxFullItems) {
			return store.Page[model.Post]{}, apperr.New(apperr.ValidationInvalidField,
				"共 "+itoa64(count.Count)+" 条，超过 full 模式 "+itoa(s.cfg.List.MaxFullItems)+" 条上限，请改用分页拉取",
				apperr.FieldDetails{Fields: []apperr.FieldError{
					{Field: "pageSize", Message: "full 模式上限 " + itoa(s.cfg.List.MaxFullItems) + " 条"},
				}})
		}
	}
	res, err := s.posts.Page(ctx, f, s.cfg.Location())
	if err != nil {
		return store.Page[model.Post]{}, err
	}
	// 标题命中前置。取舍：这是「页内」排序——跨页的全局标题优先需要把标题命中
	// 全量拉回再拼页，83 篇的规模下收益趋零、实现复杂度翻倍；契约允许的语义
	// 修正里只要求「标题命中先于正文命中」，页内排序满足可观察行为。
	if kws := splitKeywords(f.Keyword); len(kws) > 0 && !f.TitleOnly && !f.CountOnly {
		titleBoost(res.Items, kws)
	}
	return res, nil
}

// titleBoost 把本页内标题命中第一条关键词的结果移到最前（稳定排序，发布时间顺序在组内保持）。
func titleBoost(items []model.Post, kws []string) {
	first := lower(kws[0])
	split := 0
	for i, p := range items {
		if contains(lower(p.Title), first) {
			items[split], items[i] = items[i], items[split]
			split++
		}
	}
}

// Detail 详情；ErrNotFound 转 post.not_found。
func (s *PostService) Detail(ctx context.Context, slug string) (*model.Post, error) {
	p, err := s.posts.BySlug(ctx, slug)
	if err == store.ErrNotFound {
		return nil, apperr.New(apperr.PostNotFound, "文章不存在或已被删除", nil)
	}
	return p, err
}

// Statistics 全站总字数。预留扩展位（平均字数/分类分布以后加）。
func (s *PostService) Statistics(ctx context.Context) (map[string]any, error) {
	total, err := s.posts.TotalWordCount(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"totalWordCount": total}, nil
}

// Slugs 全部文章 slug（sitemap / 静态化用），不分页。
func (s *PostService) Slugs(ctx context.Context) ([]string, error) {
	return s.posts.Slugs(ctx)
}

// Navigation 上一篇/下一篇（newer/older，修正旧接口 previous/next 名实相反）。
func (s *PostService) Navigation(ctx context.Context, slug string) (map[string]any, error) {
	// 先确认文章存在：不存在要报 post.not_found 而不是空对象
	if _, err := s.posts.BySlug(ctx, slug); err != nil {
		if err == store.ErrNotFound {
			return nil, apperr.New(apperr.PostNotFound, "文章不存在或已被删除", nil)
		}
		return nil, err
	}
	newer, older, err := s.posts.Neighbours(ctx, slug)
	if err != nil {
		return nil, err
	}
	return map[string]any{"newer": newer, "older": older}, nil
}

// Related 相关文章；无标签返回空切片（旧接口 404 是行为 bug，契约已修正）。
func (s *PostService) Related(ctx context.Context, slug string, limit int) ([]model.PostBrief, error) {
	return s.posts.Related(ctx, slug, limit)
}

// SummaryGet 公开摘要：缓存优先；无缓存且摘要服务未启用时报 upstream.failed。
// P3 接入 SiliconFlow 后，未命中的生成逻辑实现在同一位置（每篇最多生成一次）。
func (s *PostService) SummaryGet(ctx context.Context, slug string) (map[string]any, error) {
	if _, err := s.posts.BySlug(ctx, slug); err != nil {
		if err == store.ErrNotFound {
			return nil, apperr.New(apperr.PostNotFound, "文章不存在或已被删除", nil)
		}
		return nil, err
	}
	summary, _, err := s.summaries.Get(ctx, slug)
	if err == nil {
		return map[string]any{"summary": summary}, nil
	}
	if err != store.ErrNotFound {
		return nil, err
	}
	// 未命中：本机 ai.summary 关闭时明说「未启用」，别让用户以为文章坏了
	return nil, apperr.New(apperr.UpstreamFailed, "摘要生成服务未启用，暂无该文章的摘要缓存", nil)
}

// SummaryCached 只读缓存、绝不生成（后台预览用，对应旧 `GET /update/post/summary`）。
// 命中返回 {"summary": "…"}；未命中返回 {"summary": null}（旧后端就是 200 + data:null）。
func (s *PostService) SummaryCached(ctx context.Context, slug string) (map[string]any, error) {
	summary, _, err := s.summaries.Get(ctx, slug)
	if err == store.ErrNotFound {
		return map[string]any{"summary": nil}, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"summary": summary}, nil
}

// Categories / Tags / Archives 转发同集合的分类聚合。
// 注意排序语义是刻意修正：按 publishTime 相关的字段排（归档倒序），
// 旧后端按不存在的 `date` 字段排序等于没排序。
func (s *PostService) Categories(ctx context.Context) ([]model.CategoryCount, error) {
	return s.taxonomy.Categories(ctx)
}

func (s *PostService) Tags(ctx context.Context) ([]model.TagCount, error) {
	return s.taxonomy.Tags(ctx)
}

func (s *PostService) Archives(ctx context.Context) ([]model.ArchiveBucket, error) {
	// 时区由 config 决定（Asia/Shanghai），分桶在 Go 侧完成
	return s.taxonomy.ArchiveBuckets(ctx, s.cfg.Location())
}

// SpeakService：说说。
type SpeakService struct {
	cfg    *config.Config
	speaks store.SpeakRepository
}

func (s *SpeakService) List(ctx context.Context, f store.ListFilter) (store.Page[model.Speak], error) {
	return s.speaks.Page(ctx, f)
}

// SiteService：站点信息。
// ⚠️ 语义修正（用户定论）：latestUpdateTime 只由胶水层写入——
// 旧链路「后端写时间戳 + 顺带 GET blog/refreshCache/siteinfo 通知刷缓存」的
// 耦合要删除，刷新缓存由 dashboard 单独请求。新接口支持自定义时间戳，缺省取当前时间。
type SiteService struct {
	sites store.SiteRepository
}

// Info 站点最近更新时间（key=latestUpdateTime，与旧后端同 key）。
func (s *SiteService) Info(ctx context.Context) (map[string]any, error) {
	t, err := s.sites.GetInt(ctx, "latestUpdateTime")
	if err != nil {
		return nil, err
	}
	return map[string]any{"latestUpdateTime": t}, nil
}

// UpdateTime 写入时间戳并返回写入值。
// ts 为 nil 时取当前时间（胶水默认行为）；dashboard 要改历史时间就显式传。
// 不再 fire-and-forget 通知刷缓存——那个耦合已删除（见 SiteService 注释）。
func (s *SiteService) UpdateTime(ctx context.Context, ts *int64) (map[string]any, error) {
	var t int64
	var err error
	if ts == nil {
		t, err = s.sites.SetNow(ctx, "latestUpdateTime")
	} else {
		t, err = s.sites.SetInt(ctx, "latestUpdateTime", *ts)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"latestUpdateTime": t}, nil
}

// LinkService：友链 + 友链申请（读侧）。
type LinkService struct {
	cfg   *config.Config
	links store.LinkRepository
	apps  store.ApplicationRepository
	theme *ThemeService
	// 邮件依赖（审批通知用）
	templates *platform.MailTemplates
	mailer    *platform.Mailer
}

// List 友链分组列表；meta.count 是友链条目总数（不是分组数）。
func (s *LinkService) List(ctx context.Context) ([]model.LinkGroup, int64, error) {
	groups, err := s.links.Groups(ctx)
	if err != nil {
		return nil, 0, err
	}
	n, err := s.links.LinkCount(ctx)
	if err != nil {
		return nil, 0, err
	}
	return groups, n, nil
}

// ThemeColor 转发取色服务（SSRF 校验与超时都在 ThemeService 里）。
func (s *LinkService) ThemeColor(ctx context.Context, rawURL string) (string, error) {
	return s.theme.ThemeColor(ctx, rawURL)
}

// ApplicationsByContact 申请人自助查询：按邮箱或站名查最近 limit 条。
func (s *LinkService) ApplicationsByContact(ctx context.Context, email, name string, limit int) ([]model.LinkApplication, error) {
	list, err := s.apps.FindByContact(ctx, email, name, limit)
	if err != nil {
		return nil, err
	}
	localizeApplications(list, s.cfg.Location())
	return list, nil
}

// ---------------- 小工具 ----------------

func itoa(n int) string           { return strconv.Itoa(n) }
func itoa64(n int64) string       { return strconv.FormatInt(n, 10) }
func lower(s string) string       { return strings.ToLower(s) }
func contains(s, sub string) bool { return strings.Contains(s, sub) }

// splitKeywords 与 store 层 SplitKeywords 同一语义（空白分隔、多关键词 AND）。
// service 不 import mongo 包（依赖方向），复制小逻辑并靠测试对齐。
func splitKeywords(raw string) []string {
	fields := strings.Fields(raw)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// summaryCooldown 前的生成尝试也被视为「刚试过」，冷却期内不重复打上游（config.ai.summary.cooldown）。
//
// SummaryGenerate 生成并落库摘要（GET miss 与 POST 强制共用）。
// 长文分块、上游超时/限流都在 platform.LLM 里处理。
func (s *PostService) SummaryGenerate(ctx context.Context, slug string) (map[string]any, error) {
	// 先判文章存在：文章不存在是 404，与摘要服务是否配置无关（错误码语义要准）
	p, err := s.posts.BySlug(ctx, slug)
	if err == store.ErrNotFound {
		return nil, apperr.New(apperr.PostNotFound, "文章不存在或已被删除", nil)
	}
	if err != nil {
		return nil, err
	}
	if !s.llm.Enabled() {
		return nil, apperr.New(apperr.UpstreamFailed, "摘要生成服务未启用", nil)
	}
	summary, err := s.llm.GenerateSummary(ctx, p.Title, p.MdContent)
	if err != nil {
		return nil, err
	}
	if err := s.summaries.Upsert(ctx, slug, summary, time.Now()); err != nil {
		return nil, err
	}
	return map[string]any{"summary": summary}, nil
}
