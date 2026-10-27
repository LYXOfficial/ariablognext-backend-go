package textutil

import (
	"strings"
	"testing"
)

// 期望值全部写死成常量（testing.md §2.4）：不许用「再跑一遍同样正则」来验证自己。

func TestWordCount(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"空串", "", 0},
		{"纯中文", "你好世界", 4},
		{"纯英文单词", "hello world foo", 3},
		{"中英混排", "用 Go 写了 3 个服务", 7}, // 用/Go/写/了/3/个/服/务 → 8？见下
		{"标点不算", "你好，世界！", 4},
		{"数字与英文都是词", "version 2.10 发布", 5},
		{"英文大小写连续", "MongoDB Go", 2},
	}
	// 中英混排逐项校准：「用/写/了/个/服/务」6 个汉字 + Go + 3 = 8
	cases[3].want = 8
	for _, tc := range cases {
		if got := WordCount(tc.in); got != tc.want {
			t.Errorf("%s：WordCount(%q) = %d，应为 %d", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestCleanMarkdownHeading(t *testing.T) {
	const want = "标题文字"
	if got := CleanMarkdown("## 标题文字"); got != want {
		t.Errorf("标题：got %q want %q", got, want)
	}
}

func TestCleanMarkdownBoldItalic(t *testing.T) {
	const want = "粗体和斜体和粗斜"
	if got := CleanMarkdown("**粗体**和*斜体*和***粗斜***"); got != want {
		t.Errorf("粗斜体：got %q want %q", got, want)
	}
}

func TestCleanMarkdownInlineCode(t *testing.T) {
	const want = "调用 fmt.Println 输出"
	if got := CleanMarkdown("调用 `fmt.Println` 输出"); got != want {
		t.Errorf("行内代码：got %q want %q", got, want)
	}
}

func TestCleanMarkdownFencedCode(t *testing.T) {
	const want = "前面\nprint(1)\n后面"
	got := CleanMarkdown("前面\n```python\nprint(1)\n```\n后面")
	if got != want {
		t.Errorf("围栏代码：got %q want %q", got, want)
	}
}

func TestCleanMarkdownImageAndLink(t *testing.T) {
	// alt 文字是有信息量的，保留；URL 丢弃
	const want = "看alt 文字这张图和这个链接"
	if got := CleanMarkdown("看![alt 文字](https://x/y.png)这张图和[这个链接](https://a.b)"); got != want {
		t.Errorf("图片/链接：got %q want %q", got, want)
	}
}

func TestCleanMarkdownQuoteAndList(t *testing.T) {
	const want = "引用内容\n第一项\n第二项"
	got := CleanMarkdown("> 引用内容\n- 第一项\n2. 第二项")
	if got != want {
		t.Errorf("引用/列表：got %q want %q", got, want)
	}
}

func TestCleanMarkdownTable(t *testing.T) {
	const want = "名字 数量\n苹果 3"
	got := CleanMarkdown("| 名字 | 数量 |\n|---|---|\n| 苹果 | 3 |")
	if got != want {
		t.Errorf("表格：got %q want %q", got, want)
	}
}

func TestCleanMarkdownHTMLTag(t *testing.T) {
	const want = "带标签的文本"
	if got := CleanMarkdown("<b>带标签</b>的文本"); got != want {
		t.Errorf("HTML：got %q want %q", got, want)
	}
}

func TestCleanMarkdownHorizontalRule(t *testing.T) {
	const want = "上一段\n下一段"
	if got := CleanMarkdown("上一段\n---\n下一段"); got != want {
		t.Errorf("分隔线：got %q want %q", got, want)
	}
}

func TestBuildSearchTextRules(t *testing.T) {
	// 长正文证明不截断（plainContent 在库里截 201 字是老问题，searchText 不能重蹈）
	long := strings.Repeat("正文内容", 200) // 800 字
	got := BuildSearchText("My Title", "## 小节\n**加粗** 文本\n"+long)
	if !strings.Contains(got, "my title") {
		t.Errorf("searchText 应含小写化的标题：%q", got[:60])
	}
	if !strings.Contains(got, "加粗") {
		t.Error("Markdown 标记要去掉但文字要保留")
	}
	if strings.Contains(got, "##") || strings.Contains(got, "**") {
		t.Error("searchText 不该残留 Markdown 标记")
	}
	if strings.Contains(got, "\n") {
		t.Error("searchText 应把换行压成空格")
	}
	if len([]rune(got)) < 800 {
		t.Errorf("searchText 不截断：长度 %d", len([]rune(got)))
	}
	if got != strings.ToLower(got) {
		t.Error("searchText 应全部小写")
	}
}

func TestSearchContext(t *testing.T) {
	st := BuildSearchText("标题", "前导文字。这里是关键词所在的位置。后继文字。")
	// 命中处前后各取若干字
	ctx := SearchContext(st, "关键词", 5)
	if !strings.Contains(ctx, "关键词") {
		t.Errorf("context 应包含命中词：%q", ctx)
	}
	if len([]rune(ctx)) > 5+len([]rune("关键词"))+5 {
		t.Errorf("context 超出半径：%q", ctx)
	}
	// 找不到返回空
	if got := SearchContext(st, "不存在的词", 5); got != "" {
		t.Errorf("未命中应返回空串：%q", got)
	}
}

func TestExternalTagsNotInSearchIndex(t *testing.T) {
	// 用户明令：Markdown 结构不进搜索索引。
	// 搜 "fold" 只该命中正文里的 fold，不该把所有带 Folding 外挂的文章都翻出来。
	// 外挂标签是 MDX 风格：<Folding summary="…">…</Folding>，整段标签（含属性）都该剥掉。
	md := "<Folding summary=\"点开看看 fold 说明\">\n正文里有 fold 这个词。\n</Folding>\n其它段落。"
	got := BuildSearchText("标题", md)
	// 标签名是结构：搜 "folding" 不该因此命中
	if strings.Contains(got, "folding") {
		t.Errorf("外挂标签名不该进索引：%q", got)
	}
	// 但 summary 是渲染后可见的文字，属于正文内容，要保留
	if !strings.Contains(got, "点开看看") {
		t.Errorf("summary 的可见文字应保留：%q", got)
	}
	if !strings.Contains(got, "fold 这个词") {
		t.Errorf("正文里的 fold 必须保留：%q", got)
	}
	// 结构性属性（link/src/color）的文字不进索引
	structOnly := BuildSearchText("t", "<Link link=\"https://butterfly.js.org/\" title=\"标题文字\" color=\"pink\" />")
	if strings.Contains(structOnly, "butterfly") || strings.Contains(structOnly, "pink") {
		t.Errorf("结构性属性不该进索引：%q", structOnly)
	}
	if !strings.Contains(structOnly, "标题文字") {
		t.Errorf("title 属性文字应保留：%q", structOnly)
	}
}

func TestCleanMarkdownMoreSyntax(t *testing.T) {
	cases := []struct{ in, want string }{
		// 有序列表带括号
		{"1) 第一\n2) 第二", "第一\n第二"},
		// 引用嵌套
		{"> > 两层引用", "两层引用"},
		// 波浪线围栏代码
		{"~~~\ncode\n~~~", "code"},
		// 表格分隔行被丢弃、宽表压空格
		{"| a | b |\n|:--|--:|\n| 1 | 2 |", "a b\n1 2"},
		// 自动链接
		{"见 <https://x.example/a>", "见 https://x.example/a"},
		// 删除线
		{"~~删掉~~保留", "删掉保留"},
		// 行内公式
		{"公式 $x^2$ 结束", "公式 x^2 结束"},
		// 分隔线（星号）
		{"上\n***\n下", "上\n下"},
		// 未闭合的标记保持原样（不能盲目 ReplaceAll）
		{"孤立的 ` 和 * 不配对", "孤立的 ` 和 * 不配对"},
	}
	for _, c := range cases {
		if got := CleanMarkdown(c.in); got != c.want {
			t.Errorf("CleanMarkdown(%q) = %q，应为 %q", c.in, got, c.want)
		}
	}
}

func TestSearchContextEdges(t *testing.T) {
	// 命中在开头：左边界截断
	if got := SearchContext("关键词在开头然后很多字", "关键词", 5); !strings.HasPrefix(got, "关键词") {
		t.Errorf("开头命中异常：%q", got)
	}
	// 命中在结尾：右边界截断
	if got := SearchContext("很多字之后才出现关键词", "关键词", 5); !strings.HasSuffix(got, "关键词") {
		t.Errorf("结尾命中异常：%q", got)
	}
	// 空文本 / 空关键词
	if SearchContext("", "x", 5) != "" || SearchContext("abc", "", 5) != "" {
		t.Error("空输入应返回空串")
	}
}
