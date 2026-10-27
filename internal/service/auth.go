package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/auth"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

// LoginService：后台登录。
// 爆破防护与旧后端等价：进程内按 IP 记连续失败，5 次 / 10 分钟窗，
// 超限锁定 10 分钟。锁定与「密码错误」返回同一错误码与文案——不暴露锁定状态。
type LoginService struct {
	users store.UserRepository
	jwt   *auth.JWT

	mu       sync.Mutex
	failures map[string]failureEntry // ip → 连续失败次数 + 最近失败时间
	clock    func() time.Time
}

const (
	failLimit    = 5
	lockDuration = 10 * time.Minute
)

type failureEntry struct {
	count int
	last  time.Time
}

func NewLoginService(users store.UserRepository, jwt *auth.JWT) *LoginService {
	return &LoginService{users: users, jwt: jwt, failures: map[string]failureEntry{}, clock: time.Now}
}

// SetClock 注入时钟（测试用）。
func (s *LoginService) SetClock(now func() time.Time) { s.clock = now }

// Login 返回 JWT；错误统一 auth.locked / 403（见契约 §0.3：不暴露锁定状态）。
func (s *LoginService) Login(ctx context.Context, ip, user, password string) (string, error) {
	now := s.clock()
	if s.isLocked(ip, now) {
		return "", apperr.New(apperr.AuthLocked, "用户名或密码错误", nil)
	}

	sum := sha256.Sum256([]byte(password))
	u, err := s.users.ByCredentials(ctx, user, hex.EncodeToString(sum[:]))
	if err == store.ErrNotFound {
		s.recordFailure(ip, now)
		return "", apperr.New(apperr.AuthLocked, "用户名或密码错误", nil)
	}
	if err != nil {
		return "", err
	}

	s.clearFailures(ip)
	return s.jwt.Issue(u.User)
}

func (s *LoginService) isLocked(ip string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.failures[ip]
	if !ok {
		return false
	}
	if now.Sub(entry.last) >= lockDuration {
		delete(s.failures, ip)
		return false
	}
	return entry.count >= failLimit
}

func (s *LoginService) recordFailure(ip string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.failures[ip]
	if !ok || now.Sub(entry.last) >= lockDuration {
		s.failures[ip] = failureEntry{count: 1, last: now}
		return
	}
	entry.count++
	entry.last = now
	s.failures[ip] = entry
}

func (s *LoginService) clearFailures(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failures, ip)
}

// 供测试断言的便捷方法
func (s *LoginService) failureCount(ip string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failures[ip].count
}
