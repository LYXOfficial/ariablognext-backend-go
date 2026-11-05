package textutil

import "regexp"

// SpeakPlainContent 复刻旧 update/speaks/main.py 的 replace_and_remove_tags：
//
//	<a …>…</a> → [链接]，<img …> → [图片]，其余标签删除。
//
// 这是说说说 plainContent 的既有口径（与文章的 CleanMarkdown 不同），不要混用。
//
// 例外：后台说说编辑器插入的 owo 表情是 <img class="bb-emoji">，
// 它记成 [表情] 而不是 [图片]——首页 swiper 只显示 plainContent，
// 不区分的话表情会被念成"图片"。
var (
	speakATagRe     = regexp.MustCompile(`<a\s+[^>]*>.*?</a>`)
	speakEmojiImgRe = regexp.MustCompile(`<img\s[^>]*class=["'][^"']*\bbb-emoji\b[^"']*["'][^>]*>`)
	speakImgRe      = regexp.MustCompile(`<img\s+[^>]*>`)
	speakOtherRe    = regexp.MustCompile(`<.*?>`)
)

func SpeakPlainContent(text string) string {
	text = speakATagRe.ReplaceAllString(text, "[链接]")
	// 必须先于普通图片替换：表情同样匹配 <img …>
	text = speakEmojiImgRe.ReplaceAllString(text, "[表情]")
	text = speakImgRe.ReplaceAllString(text, "[图片]")
	return speakOtherRe.ReplaceAllString(text, "")
}
