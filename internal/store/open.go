package store

import (
	"context"
	"fmt"
	"sort"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
)

// 驱动注册表：适配器在自己的包里 init 注册，store 不反向 import 适配器
// （mongo 适配器要用本包的哨兵错误，直接 import 会成环）。
// cmd/server 用 blank import 触发注册，代价是多一行 import，换来的是：
// 加新数据库 = 新增适配器包 + main 里加一行 blank import，接口零改动。
type OpenFunc func(ctx context.Context, cfg *config.Config) (Store, error)

var drivers = map[string]OpenFunc{}

// RegisterDriver 由适配器包的 init 调用，注册一个存储实现。
func RegisterDriver(name string, fn OpenFunc) {
	drivers[name] = fn
}

// Open 按 cfg.Database.Type 选择存储实现。这是唯一的选择点。
func Open(ctx context.Context, cfg *config.Config) (Store, error) {
	fn, ok := drivers[cfg.Database.Type]
	if !ok {
		available := make([]string, 0, len(drivers))
		for name := range drivers {
			available = append(available, name)
		}
		sort.Strings(available)
		return nil, fmt.Errorf("不支持的 database.type %q（可用：%v；若列表为空，检查 main 是否 import 了对应适配器包）",
			cfg.Database.Type, available)
	}
	return fn(ctx, cfg)
}
