package handler

import (
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/app"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

// version 由构建注入（ldflags -X main.version=...）；本地跑是 dev。
// health 响应里带着它，线上排查时能对出「哪个构建」。
var version = "dev"

// health 真的 ping 数据库：DB 不通时 503 + internal.error（TASKS §4.2）。
// 状态字段的语义：status=服务活着，db=数据库可达；两者一起健康才是真的健康。
func health(st store.Store) app.Handler {
	return func(c *app.Ctx) (any, error) {
		if err := st.Ping(c.Context()); err != nil {
			// Ping 失败按 internal.error 503 走统一信封，cause 里带真实原因
			return nil, wrapStore(err, "数据库连接失败，请检查 mongod 是否在运行")
		}
		return map[string]any{
			"status":  "ok",
			"db":      "ok",
			"version": version,
			// unix 秒：契约规定时间一律秒，响应里不许出现带时区歧义的字符串
			"time": time.Now().Unix(),
		}, nil
	}
}
