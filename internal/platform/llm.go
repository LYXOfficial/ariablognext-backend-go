package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

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
// summaryPromptPrefix 与旧后端（ariablognext-backend/get/post/main.py 的
// _request_summary）**逐字一致**，不要改：它承载了 Aria 的人设、篇幅（约一百字）、
// 不确定内容略过等约束。prompt 任何改动都必须先得到作者同意。
// 末尾的 \n 也是旧实现的一部分（prompt 末尾换行后直接拼正文）。
const summaryPromptPrefix = "你是一位专业的作家，接下来请用精炼并平易近人而并不高深的语言，尽可能更可爱且女性化一些，但尽量直截了当描述，不要做作，使用Aria作为作者的自称，写一份一百字左右的文章总结，态度要与作者表达的所保持一致，尽可能不要有幻觉，遇到你所不确定的部分和敏感的（政治或是较负面情绪等内容）请略过而不要回答，而遇到一些晦涩的东西可以尝试解释，接下来给你的文章是markdown格式的，请输出纯文本并使用中文回答我，不要输出多余的内容，只输出文章摘要的正文，不要输出换行符和在编程语言中的非法文字。接下来是文章的正文内容：\n"

// GenerateSummary 生成摘要。行为与旧后端一致：
//   - 单次请求、整篇正文直接拼在 prompt 后面（旧实现就是如此，不做分块合并）；
//   - prompt 里不含标题（旧 prompt 只有正文）；
//   - 响应里的换行全部去掉后入库。
//
// title 参数保留是为了上层签名稳定，但不进 prompt——旧后端不传它。
func (l *LLM) GenerateSummary(ctx context.Context, title, markdown string) (string, error) {
	_ = title
	if !l.Enabled() {
		return "", apperr.New(apperr.UpstreamFailed, "摘要生成服务未启用", nil)
	}
	out, err := l.chat(ctx, summaryPromptPrefix+markdown)
	if err != nil {
		return "", err
	}
	// 旧后端：response.json()["choices"][0]["message"]["content"].replace("\n", "")
	return strings.ReplaceAll(out, "\n", ""), nil
}

// chat 调 SiliconFlow chat/completions。
// chatBody 构造请求体：形状与旧后端一致，模型专属参数（Qwen3 的
// enable_thinking=false、max_tokens 等）全部由 config.yml 的
// ai.summary.extraBody 提供，代码里不写任何厂商特判。
func (l *LLM) chatBody(prompt string) map[string]any {
	// 请求体形状与旧后端一致：只有 model + messages（**没有 system 消息**，
	// 也没有 max_tokens）；模型专属参数统一由 config 的 ai.summary.extraBody 提供。
	body := map[string]any{
		"model": l.cfg.Model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	for k, v := range l.cfg.ExtraBody {
		body[k] = v // 配置优先：可以覆盖 model/max_tokens，也可以加任意厂商参数
	}
	return body
}

func (l *LLM) chat(ctx context.Context, prompt string) (string, error) {
	body, _ := json.Marshal(l.chatBody(prompt))
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
