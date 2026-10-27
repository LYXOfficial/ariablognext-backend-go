package platform

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
)

// SniffImageType 按魔数判断图片类型，复刻旧 update/image/main.py 的
// sniff_image_type（content-type 不可信，如 octet-stream）。
// bmp 在旧转存里同样被拒（CONTENT_TYPE_EXT 没有 image/bmp 条目），这里不收。
func SniffImageType(body []byte) string {
	if bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")) {
		return "image/png"
	}
	if bytes.HasPrefix(body, []byte("\xff\xd8\xff")) {
		return "image/jpeg"
	}
	if bytes.HasPrefix(body, []byte("GIF87a")) || bytes.HasPrefix(body, []byte("GIF89a")) {
		return "image/gif"
	}
	if len(body) >= 12 && bytes.Equal(body[:4], []byte("RIFF")) && bytes.Equal(body[8:12], []byte("WEBP")) {
		return "image/webp"
	}
	return ""
}

// FetchExternalImage 下载外链图片（图床转存用）。
// SSRF 校验与 theme-color 同语义：只允许 http(s)、拒绝解析到私网/环回的主机
// （云元数据 169.254.169.254 是经典攻击目标，这道闸不能省）。
// 体积与超时由调用方传入（config.media.import.*：3MB / 20s）。
func FetchExternalImage(ctx context.Context, rawURL string, maxBytes int, timeout time.Duration) ([]byte, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, "", apperr.InvalidField("url", "图片地址必须以 http:// 或 https:// 开头")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", apperr.Wrap(apperr.ImageUnreachable, "图片下载请求构造失败", err)
	}
	req.Header.Set("User-Agent", "AriaBlogNext-image-import/1.0")
	// 内网/环回/重定向到内网/DNS rebinding 都在 dial 阶段统一拦（见 safedial.go）
	client := NewSafeHTTPClient(false, timeout)
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, ErrPrivateAddress) {
			return nil, "", apperr.InvalidField("url", "图片地址指向内网，已拒绝")
		}
		return nil, "", apperr.New(apperr.ImageUnreachable,
			"图片下载失败（对方站点无响应或超时），可稍后重试",
			apperr.CauseDetails{Cause: err.Error()})
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", apperr.New(apperr.ImageUnreachable,
			"图片下载失败（对方返回状态码 "+itoa(resp.StatusCode)+"）", nil)
	}
	// 超限即截断报错：不把超过上限的字节读进内存
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return nil, "", apperr.Wrap(apperr.ImageUnreachable, "图片下载中断", err)
	}
	if len(body) > maxBytes {
		return nil, "", apperr.New(apperr.ImageTooLarge, "图片超过转存大小上限，请先压缩", nil)
	}
	// 与旧后端一致：魔数嗅探优先（content-type 不可信），嗅不出就拒绝
	if ct := SniffImageType(body); ct != "" {
		return body, ct, nil
	}
	return nil, "", apperr.New(apperr.ImageUnsupportedType,
		"对方返回的不是支持的图片格式（jpeg/png/gif/webp）", nil)
}

func itoa(n int) string {
	digits := []byte("0123456789")
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := []byte{}
	for n > 0 {
		buf = append([]byte{digits[n%10]}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
