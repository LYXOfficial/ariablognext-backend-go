// Package textutil 是纯文本处理：剥 Markdown、生成 searchText、中英字数统计。
// 无状态、无 IO，方便单测覆盖到每一种语法。
package textutil

import "strings"

// WordCount 统计「中文字符 + 英文单词」的中英混合字数。
// 规则：连续的 CJK 字符每个算 1；连续的 ASCII 字母/数字串算 1 个词；
// 空白与标点不计。这与旧前端的字数展示语义一致（一篇 1830 字的文章）。
// wordCount 是给读者看的规模感，不是语言学精确计数——保持简单可复现。
func WordCount(text string) int {
	if text == "" {
		return 0
	}
	count := 0
	inWord := false
	for _, r := range text {
		switch {
		case isCJK(r):
			count++
			inWord = false
		case isASCIILetterOrDigit(r):
			if !inWord {
				count++
				inWord = true
			}
		default:
			inWord = false
		}
	}
	return count
}

func isCJK(r rune) bool {
	// CJK 统一表意文字 + 扩展 A + 兼容表意区；够覆盖中文写作场景
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0xF900 && r <= 0xFAFF)
}

func isASCIILetterOrDigit(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// CleanMarkdown 把 Markdown 源文剥成纯文本（plainContent 用）。
// 保留文字内容（含代码块内容与链接文字），去掉语法标记；
// 输出按行拼接、去多余空行——plainContent 在库里本来就是给人扫一眼的摘要形态。
// ⚠️ 期望输出全部写死在测试常量里（testing.md §2.4），不要用「再跑一遍同样正则」验证。
func CleanMarkdown(md string) string {
	if md == "" {
		return ""
	}
	lines := strings.Split(md, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	fenceMarker := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// 围栏代码块：``` 或 ~~~ 开头。围栏内的内容原样保留（代码也是正文），
		// 只去掉围栏行本身。
		if isFence(trimmed) {
			if !inFence {
				inFence = true
				fenceMarker = trimmed[:3]
			} else if strings.HasPrefix(trimmed, fenceMarker) {
				inFence = false
				fenceMarker = ""
			}
			continue
		}
		if inFence {
			out = append(out, line)
			continue
		}

		if cleaned := cleanLine(trimmed); cleaned != "" {
			out = append(out, cleaned)
		}
	}
	return strings.Join(out, "\n")
}

func isFence(line string) bool {
	return strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~")
}

// cleanLine 处理单行 Markdown：按「从外到内」的顺序剥标记。
// 顺序很重要——先块级（标题/引用/列表/表格/分隔线）再行内（图片/链接/粗斜体/代码/HTML），
// 反了会把 `#` 当正文一部分留下来。
func cleanLine(line string) string {
	// 分隔线（*** / --- / ___，至少三个）
	if isHorizontalRule(line) {
		return ""
	}
	// 标题
	if n := countLeading(line, '#'); n > 0 && n <= 6 && len(line) > n && line[n] == ' ' {
		line = strings.TrimSpace(line[n+1:])
	}
	// 引用（可嵌套 > > >）
	for strings.HasPrefix(line, "> ") || line == ">" {
		line = strings.TrimSpace(strings.TrimPrefix(line, "> "))
		if line == ">" {
			line = ""
		}
	}
	// 无序列表（- * +）
	for len(line) > 0 && (line[0] == '-' || line[0] == '*' || line[0] == '+') {
		if len(line) > 1 && line[1] == ' ' {
			line = strings.TrimSpace(line[2:])
			continue
		}
		break
	}
	// 有序列表（1. 2)）
	if i := strings.IndexAny(line, ".)"); i > 0 && i <= 3 && allDigits(line[:i]) && len(line) > i+1 && line[i+1] == ' ' {
		line = strings.TrimSpace(line[i+2:])
	}
	// 表格分隔行：以 | 开头且只由 | : - 与空格组成（含 |:--|--:| 这种短横线写法）
	if strings.HasPrefix(line, "|") && isTableSeparator(line) {
		return ""
	}
	if strings.HasPrefix(line, "|") && strings.Contains(line, "---") {
		return tableRow(line)
	}
	// 表格内容行：竖线当空格，再压掉由对齐产生的多空格
	if strings.HasPrefix(line, "|") {
		line = strings.Join(strings.Fields(strings.ReplaceAll(line, "|", " ")), " ")
	}

	line = stripInline(line)
	// 空行与多余空白：plainContent 是摘要形态，不保留装饰性空白
	return strings.TrimSpace(line)
}

// isTableSeparator 判断「表格分隔行」：只有 |、:、- 和空格。
func isTableSeparator(line string) bool {
	if !strings.Contains(line, "-") {
		return false
	}
	for _, r := range line {
		if r != '|' && r != ':' && r != '-' && r != ' ' {
			return false
		}
	}
	return true
}

func isHorizontalRule(line string) bool {
	for _, marker := range []string{"---", "***", "___"} {
		if len(line) >= 3 && strings.Trim(line, marker[:1]) == "" && strings.Contains(line, marker[:1]) {
			// 全部由同一标记字符组成（允许空格）
			compacted := strings.ReplaceAll(line, " ", "")
			if len(compacted) >= 3 && strings.Trim(compacted, marker[:1]) == "" {
				return true
			}
		}
	}
	return false
}

func countLeading(s string, c byte) int {
	n := 0
	for n < len(s) && s[n] == c {
		n++
	}
	return n
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// tableRow 把表格行剥成纯文本；分隔行（---）直接丢弃。
func tableRow(line string) string {
	if strings.Trim(line, "|-: ") == "" {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(line, "|", " "))
}

// stripInline 处理行内语法。图片先于链接处理（图片语法 ![]() 是链接的超集，
// 顺序反了会把 alt 文本和 URL 一起留下）。
func stripInline(s string) string {
	// 图片 ![alt](url) → alt
	for {
		start := strings.Index(s, "![")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "](")
		if end < 0 {
			break
		}
		close := strings.Index(s[start+end+2:], ")")
		if close < 0 {
			break
		}
		alt := s[start+2 : start+end]
		s = s[:start] + alt + s[start+end+2+close+1:]
	}
	// 链接 [text](url) → text
	for {
		start := strings.Index(s, "[")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "](")
		if end < 0 {
			break
		}
		close := strings.Index(s[start+end+2:], ")")
		if close < 0 {
			break
		}
		text := s[start+1 : start+end]
		s = s[:start] + text + s[start+end+2+close+1:]
	}
	// 自动链接 <https://…> → https://…
	for {
		start := strings.Index(s, "<http")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], ">")
		if end < 0 {
			break
		}
		s = s[:start] + s[start+1:start+end] + s[start+end+1:]
	}
	// 粗斜体 *** → ** → *（先长后短）
	s = replaceAllPairs(s, "***")
	s = replaceAllPairs(s, "**")
	s = replaceAllPairs(s, "*")
	s = replaceAllPairs(s, "_")
	// 行内代码 `code`：反引号内容原样保留、标记去掉
	s = replaceAllPairs(s, "`")
	// 行内公式 $x$：保留公式内容
	s = replaceAllPairs(s, "$")
	// 删除线 ~~text~~
	s = replaceAllPairs(s, "~~")
	// HTML 标签：<br> <b>…</b> 之类整体删除（正文里的尖括号代码场景已由代码块/行内码先行处理）
	s = removeHTMLTags(s)
	return s
}

// replaceAllPairs 去掉成对出现的标记（成对剥除，不是盲目 ReplaceAll——
// 盲目替换会把孤立的 ` 或 * 也吃掉，破坏正文）。
func replaceAllPairs(s, marker string) string {
	for {
		start := strings.Index(s, marker)
		if start < 0 {
			return s
		}
		end := strings.Index(s[start+len(marker):], marker)
		if end < 0 {
			return s
		}
		inner := s[start+len(marker) : start+len(marker)+end]
		s = s[:start] + inner + s[start+len(marker)+end+len(marker):]
	}
}

// visibleTextAttrs 是「渲染后会作为文字显示」的属性：值本身是正文，必须保留。
// 其余属性（link/src/color/class/style/open…）是结构，丢弃。
var visibleTextAttrs = []string{"title", "summary"}

// removeHTMLTags 处理 MDX/HTML 标签：
//   - 标签名、URL、结构性属性全部丢弃（所以搜 "fold" 不会被 <Folding> 这个标签名命中）；
//   - title=/summary= 的值属于**可见文字**（如 <Link title="某站支持部署至服务器">），保留成正文。
func removeHTMLTags(s string) string {
	for {
		start := strings.IndexByte(s, '<')
		if start < 0 {
			return s
		}
		end := strings.IndexByte(s[start:], '>')
		if end < 0 {
			return s
		}
		tag := s[start : start+end+1]
		text := attrText(tag)
		repl := text
		if repl != "" {
			repl = " " + repl + " "
		}
		s = s[:start] + repl + s[start+end+1:]
	}
}

// attrText 提取标签里可见属性的文字值（tag 含尖括号）。
func attrText(tag string) string {
	var parts []string
	for _, name := range visibleTextAttrs {
		for _, quote := range []string{`"`, `'`} {
			marker := name + "=" + quote
			idx := strings.Index(tag, marker)
			if idx < 0 {
				continue
			}
			rest := tag[idx+len(marker):]
			if end := strings.Index(rest, quote); end > 0 {
				if v := strings.TrimSpace(rest[:end]); v != "" {
					parts = append(parts, v)
				}
			}
		}
	}
	return strings.Join(parts, " ")
}
