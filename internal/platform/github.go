package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
)

// DispatchGitHub 触发 GitHub Actions 的 repository_dispatch。
// 初筛（flink-screening）与链路检查（check_links）都用它；
// 只是入队动作，8 秒超时（与旧后端 DISPATCH_TIMEOUT 一致），失败交由调用方静默降级。
func DispatchGitHub(ctx context.Context, repo, token, eventType string, payload map[string]any) error {
	if repo == "" || token == "" {
		return apperr.New(apperr.UpstreamFailed, "GitHub 仓库或令牌未配置", nil)
	}
	body, err := json.Marshal(map[string]any{
		"event_type":     eventType,
		"client_payload": payload,
	})
	if err != nil {
		return apperr.Wrap(apperr.UpstreamFailed, "构造 dispatch 请求失败", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("https://api.github.com/repos/%s/dispatches", repo),
		bytes.NewReader(body))
	if err != nil {
		return apperr.Wrap(apperr.UpstreamFailed, "构造 dispatch 请求失败", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return apperr.Wrap(apperr.UpstreamFailed, "触发 GitHub Actions 失败", err)
	}
	defer resp.Body.Close()
	// 204 = 入队成功；其它都按 upstream.failed 交 cause
	if resp.StatusCode != http.StatusNoContent {
		return apperr.New(apperr.UpstreamFailed,
			"GitHub 返回了意外状态 "+fmt.Sprint(resp.StatusCode),
			apperr.UpstreamDetails{Cause: "github dispatch", Upstream: "github", Status: resp.StatusCode})
	}
	return nil
}

// WorkflowDispatchEvent 是 workflow 名到触发方式的桥：check_links 用 workflow_dispatch。
// repository_dispatch 与 workflow_dispatch 的 endpoint 不同，分开两个函数。

// DispatchWorkflow 触发 workflow_dispatch 类工作流（链路检查用）。
func DispatchWorkflow(ctx context.Context, repo, token, workflowRef string, ref string) error {
	if repo == "" || token == "" {
		return apperr.New(apperr.UpstreamFailed, "GitHub 仓库或令牌未配置", nil)
	}
	if ref == "" {
		ref = "main"
	}
	body, _ := json.Marshal(map[string]any{"ref": ref})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("https://api.github.com/repos/%s/actions/workflows/%s/dispatches", repo, workflowRef),
		bytes.NewReader(body))
	if err != nil {
		return apperr.Wrap(apperr.UpstreamFailed, "构造 workflow dispatch 失败", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return apperr.Wrap(apperr.UpstreamFailed, "触发链路检查失败", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return apperr.New(apperr.UpstreamFailed,
			"GitHub 返回了意外状态 "+fmt.Sprint(resp.StatusCode),
			apperr.UpstreamDetails{Cause: "workflow dispatch", Upstream: "github", Status: resp.StatusCode})
	}
	return nil
}
