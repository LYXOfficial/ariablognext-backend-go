package platform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// SSRF 闸门在 **dial 阶段**生效：既覆盖「URL 直接指向内网」，也覆盖
// 「公网 URL 302 到内网」与 DNS rebinding（校验与连接绑定在同一个已校验 IP 上）。
// 生产一律 allowPrivate=false；只有测试才打开。

func TestSafeClientBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should-not-reach"))
	}))
	defer srv.Close()

	// 默认（生产）客户端：连 127.0.0.1 必须被拦
	client := NewSafeHTTPClient(false, 2*time.Second)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("连内网应当被拒绝，却成功了：%v", resp.StatusCode)
	}
	if !errors.Is(err, ErrPrivateAddress) {
		t.Errorf("应返回 ErrPrivateAddress，得到 %v", err)
	}

	// 测试闸门打开后能连（httptest 就在 127.0.0.1）
	okClient := NewSafeHTTPClient(true, 2*time.Second)
	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp2, err := okClient.Do(req2)
	if err != nil {
		t.Fatalf("allowPrivate 时应能连：%v", err)
	}
	resp2.Body.Close()
}

// 重定向到内网：初始 URL 是公网不行就在本地模拟——用允许私网的客户端连一个
// 「立刻 302 到 127.0.0.1」的服务，再换成禁止私网的客户端时，
// 第一跳（本地服务）就会被拦，等价证明「每一跳都走同一道闸」。
func TestSafeClientRedirectStillGuarded(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("inner"))
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	client := NewSafeHTTPClient(false, 2*time.Second)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, redirector.URL, nil)
	if _, err := client.Do(req); !errors.Is(err, ErrPrivateAddress) {
		t.Errorf("重定向链上的内网地址应被拦，得到 %v", err)
	}
}

func TestIsDisallowedIP(t *testing.T) {
	bad := []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.9",
		"169.254.169.254", "0.0.0.0", "::1", "fe80::1", "224.0.0.1"}
	for _, s := range bad {
		if ip := parseIP(s); !isDisallowedIP(ip) {
			t.Errorf("%s 应被判为不允许", s)
		}
	}
	// 公网示例地址应允许
	if isDisallowedIP(parseIP("1.1.1.1")) || isDisallowedIP(parseIP("2606:4700:4700::1111")) {
		t.Error("公网地址不该被拦")
	}
}
