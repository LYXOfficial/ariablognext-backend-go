package textutil

import (
	"strings"
	"testing"
)

// 用户场景：查询比命中文本长，应能靠部分匹配找到。
func TestScoreKeywordPartialMatch(t *testing.T) {
	text := BuildSearchText("哈基米之歌", "今天刷到哈基米，南北绿豆的梗真是笑死。")
	m := ScoreKeyword(text, "哈基米哦南北绿豆")
	if !m.Matched {
		t.Fatalf("应命中（正文含「哈基米」）：%+v", m)
	}
	if m.Exact {
		t.Error("这不该算精确命中")
	}
	if m.Weight < minFuzzyWeight {
		t.Errorf("权重应达阈值：%+v", m)
	}
}

// 反向约束：搜「网站」不能命中只有「网」「站」分居两处的文章。
func TestScoreKeywordRejectsSplitChars(t *testing.T) {
	text := BuildSearchText("杂谈", "网络很快，站点很多，但这里没有那个词。")
	if m := ScoreKeyword(text, "网站"); m.Matched {
		t.Fatalf("不该命中：单字相邻但不构成「网站」%+v", m)
	}
}

// 精确命中优先级最高。
func TestScoreKeywordExact(t *testing.T) {
	text := BuildSearchText("网站维护", "今天维护了网站。")
	m := ScoreKeyword(text, "网站")
	if !m.Matched || !m.Exact || m.Weight != exactWeight {
		t.Fatalf("应精确命中：%+v", m)
	}
	if m.Snippet != "网站" {
		t.Errorf("精确命中的片段应为 query 本身：%q", m.Snippet)
	}
}

// 只命中一个常见双字组不足以通过（避免「南北」把不相关文章带进来）。
func TestScoreKeywordSingleBigramTooWeak(t *testing.T) {
	text := BuildSearchText("出行", "这次南北方向都跑了一趟。")
	if m := ScoreKeyword(text, "南北绿豆"); m.Matched {
		t.Fatalf("单个双字组不该通过阈值：%+v", m)
	}
}

// 预过滤正则：中文 n-gram 直出，英文整词用词边界（避免 fiber 命中 fibery）。
func TestFuzzyRegexPattern(t *testing.T) {
	p := FuzzyRegexPattern("哈基米fiber")
	if p == "" {
		t.Fatal("应生成候选正则")
	}
	if !contains(p, "哈基米") {
		t.Errorf("应含中文三元组：%s", p)
	}
	if !contains(p, `\bfiber\b`) {
		t.Errorf("英文词应带词边界：%s", p)
	}
}

// 英文整词语义：fib 不能命中 fiber，fiber 不能命中 fibery。
func TestEnglishWholeWordOnly(t *testing.T) {
	text := BuildSearchText("框架", "这里讲 fiber 框架。")
	if ContainsTerm(text, "fib") {
		t.Error("fib 不该命中 fiber（英文是整词匹配）")
	}
	if !ContainsTerm(text, "fiber") {
		t.Error("fiber 应命中 fiber")
	}
	if m := ScoreKeyword(text, "fib"); m.Matched {
		t.Errorf("fib 连部分匹配也不该通过（英文不做子串）：%+v", m)
	}
	if m := ScoreKeyword(text, "fiberv3"); m.Matched {
		t.Errorf("fiberv3 是另一个词，不该命中 fiber：%+v", m)
	}
}

// 中英混排：中文子串 + 英文整词，两者都满足才算命中。
func TestMixedQueryRules(t *testing.T) {
	text := BuildSearchText("Fiber 部署", "fiber 部署到 vercel 的笔记。")
	if !ContainsTerm(text, "fiber 部署") {
		t.Error("中文子串 + 英文整词都应满足")
	}
	if ContainsTerm(text, "fibe 部署") {
		t.Error("英文部分拼错（fibe）不该命中")
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && (len(s) >= len(sub)) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// 别名支持正则：年份这类「概念词」由配置提供规则，不在代码里写死。
func TestAliasRegexMatch(t *testing.T) {
	text := BuildSearchText("2023 年终总结", "2023 年发生了很多事，2024 年继续加油。")
	m, w, snippet := AliasMatch(text, `/(19|20)\d{2}\s*年/`)
	if !m {
		t.Fatal("正则别名应命中正文里的年份")
	}
	if w < 2 {
		t.Errorf("命中越多权重应越高：%d", w)
	}
	if !strings.Contains(snippet, "2023") {
		t.Errorf("片段应含命中的年份：%q", snippet)
	}
	// 没有年份的正文不该命中
	if m, _, _ := AliasMatch(BuildSearchText("杂谈", "今天天气不错"), `/(19|20)\d{2}年/`); m {
		t.Error("无年份不该命中")
	}
}

// 普通词别名走语言规则（英文整词）。
func TestAliasPlainTerm(t *testing.T) {
	text := BuildSearchText("图床", "用 R2 存图片。")
	if m, _, _ := AliasMatch(text, "R2"); !m {
		t.Error("别名 R2 应命中")
	}
	if m, _, _ := AliasMatch(text, "r"); m {
		t.Error("单字母不该命中")
	}
}
