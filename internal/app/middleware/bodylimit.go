package middleware

import (
	"strconv"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
)

// BodyLimitFor 返回路由的请求体上限：图片 4MB > markdown 2MB > 默认 1MB。
// 路由到档位的映射集中在这里，新写接口按需登记；
// Vercel 平台还有 4.5MB 硬上限（文档明写，不是我们的 bug）。
func BodyLimitFor(cfg *config.Config, path string) config.ByteSize {
	switch path {
	case "/posts/content", "/drafts/content":
		return cfg.Server.BodyLimit.Markdown
	case "/images/upload", "/images/reupload":
		return cfg.Server.BodyLimit.Image
	default:
		return cfg.Server.BodyLimit.Default
	}
}

// CheckBodySize 用 Content-Length 预检请求体大小。
// 超限返回 image.too_large（图片路由）或 validation.invalid_field（其它）。
// 注意这只挡「声明了 Content-Length」的请求；分块传输由平台 4.5MB 上限兜底。
func CheckBodySize(cfg *config.Config, path string, contentLength int64) error {
	limit := BodyLimitFor(cfg, path)
	// limit<=0 视为「未配置」：跳过预检（测试环境零值配置；生产配置校验已保证非零）
	if limit <= 0 || contentLength <= 0 || contentLength <= limit.Int64() {
		return nil
	}
	if path == "/images/upload" || path == "/images/reupload" {
		return apperr.New(apperr.ImageTooLarge,
			"图片超过 "+humanSize(limit)+" 上限，请压缩后重试", nil)
	}
	return apperr.InvalidField("body", "请求体超过 "+humanSize(limit)+" 上限")
}

func humanSize(b config.ByteSize) string {
	switch {
	case b >= config.GB:
		return strconv.Itoa(int(b/config.GB)) + "GB"
	case b >= config.MB:
		return strconv.Itoa(int(b/config.MB)) + "MB"
	default:
		return strconv.Itoa(int(b/config.KB)) + "KB"
	}
}
