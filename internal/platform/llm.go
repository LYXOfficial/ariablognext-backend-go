package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
)

// LLM 是 SiliconFlow 摘要生成客户端。
// 上游很常见超时/限流（旧后端 50602），错误必须带 upstream 字段方便前端区分。
type LLM struct {
	cfg config.SummaryConfig
}

func NewLLM(cfg config.SummaryConfig) *LLM {
	return &LLM{cfg: cfg}
}

// Enabled 判断是否配置完整。
func (l *LLM) Enabled() bool { return l.cfg.Enabled && l.cfg.APIKey != "" }

// GenerateSummary 给定标题与 markdown 原文，返回摘要文本。
// 长文超上下文是常态（设计文档 §16），按 6000 字符分块、分别摘要后合并。
func (l *LLM) GenerateSummary(ctx context.Context, title, markdown string) (string, error) {
	if !l.Enabled() {
		return "", apperr.New(apperr.UpstreamFailed, "摘要生成服务未启用", nil)
	}
	// 上下文保护：超长文先分块（约 6000 字符一块），块数>1 时做「分块摘要 → 合并摘要」
	const chunkSize = 6000
	var final string
	chunks := splitChunks(markdown, chunkSize)
	if len(chunks) == 1 {
		out, err := l.chat(ctx, buildSummaryPrompt(title, chunks[0]))
		if err != nil {
			return "", err
		}
		final = out
	} else {
		var partials []string
		for i, chunk := range chunks {
			if i >= 6 { // 6 块以上的天书不再细分，直接截断保护上游
				break
			}
			out, err := l.chat(ctx, buildSummaryPrompt(title, chunk))
			if err != nil {
				return "", err
			}
			partials = append(partials, out)
		}
		merged, err := l.chat(ctx, buildMergePrompt(title, partials))
		if err != nil {
			return "", err
		}
		final = merged
	}
	return final, nil
}

// chat 调 SiliconFlow chat/completions。
func (l *LLM) chat(ctx context.Context, prompt string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model": l.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": "你是博客文章摘要助手。请用简体中文输出 2-3 句话的摘要，概括文章核心内容，不要客套话。"},
			{"role": "user", "content": prompt},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", apperr.Wrap(apperr.UpstreamFailed, "构造摘要请求失败", err)
	}
	req.Header.Set("Authorization", "Bearer "+l.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: l.cfg.Timeout.Std()}
	resp, err := client.Do(req)
	if err != nil {
		return "", apperr.New(apperr.UpstreamFailed, "摘要生成服务无响应，可稍后重试",
			apperr.UpstreamDetails{Cause: err.Error(), Upstream: "siliconflow", Status: 0})
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", apperr.New(apperr.UpstreamFailed,
			"摘要生成服务返回异常（"+fmt.Sprint(resp.StatusCode)+"），可稍后重试",
			apperr.UpstreamDetails{Cause: string(raw), Upstream: "siliconflow", Status: resp.StatusCode})
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Choices) == 0 {
		return "", apperr.New(apperr.UpstreamFailed, "摘要生成服务返回了无法解析的内容",
			apperr.UpstreamDetails{Cause: string(raw), Upstream: "siliconflow", Status: resp.StatusCode})
	}
	return parsed.Choices[0].Message.Content, nil
}

func buildSummaryPrompt(title, chunk string) string {
	return "文章标题：" + title + "\n\n以下是文章片段：\n" + chunk + "\n\n请概括这部分内容。"
}

func buildMergePrompt(title string, partials []string) string {
	joined := ""
	for i, p := range partials {
		joined += fmt.Sprintf("片段%d：%s\n\n", i+1, p)
	}
	return "文章标题：" + title + "\n\n以下是对各片段的摘要：\n" + joined + "请合并成一段 2-3 句话的整体摘要。"
}

// splitChunks 按字符数切分（中文按 rune），带 200 字符重叠避免句子被拦腰截断后丢上下文。
func splitChunks(s string, size int) []string {
	r := []rune(s)
	if len(r) <= size {
		return []string{s}
	}
	var out []string
	for start := 0; start < len(r); start += size - 200 {
		end := start + size
		if end > len(r) {
			end = len(r)
		}
		out = append(out, string(r[start:end]))
		if end == len(r) {
			break
		}
	}
	return out
}
