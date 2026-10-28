package platform

import (
	"context"
	"strings"
	"testing"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
)

// oldBackendPrompt 是旧 Python 后端（ariablognext-backend/get/post/main.py
// 的 _request_summary）里的 prompt 原文。**改动它就是改产品行为**：
// 这段文字决定了 Aria 的人设、篇幅、敏感内容处理方式，必须逐字一致。
const oldBackendPrompt = "你是一位专业的作家，接下来请用精炼并平易近人而并不高深的语言，尽可能更可爱且女性化一些，但尽量直截了当描述，不要做作，使用Aria作为作者的自称，写一份一百字左右的文章总结，态度要与作者表达的所保持一致，尽可能不要有幻觉，遇到你所不确定的部分和敏感的（政治或是较负面情绪等内容）请略过而不要回答，而遇到一些晦涩的东西可以尝试解释，接下来给你的文章是markdown格式的，请输出纯文本并使用中文回答我，不要输出多余的内容，只输出文章摘要的正文，不要输出换行符和在编程语言中的非法文字。接下来是文章的正文内容：\n"

// 守卫：常量必须与旧后端 prompt 逐字相同（含末尾换行）。
func TestSummaryPromptMatchesOldBackend(t *testing.T) {
	if summaryPromptPrefix != oldBackendPrompt {
		t.Fatalf("摘要 prompt 与旧后端不一致！\n旧: %q\n新: %q", oldBackendPrompt, summaryPromptPrefix)
	}
}

// 请求体形状与旧后端一致：单条 user 消息、没有 system 消息。
func TestChatBodyShape(t *testing.T) {
	llm := NewLLM(config.SummaryConfig{Enabled: true, APIKey: "test-key", Model: "Qwen/Qwen3-8B"})
	body := llm.chatBody("测试正文")
	msgs, ok := body["messages"].([]map[string]string)
	if !ok || len(msgs) != 1 {
		t.Fatalf("应只有一条消息：%v", body["messages"])
	}
	if msgs[0]["role"] != "user" {
		t.Errorf("唯一消息的 role 应为 user（旧后端没有 system 消息），得到 %q", msgs[0]["role"])
	}
	if msgs[0]["content"] != "测试正文" {
		t.Errorf("user 内容应原样透传：%q", msgs[0]["content"])
	}
	if _, ok := body["max_tokens"]; ok {
		t.Error("旧后端不传 max_tokens，代码不该自带默认值")
	}
	if body["model"] != "Qwen/Qwen3-8B" {
		t.Errorf("model 应来自配置：%v", body["model"])
	}
}

// 模型专属参数由配置提供并覆盖/追加（Qwen3 的 enable_thinking=false 就是这样进来的）。
func TestChatBodyExtraBody(t *testing.T) {
	llm := NewLLM(config.SummaryConfig{
		Enabled: true, APIKey: "test-key", Model: "Qwen/Qwen3-8B",
		ExtraBody: map[string]any{"enable_thinking": false, "max_tokens": 400},
	})
	body := llm.chatBody("测试正文")
	if body["enable_thinking"] != false {
		t.Errorf("extraBody 应进入请求体：%v", body)
	}
	if body["max_tokens"] != 400 {
		t.Errorf("extraBody 应能补充参数：%v", body["max_tokens"])
	}
}

// 分块路径：每一块与最后的合并，都必须使用同一段旧 prompt（不得新增指令文本）。
func TestChunkingReusesSamePrompt(t *testing.T) {
	llm := NewLLM(config.SummaryConfig{Enabled: true, APIKey: "test-key", Model: "Qwen/Qwen3-8B", ChunkSize: 1000, MaxChunks: 3})
	var prompts []string
	llm.chatHook = func(p string) (string, error) {
		prompts = append(prompts, p)
		return "块摘要", nil
	}
	// 300 字符 → 至少 4 块（重叠 200 会更多），但只处理 MaxChunks=3 块 + 1 次合并
	md := strings.Repeat("中", 3000)
	if _, err := llm.GenerateSummary(context.Background(), "标题不进 prompt", md); err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 4 {
		t.Fatalf("3 块 + 1 次合并 = 4 次调用，实际 %d", len(prompts))
	}
	for i, p := range prompts {
		if !strings.HasPrefix(p, summaryPromptPrefix) {
			t.Errorf("第 %d 次调用的 prompt 不以旧 prompt 开头：%q", i+1, p[:40])
		}
		// 除 prompt 前缀外只能是正文/块摘要，不能出现新造的指令
		if strings.Contains(p, "请合并") || strings.Contains(p, "请概括") || strings.Contains(p, "片段") {
			t.Errorf("第 %d 次调用混入了新造指令：%q", i+1, p[len(summaryPromptPrefix):])
		}
		if strings.Contains(p, "标题") {
			t.Errorf("prompt 不应包含标题（旧后端不传）：%q", p)
		}
	}
	if !strings.HasPrefix(prompts[3], summaryPromptPrefix+"块摘要\n块摘要\n块摘要") {
		t.Errorf("合并请求应是把各块摘要当正文再喂同一 prompt：%q", prompts[3])
	}
}

// 短文只有一块：只调用一次。
func TestShortArticleSingleCall(t *testing.T) {
	llm := NewLLM(config.SummaryConfig{Enabled: true, APIKey: "test-key", Model: "Qwen/Qwen3-8B"})
	calls := 0
	llm.chatHook = func(p string) (string, error) { calls++; return "摘要", nil }
	if _, err := llm.GenerateSummary(context.Background(), "", "短正文"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("短文应只调用一次，实际 %d", calls)
	}
}

// 换行必须去掉（旧后端 .replace("\n","")）。
func TestSummaryStripsNewlines(t *testing.T) {
	llm := NewLLM(config.SummaryConfig{Enabled: true, APIKey: "test-key", Model: "Qwen/Qwen3-8B"})
	llm.chatHook = func(string) (string, error) { return "第一行\n第二行", nil }
	out, err := llm.GenerateSummary(context.Background(), "", "正文")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\n") {
		t.Errorf("换行应被移除：%q", out)
	}
}

// chunkSize 配得比重叠量还小时不能 panic（配置化之后新增的边界）。
func TestSplitChunksTinySize(t *testing.T) {
	for _, size := range []int{0, 1, 10, 200, 201} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("splitChunks(size=%d) panic: %v", size, r)
				}
			}()
			got := splitChunks(strings.Repeat("中", 500), size)
			if size > 0 && len(got) == 0 {
				t.Errorf("size=%d 应至少切出一块", size)
			}
		}()
	}
}
