package middleware

import (
	"testing"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
)

// 限流器与体积检查的纯逻辑测试（不经过 fiber）。

func TestRateLimiterWindow(t *testing.T) {
	l := NewRateLimiter(time.Minute)
	// 窗口内前 3 次放行，第 4 次拒绝
	for i := 0; i < 3; i++ {
		if !l.Allow("ip1", 3) {
			t.Fatalf("第 %d 次应放行", i+1)
		}
	}
	if l.Allow("ip1", 3) {
		t.Fatal("超限应拒绝")
	}
	// 其它 key 不受影响
	if !l.Allow("ip2", 3) {
		t.Error("不同 key 应独立计数")
	}
	// Retry-After 在 (0, window] 之间
	if d := l.RetryAfter("ip1"); d <= 0 || d > 60 {
		t.Errorf("RetryAfter = %d", d)
	}
	// requests<=0 = 不限制（测试环境零值配置）
	if !l.Allow("ip3", 0) {
		t.Error("未配置限流时应放行")
	}
}

func TestRateLimiterPrunes(t *testing.T) {
	// 短窗口：睡眠后旧记录应过期，重新放行
	l := NewRateLimiter(30 * time.Millisecond)
	if !l.Allow("k", 1) {
		t.Fatal("首次应放行")
	}
	if l.Allow("k", 1) {
		t.Fatal("窗口内应拒绝")
	}
	time.Sleep(40 * time.Millisecond)
	if !l.Allow("k", 1) {
		t.Error("窗口过后应重新放行")
	}
}

func TestRouteLimitersFromConfig(t *testing.T) {
	cfg := &config.Config{RateLimit: config.RateLimitConfig{
		Global:     config.RateRule{Requests: 300, Window: config.Duration(time.Minute)},
		Login:      config.RateRule{Requests: 10, Window: config.Duration(10 * time.Minute)},
		LinkApply:  config.RateRule{Requests: 5, Window: config.Duration(30 * time.Minute)},
		ThemeColor: config.RateRule{Requests: 60, Window: config.Duration(time.Hour)},
	}}
	rl := NewRouteLimiters(cfg)
	if rl.Global == nil || rl.Login == nil || rl.LinkApply == nil || rl.ThemeColor == nil {
		t.Fatal("四个限流器都要创建")
	}
}

func TestCheckBodySize(t *testing.T) {
	cfg := &config.Config{Server: config.ServerConfig{BodyLimit: config.BodyLimitConfig{
		Default: 1 * config.MB, Markdown: 2 * config.MB, Image: 4 * config.MB,
	}}}
	// 默认档：1MB 以内 OK，超出报 validation.invalid_field
	if err := CheckBodySize(cfg, "/posts/create", 1024); err != nil {
		t.Errorf("小请求不该拦：%v", err)
	}
	if err := CheckBodySize(cfg, "/posts/create", 2*1024*1024); err == nil {
		t.Error("超过默认 1MB 应拦")
	}
	// markdown 档 2MB
	if err := CheckBodySize(cfg, "/posts/content", 1500*1024); err != nil {
		t.Errorf("1.5MB 正文应放行：%v", err)
	}
	// 图片档 4MB，超限是 image.too_large
	err := CheckBodySize(cfg, "/images/upload", 5*1024*1024)
	if err == nil {
		t.Fatal("超 4MB 应拦")
	}
	if got := err.Error(); !containsSub(got, "image.too_large") {
		t.Errorf("图片超限应是 image.too_large：%v", err)
	}
	// limit<=0（未配置）跳过
	empty := &config.Config{}
	if err := CheckBodySize(empty, "/posts/create", 1<<30); err != nil {
		t.Errorf("未配置时不该拦：%v", err)
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
