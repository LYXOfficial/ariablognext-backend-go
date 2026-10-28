package service

// 语义搜索单测：假 embedder（确定性向量）+ fake store。
// 覆盖三件事：余弦排序正确、RRF 两路融合、阈值拦截「硬匹」与降级路径。

import (
	"context"
	"errors"
	"testing"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store/fake"
)

// fakeEmbedder：按文本首字符哈希生成固定维度向量，查询与文档的相似度可控。
type fakeEmbedder struct {
	vecs    map[string][]float32 // 文本 → 向量（未命中按全零）
	enabled bool
	fail    bool
}

func (f *fakeEmbedder) Enabled() bool { return f.enabled }

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if f.fail {
		return nil, errors.New("embedder down")
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if v, ok := f.vecs[t]; ok {
			out[i] = v
			continue
		}
		out[i] = make([]float32, 4) // 全零 = 与任何向量点积 0（必然低于阈值）
	}
	return out, nil
}

func semanticTestConfig() *config.Config {
	cfg := serviceConfig()
	cfg.Features.SemanticSearch = true
	cfg.AI.Embedding = config.EmbeddingConfig{
		Enabled: true, Model: "test-model", Dim: 4, MinScore: 0.5,
	}
	return cfg
}

func semanticTestService(t *testing.T, cfg *config.Config, emb *fakeEmbedder) (*SemanticService, *fake.Store) {
	t.Helper()
	st := fake.New()
	return NewSemanticService(cfg, st.Posts(), st.Embeddings(), emb), st
}

// seedSemPosts 铺三篇：fiber（向量/关键词都该命中）、go（仅关键词）、其它。
func seedSemPosts(st *fake.Store) {
	st.SetPosts([]model.Post{
		{Slug: "fiber", Title: "Fiber 入门", MdContent: "fiber 框架介绍", PlainContent: "fiber 框架介绍", PublishTime: 300},
		{Slug: "go", Title: "Go 并发", MdContent: "go goroutine", PlainContent: "go goroutine", PublishTime: 200},
		{Slug: "life", Title: "日常", MdContent: "今天天气不错", PlainContent: "今天天气不错", PublishTime: 100},
	})
}

// TestSemanticHybridOrder：语义路命中的文章排前，关键词命中的也必须在场。
func TestSemanticHybridOrder(t *testing.T) {
	cfg := semanticTestConfig()
	emb := &fakeEmbedder{enabled: true, vecs: map[string][]float32{
		"框架": {1, 0, 0, 0},
	}}
	svc, st := semanticTestService(t, cfg, emb)
	seedSemPosts(st)
	// 给 fiber 的 chunk 一个与 query 平行的向量（余弦 1.0）
	st.SetEmbedding(store.EmbeddingDoc{
		Slug: "fiber", Model: "test-model", Dim: 4, UpdatedAt: 100,
		Chunks: []store.EmbeddingChunk{{Idx: 0, Text: "fiber 框架介绍", Vector: []float32{1, 0, 0, 0}}},
	})

	res, err := svc.Search(context.Background(), "框架")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("应至少命中 fiber")
	}
	if res[0].Slug != "fiber" {
		t.Errorf("语义直命中应排第一，得到 %v", res)
	}
	// 关键词路：query「框架」字面只在 fiber 里 → fiber 双路叠加仍第一；其余文章字面无命中
	for _, r := range res {
		if r.Slug == "fiber" && r.Context == nil {
			t.Error("语义命中应带 chunk 片段")
		}
	}
}

// TestSemanticThresholdBlocksForcedMatch：库里没有相关内容时不得硬排——
// 全部 chunk 都低于阈值，结果退化为纯关键词（此处为空）。
func TestSemanticThresholdBlocksForcedMatch(t *testing.T) {
	cfg := semanticTestConfig()
	emb := &fakeEmbedder{enabled: true} // query 无预设向量 → 全零 → 点积 0 < 0.5
	svc, st := semanticTestService(t, cfg, emb)
	seedSemPosts(st)
	st.SetEmbedding(store.EmbeddingDoc{
		Slug: "fiber", Model: "test-model", Dim: 4, UpdatedAt: 100,
		Chunks: []store.EmbeddingChunk{{Idx: 0, Text: "fiber 框架介绍", Vector: []float32{1, 0, 0, 0}}},
	})

	res, err := svc.Search(context.Background(), "红烧肉怎么做")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Slug == "fiber" {
			t.Errorf("低于阈值的语义命中不该出现：%+v", res)
		}
	}
}

// TestSemanticFallbackWhenDisabled：开关关 = 纯关键词（与 /posts/list?keyword= 同序）。
func TestSemanticFallbackWhenDisabled(t *testing.T) {
	cfg := semanticTestConfig()
	cfg.Features.SemanticSearch = false
	svc, st := semanticTestService(t, cfg, &fakeEmbedder{enabled: true})
	seedSemPosts(st)

	res, err := svc.Search(context.Background(), "fiber")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Slug != "fiber" {
		t.Errorf("关闭开关应只回关键词命中：%+v", res)
	}
}

// TestSemanticDegradeOnError：向量化失败 → 降级关键词，不返回 502。
func TestSemanticDegradeOnError(t *testing.T) {
	cfg := semanticTestConfig()
	svc, st := semanticTestService(t, cfg, &fakeEmbedder{enabled: true, fail: true})
	seedSemPosts(st)

	res, err := svc.Search(context.Background(), "Go")
	if err != nil {
		t.Fatalf("失败应降级而不是报错：%v", err)
	}
	if len(res) != 1 || res[0].Slug != "go" {
		t.Errorf("降级后应有关键词结果：%+v", res)
	}
}
