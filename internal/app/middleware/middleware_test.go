package middleware

import (
	"errors"
	"testing"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
)

// 中间件包虽小，架构守卫要求它也有测试；这里覆盖三个纯逻辑点。

func TestTimeoutForOverrideAndDefault(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{
			RequestTimeout:   config.Duration(8 * time.Second),
			TimeoutOverrides: map[string]config.Duration{"/maintenance/export": config.Duration(120 * time.Second)},
		},
	}
	if got := TimeoutFor(cfg, "/maintenance/export"); got != 120*time.Second {
		t.Errorf("override 失效：%v", got)
	}
	if got := TimeoutFor(cfg, "/posts/list"); got != 8*time.Second {
		t.Errorf("默认超时错误：%v", got)
	}
	// 覆盖值非法（<=0）时回落默认，不返回 0
	cfg.Server.TimeoutOverrides["/bad"] = 0
	if got := TimeoutFor(cfg, "/bad"); got != 8*time.Second {
		t.Errorf("非法覆盖值应回落默认：%v", got)
	}
}

func TestSafeRecoversPanic(t *testing.T) {
	panicked := errors.New("captured")
	_, err := Safe(func() (int, error) {
		panic("boom")
	}, func(r any) error {
		if r != "boom" {
			t.Errorf("panic 值错误：%v", r)
		}
		return panicked
	})
	if !errors.Is(err, panicked) {
		t.Errorf("onPanic 的错误应被返回：%v", err)
	}

	// 正常路径：结果透传、不触发 onPanic
	n, err := Safe(func() (int, error) { return 42, nil }, func(r any) error {
		t.Fatal("不该触发 onPanic")
		return nil
	})
	if err != nil || n != 42 {
		t.Errorf("正常路径结果错误：%d %v", n, err)
	}
}
