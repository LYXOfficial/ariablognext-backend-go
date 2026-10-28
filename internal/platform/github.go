// GitHub Actions 触发。两种触发方式的 endpoint 与语义不同，但鉴权、超时、
// 错误映射完全一样：
//   - repository_dispatch：事件名（event_type）由 workflow 的 on.types 匹配，可带 client_payload
//     → 初筛用它（workflow 就在本仓库，payload 精确投递给它）
//   - workflow_dispatch：按 workflow 文件名触发
//     → 链路检查用它（workflow 在外部仓库 lyxofficial/check-flink）
//
// 两者共用 dispatch() 一个实现：headers / 8s 超时 / 非 204 错误映射只写一遍。
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

// githubAPIBase 抽成包级变量：单测把它指向 httptest，不必打真实 GitHub。
var githubAPIBase = "https://api.github.com"

// dispatchTimeout 与旧后端 DISPATCH_TIMEOUT 一致：只是入队动作，8 秒足够。
const dispatchTimeout = 8 * time.Second

// dispatch 是两种 dispatch 的公共实现。endpoint 是相对 api.github.com 的路径。
func dispatch(ctx context.Context, endpoint, token string, payload map[string]any, cause string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return apperr.Wrap(apperr.UpstreamFailed, "构造 dispatch 请求失败", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubAPIBase+endpoint, bytes.NewReader(body))
	if err != nil {
		return apperr.Wrap(apperr.UpstreamFailed, "构造 dispatch 请求失败", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: dispatchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return apperr.Wrap(apperr.UpstreamFailed, "触发 GitHub Actions 失败", err)
	}
	defer resp.Body.Close()
	// 204 = 入队成功；其它状态一律交 cause，由调用方决定静默还是上报
	if resp.StatusCode != http.StatusNoContent {
		return apperr.New(apperr.UpstreamFailed,
			"GitHub 返回了意外状态 "+fmt.Sprint(resp.StatusCode),
			apperr.UpstreamDetails{Cause: cause, Upstream: "github", Status: resp.StatusCode})
	}
	return nil
}

// DispatchEvent 触发 repository_dispatch（初筛）。
// eventType 必须与 workflow 的 on.repository_dispatch.types 完全一致——
// 它现在是配置项（flink.screening.eventType），不再硬编码在 service 里。
func DispatchEvent(ctx context.Context, repo, token, eventType string, payload map[string]any) error {
	if repo == "" || token == "" || eventType == "" {
		return apperr.New(apperr.UpstreamFailed, "GitHub 仓库、令牌或事件名未配置", nil)
	}
	return dispatch(ctx, "/repos/"+repo+"/dispatches", token, map[string]any{
		"event_type":     eventType,
		"client_payload": payload,
	}, "repository_dispatch")
}

// DispatchWorkflow 触发 workflow_dispatch（链路检查，workflow 在外部仓库）。
func DispatchWorkflow(ctx context.Context, repo, token, workflowFile, ref string) error {
	if repo == "" || token == "" || workflowFile == "" {
		return apperr.New(apperr.UpstreamFailed, "GitHub 仓库、令牌或 workflow 名未配置", nil)
	}
	if ref == "" {
		ref = "main"
	}
	return dispatch(ctx, "/repos/"+repo+"/actions/workflows/"+workflowFile+"/dispatches", token,
		map[string]any{"ref": ref}, "workflow_dispatch")
}
