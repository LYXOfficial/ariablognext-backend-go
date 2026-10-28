package textutil

import (
	"regexp"
	"strings"
	"sync"
	"unicode"
)

// 关键词匹配规则（2026-10-28 用户定）：
//
//	中文：允许子串匹配——「哈基米哦南北绿豆」能命中正文只有「哈基米」的文章，
//	      因为中文没有空格，语义单位是字/词组；
//	英文：整词匹配 + 拼写容错——英文语义单位是空格分隔的单词，
//	      「fib」不能命中「fiber」，但「verce」能命中「vercel」（编辑距离 1）。
//
// 两级策略：先精确（按上面的语言规则），不中再退到部分匹配：
//   - 中文 n-gram（三元组权重高；双字组需稀有或累计达标）；
//   - 英文整词 / 拼写近似词。
//
// 别名与概念词**不写死在代码里**：配置 search.synonyms 提供「查询词 → 别名列表」，
// 别名支持 /正则/ 形式（例如 年份: ['/(19|20)\d{2}年/']），由配置维护规则。

const (
	// minFuzzyWeight 是部分匹配的最低权重。
	minFuzzyWeight = 3
	// exactWeight 让精确命中排在任何部分匹配之前。
	exactWeight = 10000
)

// KeywordMatch 是一次匹配的结果。
type KeywordMatch struct {
	Matched bool
	Exact   bool
	Weight  int
	// Snippet 用于取 context 片段：精确命中为命中的词，部分命中为命中的那个 n-gram。
	Snippet string
}

// ---------------- 查询解析 ----------------

// queryParts 把查询切成「中文片段」与「英文词」两类。
// 英文词 = 连续的字母/数字/_/./-（next.js、bge-m3、go.mod 都是一个词）。
type queryParts struct {
	cjk   []string
	words []string
}

func splitQueryParts(query string) queryParts {
	var p queryParts
	var cjk, word []rune
	flushCJK := func() {
		if len(cjk) > 0 {
			p.cjk = append(p.cjk, string(cjk))
			cjk = cjk[:0]
		}
	}
	flushWord := func() {
		if len(word) > 0 {
			p.words = append(p.words, string(word))
			word = word[:0]
		}
	}
	for _, r := range query {
		switch {
		case isASCIIWordRune(r):
			flushCJK()
			word = append(word, r)
		case queryIsCJK(r):
			flushWord()
			cjk = append(cjk, r)
		default:
			flushCJK()
			flushWord()
		}
	}
	flushCJK()
	flushWord()
	return p
}

func isASCIIWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-'
}

func queryIsCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || (r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x3000 && r <= 0x303F) || (r >= 0xFF00 && r <= 0xFFEF)
}

// asciiWords 从文本里抽出英文词集合（文本应已小写）。
func asciiWords(text string) map[string]struct{} {
	out := map[string]struct{}{}
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out[string(cur)] = struct{}{}
			cur = cur[:0]
		}
	}
	for _, r := range text {
		if isASCIIWordRune(r) {
			cur = append(cur, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func normalizeKeyword(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// ---------------- 精确匹配 ----------------

// ContainsTerm 判断文本是否包含一个查询词，按语言规则：
// 英文整词（词集合里有它），中文子串。
func ContainsTerm(text, term string) bool {
	text = normalizeKeyword(text)
	term = normalizeKeyword(term)
	parts := splitQueryParts(term)
	if len(parts.words) == 0 && len(parts.cjk) == 0 {
		return strings.Contains(text, term) // 纯符号查询
	}
	if len(parts.words) > 0 {
		words := asciiWords(text)
		for _, w := range parts.words {
			if _, ok := words[w]; !ok {
				return false
			}
		}
	}
	for _, c := range parts.cjk {
		if !strings.Contains(text, c) {
			return false
		}
	}
	return true
}

// ---------------- 打分 ----------------

// ScoreKeyword 按「精确 → 部分匹配」给文本打分（不含语料统计，用于单测/简化场景）。
func ScoreKeyword(text, query string) KeywordMatch {
	return ScoreKeywordStats(text, query, nil)
}

// ScoreKeywordStats 是完整实现：精确命中、中文 n-gram、英文整词/拼写容错，
// 并用语料统计（st）判断双字组是否足够稀有（稀有双字组单独即可命中）。
func ScoreKeywordStats(text, query string, st *KeywordStats) KeywordMatch {
	text = normalizeKeyword(text)
	query = normalizeKeyword(query)
	if query == "" {
		return KeywordMatch{Matched: true}
	}
	if ContainsTerm(text, query) {
		return KeywordMatch{Matched: true, Exact: true, Weight: exactWeight, Snippet: bestExactSnippet(query)}
	}

	tri, bi, words := ngramsOf(query)
	weight := 0
	best := ""
	consider := func(ng string, w int) {
		if ng == "" || !strings.Contains(text, ng) {
			return
		}
		weight += w
		if len([]rune(ng)) > len([]rune(best)) {
			best = ng
		}
	}

	triHits := 0
	for _, ng := range tri {
		if strings.Contains(text, ng) {
			triHits++
			consider(ng, 3)
		}
	}
	biHits, rareBiHits := 0, 0
	for _, ng := range bi {
		if !strings.Contains(text, ng) {
			continue
		}
		biHits++
		consider(ng, 1)
		if st != nil && st.DF[ng] <= st.rareBigramMaxDF() {
			rareBiHits++
		}
	}

	pool := asciiWords(text)
	for _, w := range words {
		if _, ok := pool[w]; ok {
			weight += 3
			if len([]rune(w)) > len([]rune(best)) {
				best = w
			}
			continue
		}
		for cand := range pool {
			if IsTypoMatch(w, cand) {
				weight += 3 // 拼写错误与整词同权：单个错拼词就足以命中
				if len([]rune(cand)) > len([]rune(best)) {
					best = cand
				}
				break
			}
		}
	}

	matched := triHits > 0 || rareBiHits > 0 || weight >= minFuzzyWeight
	return KeywordMatch{Matched: matched, Weight: weight, Snippet: best}
}

// bestExactSnippet 精确命中时用于取 context 的片段：取查询里最长的那一段。
func bestExactSnippet(query string) string {
	parts := splitQueryParts(query)
	best := ""
	upd := func(s string) {
		if len([]rune(s)) > len([]rune(best)) {
			best = s
		}
	}
	for _, c := range parts.cjk {
		upd(c)
	}
	for _, w := range parts.words {
		upd(w)
	}
	if best == "" {
		return normalizeKeyword(query)
	}
	return best
}

// ---------------- 语料统计（稀有度） ----------------

// KeywordStats 是一次查询的语料统计。N 是语料文档总数，DF 是各 n-gram/词的文档频率。
type KeywordStats struct {
	N  int
	DF map[string]int
}

// BuildStats 统计查询涉及的 n-gram 在语料里的文档频率。
func BuildStats(texts []string, query string) *KeywordStats {
	st := &KeywordStats{N: len(texts), DF: map[string]int{}}
	tri, bi, words := ngramsOf(query)
	need := map[string]struct{}{}
	for _, g := range tri {
		need[g] = struct{}{}
	}
	for _, g := range bi {
		need[g] = struct{}{}
	}
	for _, w := range words {
		need[w] = struct{}{}
	}
	for _, t := range texts {
		t = normalizeKeyword(t)
		for g := range need {
			if strings.Contains(t, g) {
				st.DF[g]++
			}
		}
	}
	return st
}

// ngramsOf 拆出查询的中文三元组、双字组与英文词。
func ngramsOf(query string) (tri, bi, words []string) {
	parts := splitQueryParts(normalizeKeyword(query))
	for _, seg := range parts.cjk {
		r := []rune(seg)
		for i := 0; i+2 < len(r); i++ {
			tri = append(tri, string(r[i:i+3]))
		}
		for i := 0; i+1 < len(r); i++ {
			bi = append(bi, string(r[i:i+2]))
		}
	}
	return tri, bi, parts.words
}

// rareBigramMaxDF：双字组出现在不超过 5%（至少 2 篇）的文档里才算「稀有」。
func (st *KeywordStats) rareBigramMaxDF() int {
	if st == nil || st.N == 0 {
		return 2
	}
	n := st.N / 20
	if n < 2 {
		n = 2
	}
	return n
}

// ---------------- 拼写容错 ----------------

// IsTypoMatch 判断拼写错误：长度 ≥3 的英文词，编辑距离在容忍范围内。
// 阈值按长度递增：≤7 个字母只容 1 处，≥8 个字母容 2 处；首字母必须相同。
// 这样「verce→vercel」能过，而「fiberv3→fiber」不会（那是加了后缀的另一个词）。
func IsTypoMatch(query, candidate string) bool {
	a, b := []rune(query), []rune(candidate)
	if len(a) < 3 || len(b) < 3 || a[0] != b[0] {
		return false
	}
	limit := 1
	if len(a) >= 8 {
		limit = 2
	}
	if abs(len(a)-len(b)) > limit {
		return false
	}
	return editDistanceWithin(a, b, limit)
}

// editDistanceWithin 带提前退出的编辑距离（只关心是否 ≤ max）。
func editDistanceWithin(a, b []rune, max int) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	if len(b)-len(a) > max {
		return false
	}
	prev := make([]int, len(a)+1)
	cur := make([]int, len(a)+1)
	for i := range prev {
		prev[i] = i
	}
	for j := 1; j <= len(b); j++ {
		cur[0] = j
		rowMin := cur[0]
		for i := 1; i <= len(a); i++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			v := prev[i-1] + cost
			if x := prev[i] + 1; x < v {
				v = x
			}
			if x := cur[i-1] + 1; x < v {
				v = x
			}
			cur[i] = v
			if v < rowMin {
				rowMin = v
			}
		}
		if rowMin > max {
			return false
		}
		prev, cur = cur, prev
	}
	return prev[len(a)] <= max
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// ---------------- 别名 / 概念词（配置驱动） ----------------

var aliasRegexCache sync.Map // pattern -> *regexp.Regexp

// ParseAlias 判断别名形态：形如 /.../ 的按正则处理，否则当普通词。
func ParseAlias(alias string) (pattern string, isRegex bool) {
	if len(alias) >= 2 && alias[0] == '/' && alias[len(alias)-1] == '/' {
		return alias[1 : len(alias)-1], true
	}
	return alias, false
}

// AliasMatch 用一条别名规则匹配文本，返回是否命中、权重与 context 片段。
//   - 正则：按命中次数加权（出现越多越相关），上限 9；
//   - 普通词：走与关键词一致的语言规则（英文整词、中文子串、拼写容错）。
func AliasMatch(text, alias string) (matched bool, weight int, snippet string) {
	pattern, isRegex := ParseAlias(strings.TrimSpace(alias))
	if isRegex {
		re, err := aliasRegex(pattern)
		if err != nil {
			return false, 0, ""
		}
		hits := re.FindAllString(normalizeKeyword(text), -1)
		if len(hits) == 0 {
			return false, 0, ""
		}
		w := 1 + len(hits)
		if w > 9 {
			w = 9
		}
		return true, w, hits[0]
	}
	m := ScoreKeyword(text, pattern)
	if !m.Matched {
		return false, 0, ""
	}
	return true, 2, m.Snippet
}

func aliasRegex(pattern string) (*regexp.Regexp, error) {
	if v, ok := aliasRegexCache.Load(pattern); ok {
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, err
	}
	aliasRegexCache.Store(pattern, re)
	return re, nil
}

// ---------------- mongo 预过滤正则 ----------------

// FuzzyRegexPattern 构造 mongo 侧的候选预过滤正则：中文 ≥2 字的 n-gram
// 与英文整词（词边界）用 | 连接。DB 只负责粗筛，精排与阈值由 Go 侧做。
func FuzzyRegexPattern(query string) string {
	query = normalizeKeyword(query)
	if query == "" {
		return ""
	}
	seen := map[string]bool{}
	var parts []string
	add := func(ng string) {
		if ng == "" || seen[ng] {
			return
		}
		seen[ng] = true
		parts = append(parts, regexp.QuoteMeta(ng))
	}
	parsed := splitQueryParts(query)
	for _, seg := range parsed.cjk {
		runes := []rune(seg)
		for i := 0; i+2 < len(runes); i++ {
			add(string(runes[i : i+3]))
		}
		for i := 0; i+1 < len(runes); i++ {
			add(string(runes[i : i+2]))
		}
	}
	for _, w := range parsed.words {
		if len([]rune(w)) >= 3 {
			parts = append(parts, `\b`+regexp.QuoteMeta(w)+`\b`)
		} else {
			add(w)
		}
	}
	return strings.Join(parts, "|")
}
