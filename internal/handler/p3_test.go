package handler

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
)

// P3 handler 测试：申请校验/判重/队列容量、图床未配置语义、内部令牌。
// 真实 S3/SMTP/GitHub 的联调走生产只读与手工验证（见 docs/pitfalls.md）。

func TestApplyValidationAndDuplicate(t *testing.T) {
	a, st := testHandlerApp(t)
	// 空提交：逐字段 400
	body := bytes.NewBufferString(`{}`)
	status, obj := doAuth(t, a, "POST", "/links/applications/apply", body, jsonHeader())
	if status != 400 {
		t.Fatalf("空申请应 400，得到 %d", status)
	}
	if obj["error"].(map[string]any)["code"] != "validation.invalid_field" {
		t.Errorf("code = %v", obj["error"])
	}

	// 正常申请：200
	payload := `{"name":"测试站","link":"https://x.example","avatar":"https://x.example/a.png","descr":"介绍","email":"a@b.cc","color":"#f08","type":0}`
	status, raw, err := a.TestReq("POST", "/links/applications/apply", bytes.NewBufferString(payload), jsonHeader())
	if err != nil || status != 200 {
		t.Fatalf("正常申请应 200：%d %v %s", status, err, raw)
	}
	var resp map[string]any
	_ = json.Unmarshal(raw, &resp)
	data := resp["data"].(map[string]any)
	// 本地关闭外部依赖：两个 false
	if data["mailSent"] != false || data["screeningTriggered"] != false {
		t.Errorf("未配置外部依赖时应为 false：%v", data)
	}

	// 完全相同的重复提交：409 application.duplicate
	status, obj = doAuth(t, a, "POST", "/links/applications/apply", bytes.NewBufferString(payload), jsonHeader())
	if status != 409 {
		t.Fatalf("重复申请应 409：%d %v", status, obj)
	}
	if obj["error"].(map[string]any)["code"] != "application.duplicate" {
		t.Errorf("code = %v", obj["error"])
	}

	// 已收录网址：409
	st.SetGroups([]model.LinkGroup{{Name: "友达", Links: []model.Link{{ID: "1", URL: "https://taken.example"}}}})
	taken := `{"name":"已收录","link":"https://taken.example/","avatar":"https://x.example/a.png","email":"a@b.cc","type":0}`
	status, obj = doAuth(t, a, "POST", "/links/applications/apply", bytes.NewBufferString(taken), jsonHeader())
	if status != 409 {
		t.Fatalf("已收录网址应 409：%d %v", status, obj)
	}
}

func TestApplyQueueFull(t *testing.T) {
	a, st := testHandlerApp(t)
	// fake 里灌满 20 条 pending（config.Flink.PendingLimit 默认 20）
	pending := make([]model.LinkApplication, 0, 20)
	for i := 0; i < 20; i++ {
		pending = append(pending, model.LinkApplication{
			Type: 0, Name: "站" + string(rune('a'+i)), Link: "https://q" + string(rune('a'+i)) + ".example",
			Status: model.ApplicationStatusPending, CreatedAt: time.Unix(int64(1750000000+i), 0),
		})
	}
	for _, p := range pending {
		if _, err := st.Applications().Create(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	body := bytes.NewBufferString(`{"name":"新站","link":"https://new.example","avatar":"https://x.example/a.png","email":"a@b.cc","type":0}`)
	status, obj := doAuth(t, a, "POST", "/links/applications/apply", body, jsonHeader())
	if status != 429 {
		t.Fatalf("队列满应 429：%d %v", status, obj)
	}
	if obj["error"].(map[string]any)["code"] != "application.full" {
		t.Errorf("code = %v", obj["error"])
	}
}

func TestImagesNotConfigured(t *testing.T) {
	// 测试配置 media.enabled=false → 503 语义（契约错误码 upstream.failed）
	a, st := testHandlerApp(t)
	seedUsers(st)
	_, loginResp := login(t, a, testUser, testPassword)
	token := loginResp["data"].(map[string]any)["token"].(string)

	status, obj := doAuth(t, a, "GET", "/images/list", nil, map[string]string{"Authorization": token})
	if status != 502 {
		t.Fatalf("未配置图床应 upstream.failed(502)：%d %v", status, obj)
	}
	if obj["error"].(map[string]any)["code"] != "upstream.failed" {
		t.Errorf("code = %v", obj["error"])
	}
	if !strings.Contains(obj["error"].(map[string]any)["message"].(string), "图床未配置") {
		t.Errorf("文案要说清未配置：%v", obj["error"])
	}
}

func TestScreeningInternalToken(t *testing.T) {
	a, st := testHandlerApp(t)
	id, err := st.Applications().Create(t.Context(), model.LinkApplication{
		Type: 0, Name: "初筛站", Link: "https://s.example", Status: model.ApplicationStatusPending,
		CreatedAt: time.Unix(1750000000, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"id":"` + id + `","verdict":"pass","summary":"ok","model":"m"}`

	// 无令牌：401
	status, obj := doAuth(t, a, "POST", "/links/applications/screening", bytes.NewBufferString(payload), jsonHeader())
	if status != 401 {
		t.Fatalf("无内部令牌应 401：%d", status)
	}
	// 错令牌：401
	status, _ = doAuth(t, a, "POST", "/links/applications/screening", bytes.NewBufferString(payload),
		map[string]string{"Content-Type": "application/json", "X-Internal-Token": "wrong"})
	if status != 401 {
		t.Fatalf("错令牌应 401：%d", status)
	}
	// 正确令牌：200 + 落库
	h := map[string]string{"Content-Type": "application/json", "X-Internal-Token": "test-secret"}
	status, raw, err := a.TestReq("POST", "/links/applications/screening", bytes.NewBufferString(payload), h)
	if err != nil || status != 200 {
		t.Fatalf("正确令牌应 200：%d %v %s", status, err, raw)
	}
	app, _ := st.Applications().ByID(t.Context(), id)
	if app == nil || app.AIScreening == nil || app.AIScreening.Verdict != "pass" {
		t.Errorf("初筛结果未落库：%+v", app)
	}
	// 非法 verdict：400
	bad := `{"id":"` + id + `","verdict":"meh"}`
	status, obj = doAuth(t, a, "POST", "/links/applications/screening", bytes.NewBufferString(bad), h)
	if status != 400 {
		t.Fatalf("非法 verdict 应 400：%d %v", status, obj)
	}
}

func jsonHeader() map[string]string {
	return map[string]string{"Content-Type": "application/json"}
}
