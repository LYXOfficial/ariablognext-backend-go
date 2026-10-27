package validate

import (
	"strings"
	"testing"
)

// 用例对齐 testing.md §2.5 与旧 flink/main.py 的语义。

var stdLimits = Limits{MaxName: 50, MaxDescription: 200, MaxURL: 300}

func v(name, link, avatar, descr, email, color, targetURL string, t int) []FieldViolation {
	return ValidateApplication(stdLimits, name, link, avatar, descr, email, color, targetURL, t)
}

func TestValidApplication(t *testing.T) {
	if got := v("站点", "https://x.example", "https://x.example/a.png", "介绍", "a@b.cc", "#f08", "", 0); got != nil {
		t.Errorf("正常申请不该报错：%v", got)
	}
	// 颜色三种形态都通过
	for _, color := range []string{"#f08", "rgb(1,2,3)", "red"} {
		if got := v("站点", "https://x.example", "https://x.example/a.png", "", "", color, "", 0); got != nil {
			t.Errorf("color=%s 应通过：%v", color, got)
		}
	}
}

func TestMissingFields(t *testing.T) {
	got := v("", "", "", "", "", "", "", 0)
	if !hasField(got, "name") || !hasField(got, "link") || !hasField(got, "avatar") {
		t.Errorf("必填缺失要逐字段报出：%v", got)
	}
}

func TestURLWithoutProtocol(t *testing.T) {
	if !hasField(v("站点", "x.example", "https://a.b/c.png", "", "", "", "", 0), "link") {
		t.Error("不带协议的网址应报 link")
	}
}

func TestXSSRejected(t *testing.T) {
	// <script 与事件属性都按 XSS 拒
	if !hasField(v("站点", "https://x.com/<script", "", "", "", "", "", 0), "link") {
		t.Error("script 标签应被拒")
	}
	if !hasField(v(`<b onmouseover=alert(1)>x`, "https://x.com", "https://a.b/c.png", "", "", "", "", 0), "name") {
		t.Error("事件属性应被拒")
	}
}

func TestColorInjectionRejected(t *testing.T) {
	if !hasField(v("站点", "https://x.com", "https://a.b/c.png", "", "", "red; }", "", 0), "color") {
		t.Error("CSS 注入应被拒")
	}
}

func TestModifyNeedsTargetURL(t *testing.T) {
	got := v("站点", "https://x.com", "https://a.b/c.png", "", "", "", "", 1)
	if !hasField(got, "targetUrl") {
		t.Errorf("type=1 缺 targetUrl 应报：%v", got)
	}
	// type=2 直接拒绝
	got = v("站点", "https://x.com", "https://a.b/c.png", "", "", "", "https://t.example", 2)
	if !hasField(got, "type") {
		t.Errorf("type=2 应报：%v", got)
	}
}

func TestLengthLimits(t *testing.T) {
	long := strings.Repeat("名", 51)
	if !hasField(v(long, "https://x.com", "https://a.b/c.png", "", "", "", "", 0), "name") {
		t.Error("51 字名称应报长度")
	}
	// 50 字整应通过
	if got := v(strings.Repeat("名", 50), "https://x.com", "https://a.b/c.png", "", "", "", "", 0); got != nil {
		t.Errorf("50 字应通过：%v", got)
	}
}

func TestNormalizers(t *testing.T) {
	if NormalizeURLText("https://x.com/") != "https://x.com" {
		t.Error("URL 应去结尾斜杠")
	}
	if NormalizeColor("") != "#888888" {
		t.Error("颜色缺省 #888888")
	}
	if NormalizeColor(" #F08 ") != "#f08" {
		t.Error("颜色应 trim + 小写")
	}
}

func hasField(violations []FieldViolation, field string) bool {
	for _, v := range violations {
		if v.Field == field {
			return true
		}
	}
	return false
}
