package textutil

import "regexp"

// SpeakPlainContent 复刻旧 update/speaks/main.py 的 replace_and_remove_tags：
//
//	<a …>…</a> → [链接]，<img …> → [图片]，其余标签删除。
//
// 这是说说说 plainContent 的既有口径（与文章的 CleanMarkdown 不同），不要混用。
var (
	speakATagRe  = regexp.MustCompile(`<a\s+[^>]*>.*?</a>`)
	speakImgRe   = regexp.MustCompile(`<img\s+[^>]*>`)
	speakOtherRe = regexp.MustCompile(`<.*?>`)
)

func SpeakPlainContent(text string) string {
	text = speakATagRe.ReplaceAllString(text, "[链接]")
	text = speakImgRe.ReplaceAllString(text, "[图片]")
	return speakOtherRe.ReplaceAllString(text, "")
}
