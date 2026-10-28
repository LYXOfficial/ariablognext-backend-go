package service

// SemanticService：语义搜索（/posts/semantic）。
// 开关语义（2026-10-28 用户定）：features.semanticSearch 开 → 语义+关键词 RRF 混合；
// 关（或向量化失败降级）→ 纯关键词搜索，调用方永远拿到结果，不再有 501。
// 防「硬匹」：语义侧有余弦阈值（ai.embedding.minScore），低于阈值不进融合——
// 全部低于阈值 = 返回纯关键词结果（可能是空数组），保住「没有就说没有」的旧行为。

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

// Embedder 是向量化能力的最小接口：为单测注入假实现而设（platform.Embedder 实现它）。
type Embedder interface {
	Enabled() bool
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// SemanticResult 是 /posts/semantic 的响应项：PostBrief 形状 + score。
// context 语义与关键词搜索一致（前端拿它做高亮），语义命中时是命中 chunk 的片段。
type SemanticResult struct {
	Slug        string  `json:"slug"`
	Title       string  `json:"title"`
	BannerImg   string  `json:"bannerImg"`
	PublishTime int64   `json:"publishTime"`
	Context     *string `json:"context"`
	Score       float32 `json:"score"`
}

// docsCacheTTL：全量向量在内存里的缓存时长。回填是独立进程，本实例感知不到
// 它的写入，5 分钟是「改完数据多久生效」的上限——个人博客完全够用。
const docsCacheTTL = 5 * time.Minute

// chunkContextLen 是语义命中返回给前端的片段长度（runes）。
// SearchBox 高亮按 query 字面匹配，片段太长没意义，太短不够判断相关性。
const chunkContextLen = 120

// chunkHit 移到包级：hybrid 里构造后要传给 hitSlugs，函数内类型没法跨函数传。
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

// Search 语义开关开 → 混合；关/失败 → 关键词回退。
// 不限结果个数（2026-10-28 用户定）：语义侧全量过阈值，关键词侧走 full——
// 结果太多是前端加翻页的事，服务端不擅自砍。
func (s *SemanticService) Search(ctx context.Context, query string) ([]SemanticResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, apperr.New(apperr.ValidationMissingField, "缺少 query",
			apperr.FieldDetails{Fields: []apperr.FieldError{{Field: "query", Message: "必填"}}})
	}
	if s.cfg.Features.SemanticSearch && s.embedder.Enabled() {
		res, err := s.hybrid(ctx, query)
		if err == nil {
			return res, nil
		}
		// 向量化/加载失败不把 502 抛给搜索场景——搜索是高频路径，
		// 降级出结果比报错有用（降级原因进日志）
		slog.Warn("语义搜索降级为关键词搜索", "err", err)
	}
	return s.keywordOnly(ctx, query)
}

// keywordOnly 纯关键词路径：与 /posts/list?keyword= 同源同序（含标题前置）。
func (s *SemanticService) keywordOnly(ctx context.Context, query string) ([]SemanticResult, error) {
	res, err := s.posts.Page(ctx, store.PostFilter{Keyword: query, Full: true}, s.cfg.Location())
	if err != nil {
		return nil, err
	}
	out := make([]SemanticResult, 0, len(res.Items))
	for i, p := range res.Items {
		out = append(out, postToResult(p, rankScore(i)))
	}
	return out, nil
}

// hybrid：语义召回 ∪ 关键词召回，RRF（1/(60+rank)）融合。
// 专有名词/代码标识符靠关键词兜底，同义表达靠向量，两者互补。
// 语义侧过 minScore 阈值：防止「库里没有相关内容也硬排 top-N」。
func (s *SemanticService) hybrid(ctx context.Context, query string) ([]SemanticResult, error) {
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
		// 实测校准（2026-10-28）：真相关 0.53-0.67，硬匹（演唱会门票→公交换乘题）0.51
		minScore = 0.52
	}

	// 语义侧：slug → 最高分 chunk（一篇文章多块取最强信号）
	var semHits []chunkHit
	for _, d := range docs {
		if d.Model != s.cfg.AI.Embedding.Model || d.Dim != s.cfg.AI.Embedding.Dim {
			continue // 模型/维度不匹配的旧向量不参与（等 build-embeddings 重算）
		}
		best := -1
		bestScore := float32(-2)
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
		if best >= 0 && bestScore >= minScore { // 阈值在这里，低于它不硬凑
			semHits = append(semHits, chunkHit{slug: d.Slug, score: bestScore, chunkIdx: best})
		}
	}
	sort.Slice(semHits, func(i, j int) bool { return semHits[i].score > semHits[j].score })

	// 关键词侧：同一套管线（含标题前置排序），full 拉全量
	kwRes, err := s.posts.Page(ctx, store.PostFilter{Keyword: query, Full: true}, s.cfg.Location())
	if err != nil {
		return nil, err
	}

	fused := rrfMerge(hitSlugs(semHits), postSlugs(kwRes.Items))

	semBySlug := map[string]chunkHit{}
	for _, h := range semHits {
		semBySlug[h.slug] = h
	}
	kwBySlug := map[string]model.Post{}
	for _, p := range kwRes.Items {
		kwBySlug[p.Slug] = p
	}

	out := make([]SemanticResult, 0, len(fused))
	for rank, slug := range fused {
		var brief model.PostBrief
		if p, ok := kwBySlug[slug]; ok {
			brief = p.Brief()
		} else {
			p, err := s.posts.BySlug(ctx, slug)
			if err != nil {
				continue // 向量比正文新（文章刚删）——跳过，等回填清理
			}
			brief = p.Brief()
		}
		item := SemanticResult{
			Slug: brief.Slug, Title: brief.Title,
			BannerImg: brief.BannerImg, PublishTime: brief.PublishTime,
			Score: rankScore(rank),
		}
		if h, ok := semBySlug[slug]; ok {
			// 命中 chunk 的开头当片段：可能含 query 字面（前端高亮就生效），
			// 不含也无所谓（语义相关本身就是片段成立的理由）
			for _, d := range docs {
				if d.Slug != slug {
					continue
				}
				for _, c := range d.Chunks {
					if c.Idx == h.chunkIdx {
						ctxText := chunkSnippet(c.Text)
						item.Context = &ctxText
					}
				}
			}
		} else if brief.Context != nil {
			ctxText := *brief.Context
			item.Context = &ctxText
		}
		out = append(out, item)
	}
	return out, nil
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

// rrfMerge 多路召回融合：score = Σ 1/(60+rank)。60 是论文标准参数，
// 抑制单路 rank 的极端权重；返回按融合分降序、去重。
func rrfMerge(rankLists ...[]string) []string {
	const k = 60
	scores := map[string]float32{}
	for _, list := range rankLists {
		for i, slug := range list {
			scores[slug] += 1 / float32(k+i+1)
		}
	}
	slugs := make([]string, 0, len(scores))
	for slug := range scores {
		slugs = append(slugs, slug)
	}
	sort.Slice(slugs, func(i, j int) bool { return scores[slugs[i]] > scores[slugs[j]] })
	return slugs
}

func hitSlugs(hits []chunkHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.slug)
	}
	return out
}

func postSlugs(posts []model.Post) []string {
	out := make([]string, 0, len(posts))
	for _, p := range posts {
		out = append(out, p.Slug)
	}
	return out
}

func postToResult(p model.Post, score float32) SemanticResult {
	item := SemanticResult{
		Slug: p.Slug, Title: p.Title, BannerImg: p.BannerImg,
		PublishTime: p.PublishTime, Score: score,
	}
	if p.Context != nil {
		ctxText := *p.Context
		item.Context = &ctxText
	}
	return item
}

// rankScore 把名次折算成响应里的 score：同 RRF 公式，保证开关两侧的
// score 量纲一致（前端如果要按 score 过滤/排序，两个模式下行为相同）。
func rankScore(rank int) float32 { return 1 / float32(60+rank+1) }

// chunkSnippet 语义命中的片段：复用 writes.go 的 truncateRunes（不额外造轮子），
// 但语义片段带省略号更自然——算了，保持一致不加深浅差异，直接截断。
func chunkSnippet(s string) string { return truncateRunes(s, chunkContextLen) }
