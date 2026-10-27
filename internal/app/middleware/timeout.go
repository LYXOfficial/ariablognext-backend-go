// Package middleware 收纳可复用的 HTTP 中间件与小工具。
// 与 fiber 直接相关的（requestId / CORS）是对官方中间件的薄封装；
// 超时 / recover / 日志是自己写的——它们要跟统一信封配合，官方默认行为不符合契约。
package middleware

import (
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
)

// TimeoutFor 查路由级超时：timeoutOverrides 命中路径用覆盖值，否则用默认值。
// 导出（扫全库 2500 文档）、摘要（等 LLM）这类慢接口不放宽必被 8 秒砍掉。
func TimeoutFor(cfg *config.Config, path string) time.Duration {
	if d, ok := cfg.Server.TimeoutOverrides[path]; ok && d.Std() > 0 {
		return d.Std()
	}
	return cfg.Server.RequestTimeout.Std()
}
