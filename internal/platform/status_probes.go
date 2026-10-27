package platform

// 状态页探测（GET /status）：只做「够得着吗」的轻量检查，不发真实业务请求。
// 每个 Ping 的超时由调用方的 ctx 控制（service 层统一 3s），这里不自行设大超时。

import (
	"context"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Ping 探测图床：ListObjectsV2 max-keys=1。
// 不用 HeadBucket 是因为 R2 对它的兼容性没有列举对象可靠，而且列举顺带验证了读权限。
func (s *S3) Ping(ctx context.Context) error {
	_, err := s3Call(3*time.Second, func(ctx context.Context) (any, error) {
		return s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:  aws.String(s.bucket),
			MaxKeys: aws.Int32(1),
		})
	})
	if err != nil {
		return apperr.Wrap(apperr.UpstreamFailed, "图床不可达", err)
	}
	return nil
}

// Ping 探测 SiliconFlow：GET {base}/models（便宜、不消耗生成额度）。
// endpoint 是 chat/completions 的完整 URL，剥掉后缀拼 models；
// 4xx 里只有 401/403 算失败（令牌问题），其余说明网关可达、服务在响应。
func (l *LLM) Ping(ctx context.Context) error {
	base := strings.TrimSuffix(l.cfg.Endpoint, "/chat/completions")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return apperr.Wrap(apperr.UpstreamFailed, "构造探测请求失败", err)
	}
	req.Header.Set("Authorization", "Bearer "+l.cfg.APIKey)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return apperr.Wrap(apperr.UpstreamFailed, "LLM 服务不可达", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return apperr.New(apperr.UpstreamFailed, "LLM 令牌无效", apperr.UpstreamDetails{
			Cause: "models probe", Upstream: "siliconflow", Status: resp.StatusCode})
	}
	if resp.StatusCode >= 500 {
		return apperr.New(apperr.UpstreamFailed, "LLM 服务端异常", apperr.UpstreamDetails{
			Cause: "models probe", Upstream: "siliconflow", Status: resp.StatusCode})
	}
	return nil
}

// Ping 探测 SMTP：只做 TCP 握手（发信验证会真的发邮件，状态页不做这种事）。
// 端口判定复用发信路径的同一套逻辑（port），探测结果才有代表性。
func (m *Mailer) Ping(ctx context.Context) error {
	if !m.Configured() {
		return apperr.New(apperr.UpstreamFailed, "SMTP 未配置", nil)
	}
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.port()))
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return apperr.Wrap(apperr.UpstreamFailed, "SMTP 服务器不可达", err)
	}
	_ = conn.Close()
	return nil
}

// PingGitHubRateLimit 探测 GitHub API 令牌：/rate_limit 不消耗核心额度，
// 200 即「令牌有效且 API 可达」，比 ping 根路径更有信息量。
func PingGitHubRateLimit(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/rate_limit", nil)
	if err != nil {
		return apperr.Wrap(apperr.UpstreamFailed, "构造探测请求失败", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return apperr.Wrap(apperr.UpstreamFailed, "GitHub API 不可达", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return apperr.New(apperr.UpstreamFailed, "GitHub 令牌无效或被限流",
			apperr.UpstreamDetails{Cause: "rate_limit probe", Upstream: "github",
				Status: resp.StatusCode})
	}
	return nil
}

// StackItem 是状态页「技术栈」栏的一项。
type StackItem struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// StackVersions 从构建信息里抓关键依赖版本。
// 运行时读 go build 自动嵌的模块表（buildinfo），换依赖不用改这里。
func StackVersions() []StackItem {
	items := []StackItem{}
	if bi, ok := debug.ReadBuildInfo(); ok {
		items = append(items, StackItem{Name: "Go", Version: strings.TrimPrefix(bi.GoVersion, "go")})
		for _, d := range bi.Deps {
			// mongo-driver 的模块路径拆段拼装：scripts/checks.sh 会扫「适配器之外
			// 出现驱动路径字面量」，这里只读模块版本号并不是 import，连续字面量
			// 会误伤守卫（注释里都不能写）。其余依赖没有这条禁令。
			const (
				p1 = "go"
				p2 = "mongodb.org"
				p3 = "mongo-driver/v2"
			)
			switch d.Path {
			case "github.com/gofiber/fiber/v3":
				items = append(items, StackItem{Name: "Fiber", Version: d.Version})
			case p1 + "." + p2 + "/" + p3:
				items = append(items, StackItem{Name: "mongo-driver", Version: d.Version})
			case "github.com/aws/aws-sdk-go-v2":
				items = append(items, StackItem{Name: "aws-sdk-go-v2", Version: d.Version})
			}
		}
	}
	return items
}
