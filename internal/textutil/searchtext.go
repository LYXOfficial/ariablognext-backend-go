package textutil

import (
	"strings"
	"unicode"
)

// BuildSearchText 生成搜索索引字段：标题 + 全文纯文本。
// 规则（api-draft §3.1.1 / design-notes §2.5）：
//   - 全部小写（查询侧用不区分大小写的正则，这里是双保险，也省一次 LOWER 运算）；
//   - 去 Markdown 标记与 HTML 标签；
//   - 连续空白（含换行）压成单空格——否则「关键词在换行两侧断开」的查询会漏配；
//   - 不截断（库里 plainContent 被截断到 201 字是历史包袱，searchText 是新字段不做）。
func BuildSearchText(title, mdContent string) string {
	plain := CleanMarkdown(title + "\n" + mdContent)
	var b strings.Builder
	b.Grow(len(plain))
	lastWasSpace := true // 开头的空格不进索引
	for _, r := range plain {
		if unicode.IsSpace(r) {
			if !lastWasSpace {
				b.WriteByte(' ')
				lastWasSpace = true
			}
			continue
		}
		lastWasSpace = false
		b.WriteRune(unicode.ToLower(r))
	}
	return strings.TrimRight(b.String(), " ")
}

// SearchContext 从 searchText 里取关键词命中处前后各 radius 字的片段。
// keyword 必须已经是小写字面量（调用方负责 ToLower；这里不做正则）。
// 找不到命中返回空串——由调用方决定是否带 context 字段。
func SearchContext(searchText, keyword string, radius int) string {
	if searchText == "" || keyword == "" {
		return ""
	}
	idx := strings.Index(searchText, strings.ToLower(keyword))
	if idx < 0 {
		return ""
	}
	runes := []rune(searchText)
	// idx 是字节下标，rune 切片要换算；找不到就用整个文本（极端情况：多字节截断）
	pos := runeIndex(searchText, idx)
	if pos < 0 {
		return ""
	}
	start := pos - radius
	if start < 0 {
		start = 0
	}
	end := pos + len([]rune(keyword)) + radius
	if end > len(runes) {
		end = len(runes)
	}
	return string(runes[start:end])
}

func runeIndex(s string, byteIdx int) int {
	if byteIdx >= len(s) {
		return -1
	}
	count := 0
	for i := range s {
		if i == byteIdx {
			return count
		}
		count++
	}
	return count
}
