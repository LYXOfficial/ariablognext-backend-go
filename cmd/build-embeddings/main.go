// cmd/build-embeddings 是语义搜索的预留位。
//
// 现在只打印「未配置，跳过」并退出 0（TASKS P4 的明确要求）：
// 语义搜索的对外契约（/posts/semantic → 501）已经挂好，将来接向量检索时
// 在这里实现：读 ai.embedding 配置 → 分批调 embedding API → 写
// Posts.embedding / embeddingMeta（见 design-notes §2.6）。
package main

import (
	"log/slog"
	"os"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	cfg, err := config.Load()
	if err != nil {
		slog.Error("配置加载失败", "err", err)
		os.Exit(1)
	}
	if !cfg.AI.Embedding.Enabled {
		slog.Info("语义搜索未配置，跳过（ai.embedding.enabled=false）")
		return
	}
	// 配置开了但实现还没写：明确告知，不假装成功
	slog.Warn("语义搜索配置已开启，但 embedding 生成尚未实现（/posts/semantic 仍返回 501）",
		"provider", cfg.AI.Embedding.Provider, "model", cfg.AI.Embedding.Model)
}
