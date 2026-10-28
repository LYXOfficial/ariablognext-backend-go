package fake

// embedRepo：内存实现。语义检索的单测在 service 层用真数据喂这里。

import (
	"context"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

func (s *Store) Embeddings() store.EmbeddingRepository { return embedRepo{s} }

type embedRepo struct{ st *Store }

var _ store.EmbeddingRepository = (*embedRepo)(nil)

func (r embedRepo) Upsert(_ context.Context, doc store.EmbeddingDoc) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	r.st.embeddings[doc.Slug] = doc
	return nil
}

func (r embedRepo) DeleteBySlug(_ context.Context, slug string) (bool, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if _, ok := r.st.embeddings[slug]; !ok {
		return false, nil
	}
	delete(r.st.embeddings, slug)
	return true, nil
}

func (r embedRepo) ListAll(_ context.Context) ([]store.EmbeddingDoc, error) {
	r.st.mu.RLock()
	defer r.st.mu.RUnlock()
	out := make([]store.EmbeddingDoc, 0, len(r.st.embeddings))
	for _, d := range r.st.embeddings {
		out = append(out, d)
	}
	return out, nil
}

// SetEmbedding 供测试直接铺向量数据（不经 Upsert 的锁路径）。
func (s *Store) SetEmbedding(doc store.EmbeddingDoc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.embeddings == nil {
		s.embeddings = map[string]store.EmbeddingDoc{}
	}
	s.embeddings[doc.Slug] = doc
}
