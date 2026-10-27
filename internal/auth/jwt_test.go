package auth

import (
	"strings"
	"testing"
	"time"
)

func TestIssueAndVerify(t *testing.T) {
	base := time.Unix(1750000000, 0)
	j := New("test-secret", 7*24*time.Hour)
	j.SetClock(func() time.Time { return base })

	token, err := j.Issue("ariasaka")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := j.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Sub != "ariasaka" {
		t.Errorf("sub = %q", claims.Sub)
	}
	if claims.Exp != base.Add(7*24*time.Hour).Unix() {
		t.Errorf("exp = %d", claims.Exp)
	}
	// 裸 token（不带 Bearer 前缀）也接受
	if _, err := j.Verify(strings.TrimPrefix(token, "Bearer ")); err != nil {
		t.Errorf("裸 token 应可校验：%v", err)
	}
	// Bearer 前缀形态
	if _, err := j.Verify("Bearer " + token); err != nil {
		t.Errorf("Bearer 形态应可校验：%v", err)
	}
}

func TestVerifyExpired(t *testing.T) {
	base := time.Unix(1750000000, 0)
	j := New("test-secret", time.Hour)
	j.SetClock(func() time.Time { return base })
	token, _ := j.Issue("u")

	// 时钟拨到过期后：auth.invalid_token
	j.SetClock(func() time.Time { return base.Add(2 * time.Hour) })
	_, err := j.Verify(token)
	if err == nil || !strings.Contains(err.Error(), "auth.invalid_token") {
		t.Errorf("过期应报 auth.invalid_token：%v", err)
	}
}

func TestVerifyTampered(t *testing.T) {
	j := New("test-secret", time.Hour)
	token, _ := j.Issue("u")
	// 改一个字符
	tampered := token[:len(token)-2] + "xx"
	if _, err := j.Verify(tampered); err == nil ||
		!strings.Contains(err.Error(), "auth.invalid_token") {
		t.Errorf("篡改应报 auth.invalid_token：%v", err)
	}
	// 换密钥签的 token 也不行
	other := New("other-secret", time.Hour)
	otherToken, _ := other.Issue("u")
	if _, err := j.Verify(otherToken); err == nil {
		t.Error("他钥签发的 token 应被拒绝")
	}
}

func TestVerifyEmpty(t *testing.T) {
	j := New("test-secret", time.Hour)
	if _, err := j.Verify(""); err == nil ||
		!strings.Contains(err.Error(), "auth.missing_token") {
		t.Errorf("空 token 应报 auth.missing_token：%v", err)
	}
	// "Bearer " 只有前缀也算缺
	if _, err := j.Verify("Bearer "); err == nil {
		t.Error("空 Bearer 应报 missing")
	}
}
