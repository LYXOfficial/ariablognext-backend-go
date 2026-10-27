package platform

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// ErrPrivateAddress 表示目标解析到了内网/环回/链路本地地址（SSRF 拦截）。
var ErrPrivateAddress = errors.New("目标地址指向内网，已拒绝")

// isDisallowedIP 判定不允许连的地址段：环回、私网、链路本地（含云元数据 169.254.169.254）、
// 未指定地址、以及 IPv4 映射的 IPv6 形式。
func isDisallowedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// NewSafeHTTPClient 返回一个「真正连出去之前先校验 IP」的 HTTP 客户端。
//
// 为什么必须在 dial 阶段校验，而不是只校验 URL：
//  1. **重定向**：公网 URL 可以 302 到 169.254.169.254（云元数据）——只验初始 URL 会被绕过；
//  2. **DNS rebinding**：校验时解析到公网 IP、真正连接时解析到内网 IP，
//     两次解析之间被换掉（TOCTOU）；
//  3. **fail-open**：旧写法 `if ips, err := LookupIP(...); err == nil { … }` 在解析失败时
//     直接跳过校验，等于放行。
//
// 做法：在 DialContext 里解析目标主机，把**所有**解析结果都过一遍黑名单，
// 然后直接连到已校验的 IP（把校验和连接绑定，消除 TOCTOU）。
// 重定向会自动复用同一个 Transport，因此每一跳都受同样保护。
//
// allowPrivate 只允许测试打开（httptest 监听在 127.0.0.1）；生产调用一律 false。
func NewSafeHTTPClient(allowPrivate bool, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		Proxy: nil, // 不走环境代理：代理会让「连到哪」脱离本函数的校验
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if ip := net.ParseIP(host); ip != nil {
				// 直接就是 IP 字面量：校验它本身
				if !allowPrivate && isDisallowedIP(ip) {
					return nil, ErrPrivateAddress
				}
				return dialer.DialContext(ctx, network, addr)
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				// 解析不了就不连（fail-closed），不要把错误当成「可以放行」
				return nil, fmt.Errorf("解析主机 %s 失败：%w", host, err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("主机 %s 没有解析到任何地址", host)
			}
			var chosen net.IP
			for _, cand := range ips {
				if !allowPrivate && isDisallowedIP(cand.IP) {
					return nil, ErrPrivateAddress
				}
				// 优先 IPv4（IPv6 在部分网络会先超时）
				if chosen == nil || (chosen.To4() == nil && cand.IP.To4() != nil) {
					chosen = cand.IP
				}
			}
			// 连到「已校验过」的那个 IP，而不是再交给 DNS 解析一次
			return dialer.DialContext(ctx, network, net.JoinHostPort(chosen.String(), port))
		},
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          4,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// 每一跳的地址仍由上面的 DialContext 校验；这里只限制跳数
			if len(via) >= 3 {
				return fmt.Errorf("重定向超过 3 次")
			}
			return nil
		},
	}
}
