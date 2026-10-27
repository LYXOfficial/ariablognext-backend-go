package middleware

import (
	"log/slog"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/requestid"
)

// RequestID 挂官方 requestId 中间件：生成/透传 X-Request-Id，
// 并写进 fiber locals 供信封与日志取用。
func RequestID() fiber.Handler {
	return requestid.New()
}

// FromRequestID 从 fiber locals 取 requestId（404 等无 handler 响应也要带）。
func FromRequestID(fc fiber.Ctx) string {
	return requestid.FromContext(fc)
}

// Cors 按配置挂 CORS 中间件。
// ⚠️ config.yml 里 allowOrigins=["*"] + allowCredentials=true 并存——浏览器本就会
// 拒绝带凭证的通配响应，而 fiber v3 的 cors 中间件见到这个组合会直接 panic。
// 现状前后端都不用 cookie 凭证，所以这里在通配 origin 下静默降级 credentials，
// 配置文件保持原样；真要启用凭证时应该把 origins 收敛成白名单。
func Cors(cfg *config.Config) fiber.Handler {
	allowCredentials := cfg.Server.CORS.AllowCredentials
	for _, o := range cfg.Server.CORS.AllowOrigins {
		if o == "*" {
			allowCredentials = false
			break
		}
	}
	return cors.New(cors.Config{
		AllowOrigins:     cfg.Server.CORS.AllowOrigins,
		AllowCredentials: allowCredentials,
		AllowHeaders:     cfg.Server.CORS.AllowHeaders,
		AllowMethods:     cfg.Server.CORS.AllowMethods,
	})
}

// LogRequest 输出一条结构化访问日志。字段与 design-notes §8 对齐：
// requestId、method、path、status、ms；失败时附错误码。
func LogRequest(requestID, method, path string, status int, took time.Duration, errCode string) {
	args := []any{
		"requestId", requestID,
		"method", method,
		"path", path,
		"status", status,
		"ms", took.Milliseconds(),
	}
	if errCode != "" {
		args = append(args, "code", errCode)
	}
	slog.Info("request", args...)
}
