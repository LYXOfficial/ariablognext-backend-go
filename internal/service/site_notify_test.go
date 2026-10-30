package service

// 博客侧缓存失效通知的单测：只验证「发到哪、发几次、失败不阻塞」，
// 不发真实请求（httptest 本地起一个假博客）。

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
)

func TestSiteNotify_PathsAndNoEmpty(t *testing.T) {
	paths := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.EscapedPath()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc := &SiteService{cfg: &config.Config{
		Site: config.SiteConfig{RefreshURL: srv.URL + "/"},
	}}
	// 空 kind 必须被跳过，不能变成对博客根路径的无效请求
	svc.Notify("posts", "/post/"+url.PathEscape("a/b"), "", "speaks")

	got := map[string]bool{}
	for i := 0; i < 3; i++ {
		select {
		case p := <-paths:
			got[p] = true
		case <-time.After(2 * time.Second):
			t.Fatal("等待缓存失效请求超时")
		}
	}
	for _, want := range []string{"/posts", "/post/a%2Fb", "/speaks"} {
		if !got[want] {
			t.Errorf("缺少请求 %q，实际 %v", want, got)
		}
	}
	// 不应该有第 4 个请求（空 kind 被跳过）
	select {
	case p := <-paths:
		t.Errorf("出现了多余请求 %q", p)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSiteNotify_NoConfigIsNoop(t *testing.T) {
	// cfg 为空 / RefreshURL 为空时不能 panic，也不能发请求
	(&SiteService{}).Notify("posts")
	(&SiteService{cfg: &config.Config{}}).Notify("posts")
}
