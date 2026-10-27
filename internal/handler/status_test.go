package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/service"
)

// /status：统一信封 200，data 里有版本号与探测结论。
// 测试配置里外部服务全部 disabled，只有 MongoDB 用 fake store 应为 up。
func TestStatusEnvelope(t *testing.T) {
	a, _ := testHandlerApp(t)
	status, body := do(t, a, "GET", "/status")
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	if body["ok"] != true {
		t.Fatalf("ok 应为 true: %v", body)
	}
	raw, err := json.Marshal(body["data"])
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Version    string `json:"version"`
		RouteCount int    `json:"routeCount"`
		GoVersion  string `json:"goVersion"`
		Checks     []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if data.Version == "" {
		t.Error("version 不应为空")
	}
	if data.RouteCount == 0 {
		t.Error("routeCount 不应为 0")
	}
	if data.GoVersion == "" {
		t.Error("goVersion 不应为空")
	}
	if len(data.Checks) == 0 {
		t.Fatal("checks 不应为空")
	}
	var dbUp, sawOthers bool
	for _, c := range data.Checks {
		switch {
		case c.Name == "MongoDB" && c.Status == "up":
			dbUp = true
		case c.Status == "disabled":
			sawOthers = true
		case c.Status == "down":
			t.Errorf("测试配置下不应有 down（除非探测出网）：%+v", c)
		}
	}
	if !dbUp {
		t.Error("fake store 下 MongoDB 应为 up")
	}
	if !sawOthers {
		t.Error("测试配置下其余依赖应为 disabled")
	}
}

// 版本号格式：ldflags 注入优先；buildinfo 回退应产出 dev 或日期-短hash。
func TestVersionLabelShape(t *testing.T) {
	v := service.VersionLabel(handlerTestConfig())
	if v == "" {
		t.Fatal("版本号不应为空")
	}
	if v == "dev" {
		return // 无 VCS 信息的构建环境，回退合法
	}
	parts := strings.SplitN(v, "-", 2)
	if len(parts) != 2 || len(parts[0]) != 6 || len(parts[1]) != 7 {
		t.Errorf("版本号应是 YYMMDD-短hash 形式，得到 %q", v)
	}
}
