package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// GET 200 的 ETag/304 协商缓存：ETag 现算（实时）、命中 If-None-Match 回 304 零载荷。
// 管理端反复刷新全量列表的场景免掉整段传输，且绝不返回过期数据。
func TestRenderETagNotModified(t *testing.T) {
	a := newTestApp(t)
	a.Add(Route{Method: "GET", Path: "/etag", Handler: func(c *Ctx) (any, error) {
		return map[string]any{"v": "same"}, nil
	}})
	a.Add(Route{Method: "POST", Path: "/post-echo", Handler: func(c *Ctx) (any, error) {
		return map[string]any{"v": "same"}, nil
	}})
	a.Add(Route{Method: "GET", Path: "/theme", Handler: func(c *Ctx) (any, error) {
		return WithHeader{Data: map[string]any{"color": "#fff"},
			Header: map[string]string{"Cache-Control": "public, max-age=86400"}}, nil
	}})

	resp, err := a.Test(httptest.NewRequest(http.MethodGet, "/etag", nil))
	if err != nil {
		t.Fatal(err)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("GET 200 应带 ETag")
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("GET 200 应带 no-cache（浏览器每次验证、实时），得到 %q", cc)
	}

	// 带 If-None-Match 重放 → 304 + 无 body
	req := httptest.NewRequest(http.MethodGet, "/etag", nil)
	req.Header.Set("If-None-Match", etag)
	resp2, err := a.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != 304 {
		t.Errorf("匹配 If-None-Match 应 304，得到 %d", resp2.StatusCode)
	}
	if resp2.Header.Get("ETag") != etag {
		t.Errorf("304 应带原 ETag")
	}

	// 弱化前缀（W/）也要能匹配（浏览器可能回传弱化形式）
	req3 := httptest.NewRequest(http.MethodGet, "/etag", nil)
	req3.Header.Set("If-None-Match", "W/"+etag)
	resp3, err := a.Test(req3)
	if err != nil {
		t.Fatal(err)
	}
	if resp3.StatusCode != 304 {
		t.Errorf("W/ 前缀应匹配，得到 %d", resp3.StatusCode)
	}

	// POST 不做协商缓存（写路径语义不同）
	req4 := httptest.NewRequest(http.MethodPost, "/post-echo", nil)
	resp4, err := a.Test(req4)
	if err != nil {
		t.Fatal(err)
	}
	if resp4.StatusCode != 200 || resp4.Header.Get("ETag") != "" {
		t.Errorf("POST 不应带 ETag/304")
	}

	// handler 自带 Cache-Control（theme-color 的旧契约 86400）不被覆盖
	resp5, err := a.Test(httptest.NewRequest(http.MethodGet, "/theme", nil))
	if err != nil {
		t.Fatal(err)
	}
	if cc := resp5.Header.Get("Cache-Control"); cc != "public, max-age=86400" {
		t.Errorf("handler 自带的 Cache-Control 不应被覆盖：%q", cc)
	}
}
