package middleware

import (
	"sync"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
)

// 进程内滑动窗口限流器。多实例下按实例独立（与旧后端语义等价），
// 不假装是全局限流——文档已写明，别在注释之外的地方吹。
// map 按需清理：条目超过 maxKeys 时顺带清一次过期项，防无限增长。
type RateLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	entries map[string][]time.Time // key → 窗口内的命中时间
}

const maxKeys = 5000

func NewRateLimiter(window time.Duration) *RateLimiter {
	return &RateLimiter{window: window, entries: map[string][]time.Time{}}
}

// Allow 记录一次命中并返回是否放行（窗口内未超 requests）。
// requests<=0 视为「未配置 = 不限流」：测试环境常留零值，防御性放行。
func (l *RateLimiter) Allow(key string, requests int) bool {
	if requests <= 0 {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.entries) > maxKeys {
		l.pruneLocked(now)
	}
	hits := l.entries[key]
	// 只保留窗口内的记录
	keep := hits[:0]
	for _, t := range hits {
		if now.Sub(t) < l.window {
			keep = append(keep, t)
		}
	}
	hits = keep
	if len(hits) >= requests {
		l.entries[key] = hits
		return false
	}
	l.entries[key] = append(hits, now)
	return true
}

// RetryAfter 返回距离最早一条记录出窗的秒数（命中限流时给客户端的提示）。
func (l *RateLimiter) RetryAfter(key string) int {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	hits := l.entries[key]
	if len(hits) == 0 {
		return 1
	}
	earliest := hits[0]
	d := l.window - now.Sub(earliest)
	if d < time.Second {
		return 1
	}
	return int(d.Seconds())
}

func (l *RateLimiter) pruneLocked(now time.Time) {
	for k, hits := range l.entries {
		keep := hits[:0]
		for _, t := range hits {
			if now.Sub(t) < l.window {
				keep = append(keep, t)
			}
		}
		if len(keep) == 0 {
			delete(l.entries, k)
		} else {
			l.entries[k] = keep
		}
	}
}

// RouteLimiters 是按路由的限流器集合（config.rateLimit.* 一一对应）。
type RouteLimiters struct {
	Global     *RateLimiter
	Login      *RateLimiter
	LinkApply  *RateLimiter
	ThemeColor *RateLimiter
}

func NewRouteLimiters(cfg *config.Config) *RouteLimiters {
	return &RouteLimiters{
		Global:     NewRateLimiter(cfg.RateLimit.Global.Window.Std()),
		Login:      NewRateLimiter(cfg.RateLimit.Login.Window.Std()),
		LinkApply:  NewRateLimiter(cfg.RateLimit.LinkApply.Window.Std()),
		ThemeColor: NewRateLimiter(cfg.RateLimit.ThemeColor.Window.Std()),
	}
}
