package platform

import (
	"bytes"
	"fmt"
	"log/slog"
	"mime"
	"time"
)

func slogWarn(msg string, args ...any) { slog.Warn(msg, args...) }

// buildMIME 组一封「纯文本 + 可选 HTML」的 multipart 邮件。
// 手写 MIME 而不是引第三方库：依赖清单没有邮件库，标准库 net/textproto 足够。
func buildMIME(from, fromName, to, subject, text, html string) []byte {
	buf := &bytes.Buffer{}
	boundary := "aria-" + fmt.Sprint(time.Now().UnixNano())
	fromHeader := from
	if fromName != "" {
		// 显示名含非 ASCII 时按 RFC 2047 编码（中文发件人名）
		fromHeader = mime.QEncoding.Encode("utf-8", fromName) + " <" + from + ">"
	}
	buf.WriteString("From: " + fromHeader + "\r\n")
	buf.WriteString("To: " + to + "\r\n")
	buf.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	buf.WriteString("MIME-Version: 1.0\r\n")
	if html == "" {
		buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		buf.WriteString(text)
	} else {
		buf.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		buf.WriteString(text + "\r\n")
		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
		buf.WriteString(html + "\r\n")
		buf.WriteString("--" + boundary + "--\r\n")
	}
	return buf.Bytes()
}
