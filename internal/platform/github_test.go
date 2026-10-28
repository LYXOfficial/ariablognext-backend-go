package platform

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
)

// withGitHubBase 把 githubAPIBase 指向假服务器，defer 恢复。
func withGitHubBase(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	old := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() {
		githubAPIBase = old
		srv.Close()
	})
	return srv
}

// repository_dispatch：路径、事件名、payload、鉴权头都要对。
func TestDispatchEvent(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	withGitHubBase(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusNoContent)
	})

	if err := DispatchEvent(context.Background(), "o/r", "tok", "flink-screening",
		map[string]any{"id": "abc"}); err != nil {
		t.Fatalf("dispatch 失败: %v", err)
	}
	if gotPath != "/repos/o/r/dispatches" {
		t.Errorf("路径错误: %s", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("鉴权头错误: %s", gotAuth)
	}
	if gotBody["event_type"] != "flink-screening" {
		t.Errorf("event_type 错误: %v", gotBody["event_type"])
	}
	cp, _ := gotBody["client_payload"].(map[string]any)
	if cp["id"] != "abc" {
		t.Errorf("client_payload 错误: %v", gotBody)
	}
}

// 非 204 → upstream.failed，details 带真实 HTTP 状态。
func TestDispatchEventError(t *testing.T) {
	withGitHubBase(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	})
	err := DispatchEvent(context.Background(), "o/r", "tok", "flink-screening", nil)
	if err == nil {
		t.Fatal("应返回错误")
	}
	details, ok := apperr.From(err).Details().(apperr.UpstreamDetails)
	if !ok || details.Status != http.StatusUnprocessableEntity {
		t.Errorf("details 应带 422，得到 %#v", apperr.From(err).Details())
	}
}

// 配置缺项时不发请求，直接报错（eventType 是新增的必填项）。
func TestDispatchEventMissingConfig(t *testing.T) {
	called := false
	withGitHubBase(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	if err := DispatchEvent(context.Background(), "o/r", "tok", "", nil); err == nil {
		t.Error("eventType 为空应报错")
	}
	if called {
		t.Error("配置不全时不应发出请求")
	}
}

// workflow_dispatch：路径按 workflow 文件名，ref 缺省 main。
func TestDispatchWorkflow(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	withGitHubBase(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusNoContent)
	})

	if err := DispatchWorkflow(context.Background(), "o/check", "tok", "check_links.yml", ""); err != nil {
		t.Fatalf("dispatch 失败: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/actions/workflows/check_links.yml/dispatches") {
		t.Errorf("路径错误: %s", gotPath)
	}
	if gotBody["ref"] != "main" {
		t.Errorf("ref 缺省应为 main: %v", gotBody)
	}
}
