package store

import "context"

// 语义搜索的向量存储。设计决策（2026-10-28，用户拍板）：
// 向量**不**进 Posts 主文档，单开 PostEmbeddings 集合——
// 列表投影零改动（漏一处排除就是把几 KB 浮点数发给前端）、
// 回填/换模型 = 附属集合整删重建，不碰 Posts。

// EmbeddingChunk 是文章的一个分块的向量。Text 保留原文（命中后当 context 片段返回）。
type EmbeddingChunk struct {
	Idx  int    `bson:"idx" json:"idx"`
	Text string `bson:"text" json:"text"`
	// truncate：BSON 只有 double，任何写入方（Go 的 float32、Python 的全精度 double）
	// 落库后都是 double。不加这个 tag，驱动只在「double 能被 float32 精确表示」时
	// 才允许解码——Python 写入的全精度向量会直接报错，而错误会被语义搜索静默降级
	// 成关键词搜索（2026-10-28 实际踩过）。加了之后任意精度都能读。
	Vector []float32 `bson:"vector,truncate" json:"vector"`
}

// EmbeddingDoc 是一篇文章的全部向量。Slug 作为 _id：一篇文章最多一份向量，
// upsert 天然幂等；文章删除时按 slug 级联清理。
// Model/Dim 用于换模型检测：与配置不一致的文档由回填脚本重算（scripts/build_embeddings.py）。
type EmbeddingDoc struct {
	Slug  string `bson:"_id" json:"slug"`
	Model string `bson:"model" json:"model"`
	Dim   int    `bson:"dim" json:"dim"`
	// TextHash 是嵌入源文本（searchText）的 sha256 前 16 位：增量回填的判定键。
	// 不用 lastUpdatedTime：只改正文的接口（PUT /posts/content）不会刷新它，
	// 是后台保存流程紧接着调 updatePostInfo 才补上的——用 hash 就不依赖这个约定，
	// 任何调用方（脚本/直接调 API）改完正文都能被下一次回填发现。
	TextHash  string           `bson:"textHash" json:"textHash"`
	UpdatedAt int64            `bson:"updatedAt" json:"updatedAt"`
	Chunks    []EmbeddingChunk `bson:"chunks" json:"chunks"`
}

// EmbeddingRepository 是向量集合的读写口。检索数学（余弦/RRF）在 service 层做——
// 83 篇规模全量加载到内存暴力算比任何 ANN 都快且准，store 保持「笨存储」。
type EmbeddingRepository interface {
	// Upsert 以 slug 为键整体替换。重复键不报错（语义就是覆盖）。
	Upsert(ctx context.Context, doc EmbeddingDoc) error
	// DeleteBySlug 幂等：不存在返回 (false, nil)。
	DeleteBySlug(ctx context.Context, slug string) (bool, error)
	// ListAll 全量拉取（含向量）。83 篇约 1MB，service 层有 5 分钟 TTL 缓存。
	ListAll(ctx context.Context) ([]EmbeddingDoc, error)
}
