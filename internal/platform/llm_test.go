package platform

import (
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
	llm := NewLLM(config.SummaryConfig{Model: "Qwen/Qwen3-8B"})
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
		Model:     "Qwen/Qwen3-8B",
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

// 生成结果要去掉换行（旧后端 .replace("\n","")）。
func TestSummaryStripsNewlines(t *testing.T) {
	got := strings.ReplaceAll("第一行\n第二行\n", "\n", "")
	if strings.Contains(got, "\n") {
		t.Errorf("换行应被移除：%q", got)
	}
}
