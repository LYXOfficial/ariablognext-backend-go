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
	// chatHook 仅测试注入：非 nil 时 chat 直接走它，避免单测打真实上游
	chatHook func(prompt string) (string, error)
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

// GenerateSummary 生成摘要。行为与旧后端一致，只是对超长文补了分块：
//   - 每块都用**同一段旧 prompt**（summaryPromptPrefix + 该块正文）；
//   - 多块时各块摘要再拼起来、用**同一段 prompt** 做一次合并
//     （合并输入是各块摘要，不是新写的"请合并"指令——prompt 文本没有任何新增）；
//   - 响应里的换行全部去掉后入库。
//
// title 参数保留是为了上层签名稳定，但不进 prompt——旧后端不传它。
func (l *LLM) GenerateSummary(ctx context.Context, title, markdown string) (string, error) {
	_ = title
	if !l.Enabled() {
		return "", apperr.New(apperr.UpstreamFailed, "摘要生成服务未启用", nil)
	}
	chunks := splitChunks(markdown, l.chunkSize())
	if len(chunks) == 1 {
		return l.summarize(ctx, chunks[0])
	}
	maxChunks := l.maxChunks()
	var partials []string
	for i, chunk := range chunks {
		if i >= maxChunks {
			// 超出上限的部分不再参与：多一次调用就多 3 秒，
			// 而超长文尾部的信息量对"一百字总结"边际很小
			break
		}
		p, err := l.summarize(ctx, chunk)
		if err != nil {
			return "", err
		}
		partials = append(partials, p)
	}
	return l.summarize(ctx, strings.Join(partials, "\n"))
}

// summarize 用同一段 prompt 生成一次摘要（分块与合并都走这里）。
func (l *LLM) summarize(ctx context.Context, content string) (string, error) {
	out, err := l.chat(ctx, summaryPromptPrefix+content)
	if err != nil {
		return "", err
	}
	// 旧后端：response.json()["choices"][0]["message"]["content"].replace("\n", "")
	return strings.ReplaceAll(out, "\n", ""), nil
}

// chunkSize / maxChunks 允许配置覆盖，缺省 6000 字符 / 6 块。
// 6000 字符对混合语料约 2.7k-3.5k token，留足 8k 上下文余量。
func (l *LLM) chunkSize() int {
	if l.cfg.ChunkSize > 0 {
		return l.cfg.ChunkSize
	}
	return 6000
}

func (l *LLM) maxChunks() int {
	if l.cfg.MaxChunks > 0 {
		return l.cfg.MaxChunks
	}
	return 6
}

// splitChunks 按字符数切分（中文按 rune），带重叠避免句子被拦腰截断后丢上下文。
// 重叠量默认 200 字符；当分块尺寸本身小于等于 200 时按 1/4 缩放，
// 保证步长恒正——chunkSize 现在是配置项，配小了不能让程序 panic。
func splitChunks(s string, size int) []string {
	if size <= 0 {
		return []string{s}
	}
	overlap := 200
	if size <= overlap {
		overlap = size / 4
	}
	step := size - overlap
	if step < 1 {
		step = 1
	}
	r := []rune(s)
	if len(r) <= size {
		return []string{s}
	}
	var out []string
	for start := 0; start < len(r); start += step {
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

// chat 调 SiliconFlow chat/completions。
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
	if l.chatHook != nil {
		return l.chatHook(prompt)
	}
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
