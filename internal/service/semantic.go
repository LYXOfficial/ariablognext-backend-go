package service

// SemanticService：搜索（/posts/semantic）。
//
// 结果分层（2026-10-28 用户定）：
//  1. 关键词精确命中（整段子串，tier 2）——最前
//  2. 关键词模糊命中（n-gram / 拼写容错 / 别名，tier 1）——中间
//  3. 语义命中（向量余弦 ≥ minScore）——最后，按余弦从高到低
//
// 开关语义：features.semanticSearch 开 → 三层；关（或向量化失败降级）→ 只有关键词两层。
// 调用方永远拿得到结果，不存在 501。不设条数上限（翻页交给前端）。

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

// 搜索引擎标识：写进响应头 X-Search-Engine，便于线上区分「真语义」与「静默降级」。
const (
	EngineHybrid          = "hybrid"
	EngineKeyword         = "keyword"
	EngineKeywordFallback = "keyword-fallback"
)

// 各层的 score 取值（对外可见，量纲统一为「越大越相关」）：
// 关键词精确 1.0 > 模糊 0.8 > 语义按余弦（0~1）。
const (
	scoreExactKeyword float32 = 1.0
	scoreFuzzyKeyword float32 = 0.8
)

// Embedder 是向量化能力的最小接口：为单测注入假实现而设（platform.Embedder 实现它）。
type Embedder interface {
	Enabled() bool
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// 匹配类型（对外的 matchType 字段）。
const (
	MatchTypeExact    = "exact"    // 关键词整段子串命中
	MatchTypeFuzzy    = "fuzzy"    // n-gram 部分匹配 / 拼写容错 / 别名
	MatchTypeSemantic = "semantic" // 向量语义命中
)

// SemanticResult 是 /posts/semantic 的响应项。
type SemanticResult struct {
	Slug        string  `json:"slug"`
	Title       string  `json:"title"`
	BannerImg   string  `json:"bannerImg"`
	PublishTime int64   `json:"publishTime"`
	Context     *string `json:"context"`
	// MatchType：exact / fuzzy / semantic，对应结果的三层。
	MatchType string `json:"matchType"`
	// MatchScore：匹配度 0~1（精确 1.0、模糊 0.8、语义为余弦）。
	MatchScore float32 `json:"matchScore"`
	// Score 是历史字段，与 MatchScore 同值，保留以免破坏已有调用方。
	Score float32 `json:"score"`
}

// docsCacheTTL：全量向量在内存里的缓存时长（回填是独立进程，这里感知不到它的写入）。
const docsCacheTTL = 5 * time.Minute

// chunkContextLen 是语义命中返回给前端的片段长度（runes）。
const chunkContextLen = 120

// chunkHit 是一篇文档的语义命中（取最强的那一块）。
type chunkHit struct {
	slug     string
	score    float32
	chunkIdx int
}

type SemanticService struct {
	cfg      *config.Config
	posts    store.PostRepository
	emb      store.EmbeddingRepository
	embedder Embedder

	mu      sync.Mutex
	cache   []store.EmbeddingDoc
	cacheAt time.Time
}

func NewSemanticService(cfg *config.Config, posts store.PostRepository, emb store.EmbeddingRepository, embedder Embedder) *SemanticService {
	return &SemanticService{cfg: cfg, posts: posts, emb: emb, embedder: embedder}
}

// Search 返回分层结果与引擎标识。
func (s *SemanticService) Search(ctx context.Context, query string) ([]SemanticResult, string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, "", apperr.New(apperr.ValidationMissingField, "缺少 query",
			apperr.FieldDetails{Fields: []apperr.FieldError{{Field: "query", Message: "必填"}}})
	}
	if s.cfg.Features.SemanticSearch && s.embedder.Enabled() {
		res, err := s.hybrid(ctx, query)
		if err == nil {
			return res, EngineHybrid, nil
		}
		// 向量化/加载失败不把 502 抛给搜索场景（高频路径）：降级出关键词结果
		slog.Warn("语义搜索降级为关键词搜索", "err", err)
		res, kwErr := s.keywordOnly(ctx, query)
		return res, EngineKeywordFallback, kwErr
	}
	res, err := s.keywordOnly(ctx, query)
	return res, EngineKeyword, err
}

// keywordFilter 构造关键词过滤条件（含配置化的别名展开）。
func (s *SemanticService) keywordFilter(query string) store.PostFilter {
	f := store.PostFilter{Keyword: query, Full: true}
	f.Aliases = aliasesFor(s.cfg, query)
	return f
}

// keywordOnly：语义关闭/降级时使用，与 /posts/list?keyword= 同源同序（精确在前、模糊在后）。
func (s *SemanticService) keywordOnly(ctx context.Context, query string) ([]SemanticResult, error) {
	page, err := s.posts.Page(ctx, s.keywordFilter(query), s.cfg.Location())
	if err != nil {
		return nil, err
	}
	out := make([]SemanticResult, 0, len(page.Items))
	for _, p := range page.Items {
		out = append(out, keywordResult(p))
	}
	return out, nil
}

// hybrid：关键词两层 + 语义层。
// 关键词命中过的文章不再出现在语义层（避免重复），语义层内部按余弦降序。
func (s *SemanticService) hybrid(ctx context.Context, query string) ([]SemanticResult, error) {
	kwPage, err := s.posts.Page(ctx, s.keywordFilter(query), s.cfg.Location())
	if err != nil {
		return nil, err
	}

	vecs, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, apperr.New(apperr.UpstreamFailed, "向量化返回条数异常", nil)
	}
	docs, err := s.loadDocs(ctx)
	if err != nil {
		return nil, err
	}
	minScore := float32(s.cfg.AI.Embedding.MinScore)
	if minScore <= 0 {
		minScore = 0.52
	}

	// 语义层：每篇取最强块，按余弦降序
	var semHits []chunkHit
	for _, d := range docs {
		if d.Model != s.cfg.AI.Embedding.Model || d.Dim != s.cfg.AI.Embedding.Dim {
			continue // 模型/维度不匹配的旧向量不参与（等回填重算）
		}
		best, bestScore := -1, float32(-2)
		for _, c := range d.Chunks {
			if len(c.Vector) != len(vecs[0]) {
				continue
			}
			var dot float32
			for i := range c.Vector {
				dot += c.Vector[i] * vecs[0][i]
			}
			if dot > bestScore {
				bestScore, best = dot, c.Idx
			}
		}
		if best >= 0 && bestScore >= minScore {
			semHits = append(semHits, chunkHit{slug: d.Slug, score: bestScore, chunkIdx: best})
		}
	}
	sort.Slice(semHits, func(i, j int) bool { return semHits[i].score > semHits[j].score })

	out := make([]SemanticResult, 0, len(kwPage.Items)+len(semHits))
	seen := make(map[string]bool, len(kwPage.Items))
	// 第一、二层：存储层已按 tier（精确→模糊）排好
	for _, p := range kwPage.Items {
		out = append(out, keywordResult(p))
		seen[p.Slug] = true
	}
	// 第三层：语义命中，余弦从高到低
	for _, h := range semHits {
		if seen[h.slug] {
			continue
		}
		p, err := s.posts.BySlug(ctx, h.slug)
		if err != nil {
			continue // 向量比正文新（文章刚删）
		}
		item := SemanticResult{
			Slug: p.Slug, Title: p.Title, BannerImg: p.BannerImg,
			PublishTime: p.PublishTime,
			MatchType:   MatchTypeSemantic,
			MatchScore:  h.score,
			Score:       h.score,
		}
		item.Context = s.chunkContext(docs, h)
		out = append(out, item)
	}
	return out, nil
}

// chunkContext 取命中块的开头做片段（前端高亮会在含 query 字面时生效）。
func (s *SemanticService) chunkContext(docs []store.EmbeddingDoc, h chunkHit) *string {
	for _, d := range docs {
		if d.Slug != h.slug {
			continue
		}
		for _, c := range d.Chunks {
			if c.Idx == h.chunkIdx {
				text := truncateRunes(c.Text, chunkContextLen)
				return &text
			}
		}
	}
	return nil
}

// keywordResult 把关键词命中转成响应项：按 tier 给固定 score。
func keywordResult(p model.Post) SemanticResult {
	item := SemanticResult{
		Slug: p.Slug, Title: p.Title, BannerImg: p.BannerImg,
		PublishTime: p.PublishTime, Context: p.Context,
		MatchType: MatchTypeFuzzy, MatchScore: scoreFuzzyKeyword, Score: scoreFuzzyKeyword,
	}
	if p.MatchTier >= 2 {
		item.MatchType = MatchTypeExact
		item.MatchScore = scoreExactKeyword
		item.Score = scoreExactKeyword
	}
	return item
}

// loadDocs 带缓存的全量向量加载。
func (s *SemanticService) loadDocs(ctx context.Context) ([]store.EmbeddingDoc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache != nil && time.Since(s.cacheAt) < docsCacheTTL {
		return s.cache, nil
	}
	docs, err := s.emb.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	s.cache, s.cacheAt = docs, time.Now()
	return docs, nil
}
