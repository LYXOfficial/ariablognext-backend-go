package platform

// Embedder 是语义搜索的向量化客户端（SiliconFlow /v1/embeddings，OpenAI 兼容形态）。
// 只做「文本 → 归一化向量」这一件事；检索数学在 service 层。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
)

type Embedder struct {
	cfg config.EmbeddingConfig
}

func NewEmbedder(cfg config.EmbeddingConfig) *Embedder {
	return &Embedder{cfg: cfg}
}

// Enabled 判断配置是否完整（四项缺一不可，与启动校验一致）。
func (e *Embedder) Enabled() bool {
	return e.cfg.Enabled && e.cfg.Endpoint != "" && e.cfg.Model != "" && e.cfg.APIKey != "" && e.cfg.Dim > 0
}

// embedResponse 是 /v1/embeddings 的响应骨架。data[].index 标记输入顺序，
// 上游不保证有序，必须按 index 重排。
type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed 把一批文本向量化。调用方控制批量大小（回填脚本用 16/批）；
// 返回的向量已做 L2 归一化——归一化后余弦相似度退化为点积，检索端省一次开方。
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if !e.Enabled() {
		return nil, apperr.New(apperr.UpstreamFailed, "向量化服务未启用", nil)
	}
	if len(texts) == 0 {
		return nil, nil
	}
	timeout := e.cfg.Timeout.Std()
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, err := json.Marshal(map[string]any{"model": e.cfg.Model, "input": texts})
	if err != nil {
		return nil, apperr.Wrap(apperr.UpstreamFailed, "构造向量化请求失败", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, apperr.Wrap(apperr.UpstreamFailed, "构造向量化请求失败", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, apperr.Wrap(apperr.UpstreamFailed, "向量化服务不可达", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apperr.New(apperr.UpstreamFailed, "向量化服务返回了意外状态",
			apperr.UpstreamDetails{Cause: "embeddings", Upstream: "siliconflow", Status: resp.StatusCode})
	}
	var out embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, apperr.Wrap(apperr.UpstreamFailed, "向量化响应解析失败", err)
	}
	if len(out.Data) != len(texts) {
		return nil, apperr.New(apperr.UpstreamFailed, "向量化服务返回条数与输入不符",
			apperr.UpstreamDetails{Cause: fmt.Sprintf("输入 %d 条，返回 %d 条", len(texts), len(out.Data)),
				Upstream: "siliconflow", Status: resp.StatusCode})
	}
	vectors := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(texts) {
			return nil, apperr.New(apperr.UpstreamFailed, "向量化服务返回了越界的 index", nil)
		}
		if len(d.Embedding) != e.cfg.Dim {
			return nil, apperr.New(apperr.UpstreamFailed, "向量维度与配置不符",
				apperr.UpstreamDetails{Cause: fmt.Sprintf("配置 dim=%d，返回 %d", e.cfg.Dim, len(d.Embedding)),
					Upstream: "siliconflow", Status: resp.StatusCode})
		}
		vectors[d.Index] = normalize(d.Embedding)
	}
	return vectors, nil
}

// normalize 原地 L2 归一化。零向量（空文本嵌入）不缩放，避免除零产生 NaN。
func normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
	return v
}
