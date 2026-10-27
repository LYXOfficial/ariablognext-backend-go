package service

// StatusService 支撑 GET /status：并发探测自身与各外部依赖，3 秒内必须给结论。
// 只做「可达性」不做「正确性」——不发真实业务请求、不发邮件、不耗 LLM 额度。

import (
	"context"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/platform"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

// version 是构建注入点（-ldflags "-X .../service.version=261027-abc1234"）。
// 未注入时 versionLabel 回退读 go build 嵌的 VCS 信息。
var version = "dev"

// versionLabel 返回「提交日期-短hash」形式的版本号（如 261027-abc1234）。
// 日期按配置时区（上海）取提交时刻；Vercel 从 git 检出构建，buildvcs 通常有值，
// 但不能依赖它——三级回退：ldflags → buildinfo → dev。
func VersionLabel(cfg *config.Config) string {
	if version != "dev" {
		return version
	}
	bi, ok := debug.ReadBuildInfo()
	if ok {
		var rev, commitTime string
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.time":
				commitTime = s.Value
			}
		}
		if rev != "" {
			short := rev
			if len(short) > 7 {
				short = short[:7]
			}
			if t, err := time.Parse(time.RFC3339, commitTime); err == nil {
				return t.In(cfg.Location()).Format("060102") + "-" + short
			}
			return short
		}
	}
	return "dev"
}

// probeTimeout 是单个探测的硬上限：状态页必须快，宁报 down 不可吊死。
const probeTimeout = 3 * time.Second

// Status 是单个依赖的探测结论。status 三态：up / down / disabled。
type Status struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	LatencyMS int64  `json:"latencyMS"`
	Detail    string `json:"detail,omitempty"`
}

// StatusReport 是 /status 的 data 整体。RouteCount 由 handler 填（service 不知道路由表）。
type StatusReport struct {
	Version       string               `json:"version"`
	ReadOnly      bool                 `json:"readOnly"`
	UptimeSeconds int64                `json:"uptimeSeconds"`
	GoVersion     string               `json:"goVersion"`
	Goroutines    int                  `json:"goroutines"`
	HeapMB        float64              `json:"heapMB"`
	RouteCount    int                  `json:"routeCount"`
	Stack         []platform.StackItem `json:"stack"`
	Checks        []Status             `json:"checks"`
}

type StatusService struct {
	cfg   *config.Config
	st    store.Store
	llm   *platform.LLM
	start time.Time

	// S3 客户端惰性建一次：NewS3 只组装不连网，但也没必要在启动路径上做
	s3Once sync.Once
	s3     *platform.S3
	s3Err  error
}

func NewStatusService(cfg *config.Config, st store.Store) *StatusService {
	return &StatusService{
		cfg:   cfg,
		st:    st,
		llm:   platform.NewLLM(cfg.AI.Summary),
		start: time.Now(),
	}
}

func (s *StatusService) s3Client() (*platform.S3, error) {
	s.s3Once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.s3, s.s3Err = platform.NewS3(ctx, s.cfg.Media.S3)
	})
	return s.s3, s.s3Err
}

// Probe 并发跑全部探测。总耗时 ≈ 最慢一项（≤3s），不是累加。
func (s *StatusService) Probe(_ context.Context) *StatusReport {
	checks := make([]Status, 5)
	var wg sync.WaitGroup
	run := func(i int, name string, fn func(ctx context.Context) (string, error)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
			defer cancel()
			t0 := time.Now()
			detail, err := fn(ctx)
			st := "up"
			if err != nil {
				st = "down"
				if detail == "" {
					detail = err.Error()
				}
			}
			checks[i] = Status{Name: name, Status: st,
				LatencyMS: time.Since(t0).Milliseconds(), Detail: detail}
		}()
	}

	run(0, "MongoDB", func(ctx context.Context) (string, error) {
		return "", s.st.Ping(ctx)
	})
	if s.cfg.Media.Enabled {
		run(1, "图床 S3", func(ctx context.Context) (string, error) {
			client, err := s.s3Client()
			if err != nil {
				return "", err
			}
			return "", client.Ping(ctx)
		})
	} else {
		checks[1] = Status{Name: "图床 S3", Status: "disabled", Detail: "config.media.enabled=false"}
	}
	if mailer := platform.NewMailer(s.cfg.Mail); mailer.Configured() {
		run(2, "邮件 SMTP", func(ctx context.Context) (string, error) {
			return "", mailer.Ping(ctx)
		})
	} else {
		checks[2] = Status{Name: "邮件 SMTP", Status: "disabled", Detail: "SMTP 未配置"}
	}
	if s.llm.Enabled() {
		run(3, "LLM 摘要", func(ctx context.Context) (string, error) {
			return "", s.llm.Ping(ctx)
		})
	} else {
		checks[3] = Status{Name: "LLM 摘要", Status: "disabled", Detail: "ai.summary.enabled=false"}
	}
	if s.cfg.Flink.Screening.Enabled {
		run(4, "GitHub 初筛", func(ctx context.Context) (string, error) {
			return "", platform.PingGitHubRateLimit(ctx, s.cfg.Flink.Screening.Token)
		})
	} else {
		checks[4] = Status{Name: "GitHub 初筛", Status: "disabled", Detail: "flink.screening.enabled=false"}
	}
	wg.Wait()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return &StatusReport{
		Version:       VersionLabel(s.cfg),
		ReadOnly:      s.cfg.Database.ReadOnly,
		UptimeSeconds: int64(time.Since(s.start).Seconds()),
		GoVersion:     runtime.Version(),
		Goroutines:    runtime.NumGoroutine(),
		HeapMB:        float64(ms.HeapAlloc) / 1024 / 1024,
		Stack:         platform.StackVersions(),
		Checks:        checks,
	}
}
