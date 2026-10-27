package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/auth"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store/fake"
)

// 登录服务：正确/错误密码、失败计数、锁定窗口、解锁、清空计数。

func newLoginSvc(t *testing.T) (*LoginService, *fake.Store) {
	t.Helper()
	st := fake.New()
	sum := sha256.Sum256([]byte("good-pass"))
	st.SetUserCredentials("admin", hex.EncodeToString(sum[:]))
	return NewLoginService(st.Users(), auth.New("secret", 7*24*time.Hour)), st
}

func TestLoginSuccessAndFailure(t *testing.T) {
	svc, _ := newLoginSvc(t)
	ctx := context.Background()

	token, err := svc.Login(ctx, "1.1.1.1", "admin", "good-pass")
	if err != nil {
		t.Fatalf("正确密码应成功：%v", err)
	}
	if token == "" {
		t.Fatal("应该返回 token")
	}
	// 错误密码 → auth.locked（与锁定同文案）
	if _, err := svc.Login(ctx, "1.1.1.1", "admin", "bad"); err == nil ||
		!strings.Contains(err.Error(), "auth.locked") {
		t.Errorf("错误密码应 auth.locked：%v", err)
	}
	// 不存在的用户同样 auth.locked（不暴露用户是否存在）
	if _, err := svc.Login(ctx, "1.1.1.1", "nobody", "x"); err == nil ||
		!strings.Contains(err.Error(), "auth.locked") {
		t.Errorf("不存在用户应 auth.locked：%v", err)
	}
}

func TestLoginLockoutAndUnlock(t *testing.T) {
	svc, _ := newLoginSvc(t)
	ctx := context.Background()
	now := time.Unix(1750000000, 0)
	svc.SetClock(func() time.Time { return now })
	ip := "2.2.2.2"

	// 连续 5 次失败后锁定
	for i := 0; i < 5; i++ {
		if _, err := svc.Login(ctx, ip, "admin", "bad"); err == nil {
			t.Fatalf("第 %d 次失败应报错", i+1)
		}
	}
	if got := svc.failureCount(ip); got != 5 {
		t.Errorf("失败计数 = %d，应为 5", got)
	}
	// 锁定期内即使密码正确也拒绝
	if _, err := svc.Login(ctx, ip, "admin", "good-pass"); err == nil {
		t.Error("锁定期内应拒绝")
	}
	// 过了 10 分钟窗口：自动解锁，正确密码可登录
	now = now.Add(11 * time.Minute)
	if _, err := svc.Login(ctx, ip, "admin", "good-pass"); err != nil {
		t.Errorf("窗口过后应解锁：%v", err)
	}
	if got := svc.failureCount(ip); got != 0 {
		t.Errorf("成功登录后应清零失败计数，得到 %d", got)
	}
}

func TestLoginFailureWindowResets(t *testing.T) {
	svc, _ := newLoginSvc(t)
	ctx := context.Background()
	now := time.Unix(1750000000, 0)
	svc.SetClock(func() time.Time { return now })
	ip := "3.3.3.3"

	// 4 次失败后等过窗口，计数应重新从 1 开始（不会累积成锁定）
	for i := 0; i < 4; i++ {
		_, _ = svc.Login(ctx, ip, "admin", "bad")
	}
	now = now.Add(11 * time.Minute)
	_, _ = svc.Login(ctx, ip, "admin", "bad")
	if got := svc.failureCount(ip); got != 1 {
		t.Errorf("过窗后计数应重置为 1，得到 %d", got)
	}
}
