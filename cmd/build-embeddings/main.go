// cmd/build-embeddings：语义搜索的全量回填命令。
//
// 用法：
//
//	go run ./cmd/build-embeddings            # 增量：只算缺失/换模型/正文变旧的
//	go run ./cmd/build-embeddings -force     # 全量重算（换模型后用）
//
// 行为：拉全量文章 → searchText 纯文本 → 6000 字分块 → 批量调 embedding API →
// upsert 进 PostEmbeddings 集合。判定重算的依据（三选一即重算）：
//   - 该 slug 没有向量；
//   - 已存 model/dim 与当前配置不一致；
//   - 已存 updatedAt < 文章 lastUpdatedTime（正文比向量新）。
//
// 注意：这是**写操作**，只读模式（storage.read_only）会被适配器硬闸门挡下。
// 数据侧设计决策（2026-10-28 用户拍板）：向量不进 Posts 主文档，
// 单开 PostEmbeddings 集合——列表投影零改动，回填/换模型 = 附属集合重建。

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"log/slog"
	"os"
	"time"

	_ "time/tzdata"

	_ "github.com/LYXOfficial/ariablognext-backend-go/internal/store/mongo"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/platform"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/textutil"
)

// chunkSize 与摘要生成分块一致：6000 字符约是长上下文的安全线，
// 也远小于 bge-m3 单条 8192 token 的输入上限（双保险）。
const chunkSize = 6000

// embedBatch 是单次 API 请求的文本条数。分块后的文本远小于输入上限，
// 16 条一批在请求次数与单请求延迟之间取平衡。
const embedBatch = 16

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	force := flag.Bool("force", false, "忽略 model/dim/updatedAt 判断，全量重算")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("配置加载失败", "err", err)
		os.Exit(1)
	}
	if !cfg.AI.Embedding.Enabled {
		slog.Error("ai.embedding.enabled=false，先在 config.yml 打开并配置 EMBEDDING_API_KEY")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	st, err := store.Open(ctx, cfg)
	if err != nil {
		slog.Error("存储初始化失败", "err", err)
		os.Exit(1)
	}
	defer st.Close(context.Background())

	embedder := platform.NewEmbedder(cfg.AI.Embedding)
	if !embedder.Enabled() {
		slog.Error("embedding 配置不完整",
			"endpoint", cfg.AI.Embedding.Endpoint != "",
			"model", cfg.AI.Embedding.Model,
			"apiKey", cfg.AI.Embedding.APIKey != "")
		os.Exit(1)
	}

	// full 拉全量正文。full 的 300 条上限是「对外接口」的契约；
	// 回填是内部命令，文章超过 300 篇时这里要改成分页循环——先留断言提醒。
	posts, err := st.Posts().Page(ctx, store.PostFilter{Full: true, WithContent: true}, cfg.Location())
	if err != nil {
		slog.Error("拉取文章失败", "err", err)
		os.Exit(1)
	}
	if posts.Count > int64(cfg.List.MaxFullItems) {
		slog.Error("文章数超过 full 上限，回填命令需要改成分页循环", "count", posts.Count)
		os.Exit(1)
	}

	existing := map[string]store.EmbeddingDoc{}
	oldDocs, err := st.Embeddings().ListAll(ctx)
	if err != nil {
		slog.Error("读取已有向量失败", "err", err)
		os.Exit(1)
	}
	for _, d := range oldDocs {
		existing[d.Slug] = d
	}

	type pendingChunk struct {
		slug   string
		chunks []string
		hash   string
		now    int64
	}
	var (
		done, skipped, failed int
		batchTexts            []string
		batch                 []pendingChunk
	)

	flush := func() error {
		if len(batchTexts) == 0 {
			return nil
		}
		vectors, err := embedder.Embed(ctx, batchTexts)
		if err != nil {
			return err
		}
		// 把打平的向量按 (文章, 分块) 归位后逐篇 upsert
		cursor := 0
		for _, pc := range batch {
			doc := store.EmbeddingDoc{
				Slug: pc.slug, Model: cfg.AI.Embedding.Model,
				Dim: cfg.AI.Embedding.Dim, TextHash: pc.hash, UpdatedAt: pc.now,
				Chunks: make([]store.EmbeddingChunk, 0, len(pc.chunks)),
			}
			for i, text := range pc.chunks {
				doc.Chunks = append(doc.Chunks, store.EmbeddingChunk{
					Idx: i, Text: text, Vector: vectors[cursor],
				})
				cursor++
			}
			if err := st.Embeddings().Upsert(ctx, doc); err != nil {
				failed++
				slog.Warn("单篇向量写入失败", "slug", pc.slug, "err", err)
			} else {
				done++
			}
		}
		batchTexts = batchTexts[:0]
		batch = batch[:0]
		return nil
	}

	for _, p := range posts.Items {
		// 用 searchText 做嵌入文本：标题+正文纯文本、小写、去 Markdown——
		// 与关键词搜索同一份数据，两条召回路看到的文本一致
		text := textutil.BuildSearchText(p.Title, p.MdContent)
		if text == "" {
			skipped++
			continue
		}
		// 增量判定键 = 嵌入源文本的 hash：新增/改文/换模型三种变化都会被捕捉到。
		// 不比对 lastUpdatedTime：只改正文的接口不刷新它（后台是另调 updatePostInfo
		// 补的），比对它等于把新鲜度寄托在前端调用顺序上
		sum := sha256.Sum256([]byte(text))
		textHash := hex.EncodeToString(sum[:])[:16]
		if old, ok := existing[p.Slug]; ok && !*force &&
			old.Model == cfg.AI.Embedding.Model && old.Dim == cfg.AI.Embedding.Dim &&
			old.TextHash == textHash {
			skipped++
			continue
		}
		now := time.Now().Unix()
		r := []rune(text)
		var chunks []string
		for i := 0; i*chunkSize < len(r); i++ {
			end := (i + 1) * chunkSize
			if end > len(r) {
				end = len(r)
			}
			chunks = append(chunks, string(r[i*chunkSize:end]))
		}
		batchTexts = append(batchTexts, chunks...)
		batch = append(batch, pendingChunk{slug: p.Slug, chunks: chunks, hash: textHash, now: now})
		if len(batchTexts) >= embedBatch {
			if err := flush(); err != nil {
				slog.Error("批量向量化失败，中断（已完成的保留，重跑即可续传）", "done", done, "err", err)
				os.Exit(1)
			}
		}
	}
	if err := flush(); err != nil {
		slog.Error("批量向量化失败（已完成的保留，重跑即可续传）", "done", done, "err", err)
		os.Exit(1)
	}

	slog.Info("回填完成",
		"total", posts.Count, "computed", done, "skipped", skipped, "failed", failed,
		"model", cfg.AI.Embedding.Model, "dim", cfg.AI.Embedding.Dim)
	if failed > 0 {
		os.Exit(1)
	}
}
