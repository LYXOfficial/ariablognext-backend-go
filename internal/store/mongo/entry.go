package mongo

import (
	"context"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

// 把 mongo 适配器注册进 store 的驱动表；cmd/server blank import 本包即生效。
func init() {
	store.RegisterDriver("mongo", func(ctx context.Context, cfg *config.Config) (store.Store, error) {
		return Open(ctx, cfg)
	})
}

// Open 建立连接并 Ping 一次。见 store.go 的同名导出说明。
func Open(ctx context.Context, cfg *config.Config) (*Store, error) {
	return open(ctx, cfg)
}
