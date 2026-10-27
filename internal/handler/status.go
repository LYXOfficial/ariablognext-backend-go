package handler

import (
	"github.com/LYXOfficial/ariablognext-backend-go/internal/app"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/service"
)

// status：GET /status。与其它接口同信封同状态码（200 + {ok, requestId, data}），
// data 是服务自身与各外部依赖的可达性快照。探测在 service 层并发执行（≤3s）。
func status(cfg *config.Config, svc *service.Set, routeCount func() int) app.Handler {
	return func(c *app.Ctx) (any, error) {
		report := svc.Status.Probe(c.Context())
		// 路由总数只有 app 知道；用闭包在响应时取，避免注册顺序影响计数
		report.RouteCount = routeCount()
		return report, nil
	}
}
