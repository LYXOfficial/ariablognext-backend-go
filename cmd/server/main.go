// cmd/server 是服务入口：Vercel Go 函数与本地开发共用。
// 组装顺序固定：配置 → 存储 → app → 路由 → 监听，全部显式传参，没有全局变量。
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	// tzdata 内嵌进二进制：所有「哪年哪月」的计算不依赖容器 TZ（design-notes §5）
	_ "time/tzdata"

	// mongo 适配器在这里注册进 store 的驱动表（注册表模式，见 store/open.go）
	_ "github.com/LYXOfficial/ariablognext-backend-go/internal/store/mongo"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/app"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/handler"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/service"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	cfg, err := config.Load()
	if err != nil {
		// 配置失败必须带着原因退出：绝不带着半套配置跑起来
		slog.Error("配置加载失败", "err", err)
		os.Exit(1)
	}
	slog.Info("配置加载完成",
		"listen", cfg.Server.Listen,
		"database", cfg.Database.Type,
		"readOnly", cfg.Database.ReadOnly,
		"redacted", cfg.Redacted())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg)
	if err != nil {
		slog.Error("存储初始化失败", "err", err)
		os.Exit(1)
	}

	a := app.New(cfg, st)
	// 组装顺序（依赖注入约定）：config → store → service → handler → app
	svc := service.NewSet(cfg, st)
	// 登录服务要 JWT 签发器（app.New 里已按配置构造），两段式挂载
	svc.AttachLogin(st.Users(), a.JWT())
	handler.Register(a, cfg, st, svc, handler.NewImageStore(cfg))

	// 监听：本地 127.0.0.1:2333；Vercel 上 listen 已在 config 里解析为 :$PORT
	listen := cfg.Server.Listen
	go func() {
		slog.Info("服务启动", "listen", listen, "routes", len(a.Routes()))
		if err := a.Fiber().Listen(listen); err != nil {
			slog.Error("监听失败", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("收到退出信号，开始收尾")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Fiber().Shutdown(); err != nil {
		slog.Warn("优雅关停失败", "err", err)
	}
	if err := st.Close(shutdownCtx); err != nil {
		slog.Warn("数据库连接关闭失败", "err", err)
	}
	slog.Info("服务已退出")
}
