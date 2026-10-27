// Package validate 是纯函数校验（友链申请规则，照抄 flink/main.py，不自创）。
package validate

import "regexp"

// 规则与旧后端逐条对应：
//   - XSS：任何带标签/协议/事件属性的输入都拒绝；
//   - URL：仅 http(s)，不允许空白与尖括号；
//   - 邮箱：常规格式；
//   - 颜色：十六进制 / 函数式 / 纯字母（会拼进前端 <style>，防 CSS 注入）。
var (
	XSSPattern   = regexp.MustCompile(`(?i)<\s*/?\s*[a-zA-Z!]|javascript\s*:|vbscript\s*:|data\s*:|on[a-z]+\s*=`)
	URLPattern   = regexp.MustCompile(`(?i)^https?://[^\s<>"'\\]+$`)
	EmailPattern = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)
	ColorPattern = regexp.MustCompile(`^(#[0-9a-fA-F]{3,8}|rgba?\([0-9.,%\s]+\)|hsla?\([0-9.,%\s]+\)|[a-zA-Z]{3,20})$`)
)

// Limits 是申请字段的长度上限（与 config.flink.* 对齐，由调用方传入）。
type Limits struct {
	MaxName        int
	MaxDescription int
	MaxURL         int
}

// FieldViolation 是一个字段的校验失败描述。
type FieldViolation struct {
	Field   string
	Message string
}

// ValidateApplication 校验申请内容；全部通过返回 nil。
// 错误聚合成 details.fields（一次报全，和配置校验同一体验）。
func ValidateApplication(limits Limits, name, link, avatar, descr, email, color, targetURL string, appType int) []FieldViolation {
	var violations []FieldViolation
	add := func(field, msg string) {
		violations = append(violations, FieldViolation{Field: field, Message: msg})
	}

	// XSS 检查优先：语义是「内容含攻击载荷」而不是「格式不对」
	type field struct{ label, value string }
	for _, f := range []field{
		{"name", name}, {"link", link}, {"avatar", avatar},
		{"descr", descr}, {"email", email}, {"color", color}, {"targetUrl", targetURL},
	} {
		if f.value != "" && XSSPattern.MatchString(f.value) {
			add(f.label, "包含不被允许的内容")
		}
	}
	if len(violations) > 0 {
		return violations
	}

	if name == "" || link == "" || avatar == "" {
		if name == "" {
			add("name", "站点名称必填")
		}
		if link == "" {
			add("link", "网址必填")
		}
		if avatar == "" {
			add("avatar", "头像必填")
		}
	}
	if len([]rune(name)) > limits.MaxName {
		add("name", "站点名称不超过 "+itoa(limits.MaxName)+" 字")
	}
	if len([]rune(descr)) > limits.MaxDescription {
		add("descr", "描述不超过 "+itoa(limits.MaxDescription)+" 字")
	}
	if link != "" && (!URLPattern.MatchString(link) || len(link) > limits.MaxURL) {
		add("link", "网址格式不正确，必须以 http:// 或 https:// 开头")
	}
	if avatar != "" && (!URLPattern.MatchString(avatar) || len(avatar) > limits.MaxURL) {
		add("avatar", "头像格式不正确，必须以 http:// 或 https:// 开头")
	}
	if email != "" && !EmailPattern.MatchString(email) {
		add("email", "邮箱格式不正确")
	}
	if color != "" && !ColorPattern.MatchString(color) {
		add("color", "颜色只支持 #十六进制 / rgb() / rgba() / hsl() / 颜色名")
	}
	if appType != 0 && appType != 1 {
		add("type", "申请类型不正确")
	}
	if appType == 1 && !URLPattern.MatchString(targetURL) {
		add("targetUrl", "修改申请需要填写当前已收录的网址")
	}
	return violations
}

// NormalizeText = 旧 _norm_text：trim。
func NormalizeText(s string) string { return trimSpace(s) }

// NormalizeURLText = 旧 _norm_url_text：trim + 去结尾斜杠。
func NormalizeURLText(s string) string {
	t := trimSpace(s)
	if len(t) > 1 && t[len(t)-1] == '/' {
		return t[:len(t)-1]
	}
	return t
}

// NormalizeColor = 旧 _norm_color：trim + 缺省 #888888 + 小写。
func NormalizeColor(s string) string {
	t := trimSpace(s)
	if t == "" {
		t = "#888888"
	}
	return toLower(t)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// 小工具：validate 包不 import strings，保持零依赖
func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

func toLower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
