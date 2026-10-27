package handler

import (
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
)

// handler 测试的配置底座：与 internal/config 测试同思路，
// 只填 handler 路径用到的字段（不经 Load，绕开外部依赖）。
func handlerTestConfig() *config.Config {
	if _, err := time.LoadLocation("Asia/Shanghai"); err != nil {
		panic(err)
	}
	return &config.Config{
		List:   config.ListConfig{DefaultPageSize: 16, MaxPageSize: 100, MaxFullItems: 300},
		Search: config.SearchConfig{MaxKeywordLength: 64, ContextRadius: 50, TitleBoost: true},
		Flink: config.FlinkConfig{
			MaxName: 50, MaxDescription: 200, MaxURL: 300,
			PendingLimit: 20, RetentionDays: 14,
		},
		Features: config.FeaturesConfig{Export: true},
		Site: config.SiteConfig{
			Name:          "测试站",
			URL:           "https://blog.yaria.top",
			AdminPanelURL: "https://blog.yaria.top/admin/flinks",
		},
		Auth: config.AuthConfig{
			JWTSecret:     "test-secret",
			JWTAlgorithm:  "HS256",
			JWTTTL:        config.Duration(7 * 24 * time.Hour),
			InternalToken: "test-secret",
		},
		Server: config.ServerConfig{
			Timezone:       "Asia/Shanghai",
			RequestTimeout: config.Duration(8 * time.Second),
			DBTimeout:      config.Duration(5 * time.Second),
			BodyLimit: config.BodyLimitConfig{
				Default: 1 * config.MB, Markdown: 2 * config.MB, Image: 4 * config.MB,
			},
			CORS: config.CORSConfig{
				AllowOrigins: []string{"*"},
				AllowHeaders: []string{"*"},
				AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			},
		},
	}
}
