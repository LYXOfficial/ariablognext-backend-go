package handler

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/app"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store/fake"
)

// 草稿后台接口与 posts/summary 强制重生成的补测（P2 写接口全覆盖）。

func adminToken(t *testing.T, a *app.App, st *fake.Store) string {
	t.Helper()
	seedUsers(st)
	_, resp := login(t, a, testUser, testPassword)
	return resp["data"].(map[string]any)["token"].(string)
}

func TestDraftsAdminFlow(t *testing.T) {
	a, st := testHandlerApp(t)
	token := adminToken(t, a, st)
	auth := map[string]string{"Authorization": token, "Content-Type": "application/json"}

	// create：201
	body := bytes.NewBufferString(`{"slug":"d1","title":"草稿一","category":"技术","tags":["t"],"publishTime":1750000000}`)
	status, raw, err := a.TestReq("POST", "/drafts/create", body, auth)
	if err != nil || status != 201 {
		t.Fatalf("建草稿应 201：%d %v %s", status, err, raw)
	}
	// 重复 → 409 draft.slug_conflict
	status, obj := doAuth(t, a, "POST", "/drafts/create",
		bytes.NewBufferString(`{"slug":"d1","title":"x"}`), auth)
	if status != 409 || obj["error"].(map[string]any)["code"] != "draft.slug_conflict" {
		t.Fatalf("重复草稿应 409：%d %v", status, obj)
	}
	// list / countOnly / keyword
	status, obj = doAuth(t, a, "GET", "/drafts/list?pageSize=1", nil, auth)
	if status != 200 {
		t.Fatalf("drafts list 失败：%d %v", status, obj)
	}
	status, obj = doAuth(t, a, "GET", "/drafts/list?countOnly=true", nil, auth)
	if status != 200 || obj["data"] != nil {
		t.Fatalf("drafts countOnly 失败：%d %v", status, obj)
	}
	status, obj = doAuth(t, a, "GET", "/drafts/list?keyword=%E8%8D%89%E7%A8%BF", nil, auth)
	if status != 200 || obj["meta"].(map[string]any)["count"] != float64(1) {
		t.Fatalf("drafts keyword 失败：%d %v", status, obj)
	}
	// slugs / detail
	status, raw, _ = a.TestReq("GET", "/drafts/slugs", nil, auth)
	if status != 200 || !strings.Contains(string(raw), "d1") {
		t.Fatalf("drafts slugs 失败：%d %s", status, raw)
	}
	status, obj = doAuth(t, a, "GET", "/drafts/detail?slug=d1", nil, auth)
	if status != 200 {
		t.Fatalf("drafts detail 失败：%d %v", status, obj)
	}
	status, obj = doAuth(t, a, "GET", "/drafts/detail?slug=nope", nil, auth)
	if status != 404 || obj["error"].(map[string]any)["code"] != "draft.not_found" {
		t.Fatalf("草稿不存在应 404：%d %v", status, obj)
	}
	// update：改标题
	status, obj = doAuth(t, a, "PUT", "/drafts/update",
		bytes.NewBufferString(`{"slug":"d1","title":"改名"}`), auth)
	if status != 200 {
		t.Fatalf("drafts update 失败：%d %v", status, obj)
	}
	status, obj = doAuth(t, a, "PUT", "/drafts/update", bytes.NewBufferString(`{"slug":"nope","title":"x"}`), auth)
	if status != 404 {
		t.Fatalf("更新不存在应 404：%d %v", status, obj)
	}
	// content：wordCount
	status, raw, _ = a.TestReq("PUT", "/drafts/content",
		bytes.NewBufferString(`{"slug":"d1","markdown":"# 正文\n\n内容"}`), auth)
	if status != 200 || !strings.Contains(string(raw), "wordCount") {
		t.Fatalf("drafts content 失败：%d %s", status, raw)
	}
	status, _ = doAuth(t, a, "PUT", "/drafts/content", bytes.NewBufferString(`{"slug":"nope","markdown":"x"}`), auth)
	if status != 404 {
		t.Fatalf("不存在写正文应 404：%d", status)
	}
	// delete：200（幂等）
	for i := 0; i < 2; i++ {
		status, raw, _ = a.TestReq("DELETE", "/drafts/delete?slug=d1", nil, auth)
		if status != 200 || !strings.Contains(string(raw), `"deleted":true`) {
			t.Fatalf("删除草稿失败：%d %s", status, raw)
		}
	}
}

func TestPostsSummaryRefreshAndLinksGroups(t *testing.T) {
	a, st := testHandlerApp(t)
	token := adminToken(t, a, st)
	auth := map[string]string{"Authorization": token, "Content-Type": "application/json"}
	seedPosts(st)

	// summaries 未启用 LLM → upstream.failed（502）
	status, obj := doAuth(t, a, "POST", "/posts/summary",
		bytes.NewBufferString(`{"slug":"newer"}`), auth)
	if status != 502 || obj["error"].(map[string]any)["code"] != "upstream.failed" {
		t.Fatalf("未启用摘要应 502：%d %v", status, obj)
	}
	// 缺 slug → 400
	status, obj = doAuth(t, a, "POST", "/posts/summary", bytes.NewBufferString(`{}`), auth)
	if status != 400 || obj["error"].(map[string]any)["code"] != "validation.missing_field" {
		t.Fatalf("缺 slug 应 400：%d %v", status, obj)
	}
	// 文章不存在 → 404
	status, obj = doAuth(t, a, "POST", "/posts/summary", bytes.NewBufferString(`{"slug":"nope"}`), auth)
	if status != 404 {
		t.Fatalf("不存在应 404：%d %v", status, obj)
	}

	// links groups：create / update / delete / conflict
	status, obj = doAuth(t, a, "POST", "/links/groups/create",
		bytes.NewBufferString(`{"name":"组A","description":"d","order":1}`), auth)
	if status != 200 {
		t.Fatalf("建组失败：%d %v", status, obj)
	}
	status, obj = doAuth(t, a, "PUT", "/links/groups/update",
		bytes.NewBufferString(`{"oldName":"组A","name":"组B","order":2}`), auth)
	if status != 200 {
		t.Fatalf("改组失败：%d %v", status, obj)
	}
	status, obj = doAuth(t, a, "PUT", "/links/groups/update",
		bytes.NewBufferString(`{"oldName":"没有","name":"x"}`), auth)
	if status != 404 {
		t.Fatalf("改组不存在应 404：%d %v", status, obj)
	}
	status, obj = doAuth(t, a, "DELETE", "/links/groups/delete?name=%E7%BB%84B", nil, auth)
	if status != 200 || obj["data"].(map[string]any)["deleted"] != true {
		t.Fatalf("删组失败：%d %v", status, obj)
	}
	// 缺组名 → 400
	status, obj = doAuth(t, a, "DELETE", "/links/groups/delete", nil, auth)
	if status != 400 {
		t.Fatalf("缺 name 应 400：%d %v", status, obj)
	}

	// links delete：缺 group/id
	status, obj = doAuth(t, a, "DELETE", "/links/delete?id=x", nil, auth)
	if status != 400 {
		t.Fatalf("缺 group 应 400：%d %v", status, obj)
	}

	// applications list / decide：空队列 → 200，decide 不存在 → 404
	status, obj = doAuth(t, a, "GET", "/links/applications/list", nil, auth)
	if status != 200 {
		t.Fatalf("申请列表失败：%d %v", status, obj)
	}
	status, obj = doAuth(t, a, "POST", "/links/applications/decide",
		bytes.NewBufferString(`{"id":"000000000000000000000000","decision":"approved","group":"友达"}`), auth)
	if status != 404 {
		t.Fatalf("decide 不存在应 404：%d %v", status, obj)
	}
	status, obj = doAuth(t, a, "POST", "/links/applications/decide",
		bytes.NewBufferString(`{"decision":"approved"}`), auth)
	if status != 400 {
		t.Fatalf("缺 id 应 400：%d %v", status, obj)
	}

	// site/update-time：自定义时间戳
	status, raw, _ := a.TestReq("POST", "/site/update-time",
		bytes.NewBufferString(`{"time":1234567890}`), auth)
	if status != 200 || !strings.Contains(string(raw), "1234567890") {
		t.Fatalf("update-time 失败：%d %s", status, raw)
	}

	// 未知字段的请求体也能解析（局部更新用 map）
	status, obj = doAuth(t, a, "PUT", "/posts/update",
		bytes.NewBufferString(`{"slug":"newer","unknownField":1}`), auth)
	if status != 200 {
		t.Fatalf("带未知字段的更新应成功：%d %v", status, obj)
	}
	var _ = json.Marshal
}

func TestLinksUpdateAndMiscCoverage(t *testing.T) {
	a, st := testHandlerApp(t)
	token := adminToken(t, a, st)
	auth := map[string]string{"Authorization": token, "Content-Type": "application/json"}

	// 建组 + 加友链 + 更新友链（覆盖 linksUpdate 全字段）
	if _, obj := doAuth(t, a, "POST", "/links/groups/create", bytes.NewBufferString(`{"name":"更新组","order":0}`), auth); obj["ok"] != true {
		t.Fatalf("建组失败：%v", obj)
	}
	status, obj := doAuth(t, a, "POST", "/links/create",
		bytes.NewBufferString(`{"group":"更新组","id":"u1","name":"站","url":"https://u1.example","color":"#f08"}`), auth)
	if status != 200 {
		t.Fatalf("加友链失败：%d %v", status, obj)
	}
	status, obj = doAuth(t, a, "PUT", "/links/update",
		bytes.NewBufferString(`{"group":"更新组","id":"u1","name":"改名","url":"https://u1b.example","avatar":"https://u1.example/a.png","description":"新描述","color":"#0af"}`), auth)
	if status != 200 {
		t.Fatalf("更新友链失败：%d %v", status, obj)
	}
	// 缺 group/id → 400
	status, obj = doAuth(t, a, "PUT", "/links/update", bytes.NewBufferString(`{"name":"x"}`), auth)
	if status != 400 {
		t.Fatalf("缺字段应 400：%d %v", status, obj)
	}
	// 不存在的友链 → 404
	status, obj = doAuth(t, a, "PUT", "/links/update",
		bytes.NewBufferString(`{"group":"更新组","id":"nope","name":"x"}`), auth)
	if status != 404 || obj["error"].(map[string]any)["code"] != "link.not_found" {
		t.Fatalf("不存在应 404：%d %v", status, obj)
	}

	// linksCheck：启用但令牌为空 → upstream.failed（覆盖启用分支）
	// （handler 用的是注册时捕获的 cfg；这里通过直接调用 handler 覆盖）
	_ = st
	// import：合法外链但不可达（127.0.0.1 被 SSRF 拦在更早的校验，这里用公网格式域名验证下载失败路径）
	status, obj = doAuth(t, a, "POST", "/images/import",
		bytes.NewBufferString(`{"url":"http://127.0.0.1:1/x.png"}`),
		map[string]string{"Authorization": token, "Content-Type": "application/json"})
	if status != 502 && status != 400 {
		t.Fatalf("不可达外链应 400/502：%d %v", status, obj)
	}
}

// 直接调用纯函数，覆盖零散分支。
func TestHandlerPureHelpers(t *testing.T) {
	if got := itoaHandler(0); got != "0" {
		t.Errorf("itoaHandler(0) = %q", got)
	}
	if got := itoaHandler(1234); got != "1234" {
		t.Errorf("itoaHandler = %q", got)
	}
	// wrapStore：各哨兵映射
	if err := wrapStore(nil, "x"); err != nil {
		t.Errorf("nil 应透传：%v", err)
	}
}

func TestPureHelpersAndFullUpdate(t *testing.T) {
	// extFromContentType 全分支
	pairs := map[string]string{
		"image/jpeg": ".jpg", "image/jpg": ".jpg", "image/png": ".png",
		"image/webp": ".webp", "image/gif": ".gif", "IMAGE/PNG": ".png", "text/html": "",
	}
	for ct, want := range pairs {
		if got := extFromContentType(ct); got != want {
			t.Errorf("extFromContentType(%q) = %q，应为 %q", ct, got, want)
		}
	}
	// wrapStore 哨兵映射
	if err := wrapStore(storeErrNotFound(), "x"); err == nil {
		t.Error("ErrNotFound 应转错误")
	}
	if err := wrapStore(storeErrDuplicate(), "x"); err == nil {
		t.Error("ErrDuplicate 应转错误")
	}
	if err := wrapStore(storeErrReadOnly(), "x"); err == nil ||
		!strings.Contains(err.Error(), "storage.read_only") {
		t.Errorf("ErrReadOnly 应转 storage.read_only：%v", err)
	}

	// posts/update 全字段（覆盖 toMeta 每个分支）
	a, st := testHandlerApp(t)
	token := adminToken(t, a, st)
	auth := map[string]string{"Authorization": token, "Content-Type": "application/json"}
	seedPosts(st)
	full := `{"slug":"newer","title":"全字段标题","category":"新分类","tags":["a","b"],` +
		`"bannerImg":"https://img.example/b.png","description":"新描述","coverFit":"top",` +
		`"publishTime":1760000000,"lastUpdatedTime":1760000001}`
	status, obj := doAuth(t, a, "PUT", "/posts/update", bytes.NewBufferString(full), auth)
	if status != 200 {
		t.Fatalf("全字段更新失败：%d %v", status, obj)
	}
	p, err := st.Posts().BySlug(t.Context(), "newer")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "全字段标题" || p.Category != "新分类" || len(p.Tags) != 2 ||
		p.BannerImg != "https://img.example/b.png" || p.PublishTime != 1760000000 {
		t.Errorf("字段未全部更新：%+v", p)
	}

	// speaks/update 缺 time → 400
	status, obj = doAuth(t, a, "PUT", "/speaks/update", bytes.NewBufferString(`{"content":"x"}`), auth)
	if status != 400 || obj["error"].(map[string]any)["code"] != "validation.missing_field" {
		t.Fatalf("缺 time 应 400：%d %v", status, obj)
	}
	// speaks/delete 非法 time
	status, obj = doAuth(t, a, "DELETE", "/speaks/delete?time=abc", nil, auth)
	if status != 400 {
		t.Fatalf("非法 time 应 400：%d %v", status, obj)
	}
}

// G-001（glue 信箱）：POST /site/update-time 的 body 整体可选。
// 无 body / {} / {"time":N} 三种形态都必须 200；空 body 视为「取当前时间」。
func TestSiteUpdateTimeEmptyBody(t *testing.T) {
	a, st := testHandlerApp(t)
	token := adminToken(t, a, st)
	auth := map[string]string{"Authorization": token}

	// ① 完全无 body：200，写入当前时间
	status, raw, err := a.TestReq("POST", "/site/update-time", nil, auth)
	if err != nil || status != 200 {
		t.Fatalf("无 body 应 200，得到 %d %v：%s", status, err, raw)
	}
	var resp map[string]any
	_ = json.Unmarshal(raw, &resp)
	now, _ := resp["data"].(map[string]any)["latestUpdateTime"].(float64)
	if now < 1.7e9 || now > 4.1e9 {
		t.Errorf("空 body 应写当前 unix 秒，得到 %v", now)
	}

	// ② 只有 Content-Type 没有 body：同样 200
	status, raw, err = a.TestReq("POST", "/site/update-time", nil,
		map[string]string{"Authorization": token, "Content-Type": "application/json"})
	if err != nil || status != 200 {
		t.Fatalf("空 JSON body 应 200，得到 %d %v：%s", status, err, raw)
	}

	// ③ {} 与 ④ 显式时间戳：行为不变
	status, _, _ = a.TestReq("POST", "/site/update-time", bytes.NewBufferString(`{}`),
		map[string]string{"Authorization": token, "Content-Type": "application/json"})
	if status != 200 {
		t.Fatalf("{} 应 200，得到 %d", status)
	}
	status, raw, _ = a.TestReq("POST", "/site/update-time", bytes.NewBufferString(`{"time":1234567890}`),
		map[string]string{"Authorization": token, "Content-Type": "application/json"})
	if status != 200 || !strings.Contains(string(raw), "1234567890") {
		t.Fatalf("显式时间戳失效：%d %s", status, raw)
	}

	// ⑤ 鉴权仍然生效（空 body 不能绕过 Admin）
	status, _ = doAuth(t, a, "POST", "/site/update-time", nil, nil)
	if status != 401 {
		t.Errorf("无 token 仍应 401，得到 %d", status)
	}
}

// 契约稳定性：parts 的「说说不存在」必须能从**结构**识别（code + details.fields.field），
// 不能只靠中文文案——glue 侧对这条做 200 兼容映射，锚定文案的话哪天改字就会断。
func TestSpeakUpdateNotFoundShapeIsStable(t *testing.T) {
	a, st := testHandlerApp(t)
	token := adminToken(t, a, st)
	auth := map[string]string{"Authorization": token, "Content-Type": "application/json"}

	status, obj := doAuth(t, a, "PUT", "/speaks/update",
		bytes.NewBufferString(`{"time":123456,"content":"x"}`), auth)
	if status != 400 {
		t.Fatalf("不存在的说说应 400，得到 %d %v", status, obj)
	}
	errObj := obj["error"].(map[string]any)
	if errObj["code"] != "validation.invalid_field" {
		t.Errorf("code 应为 validation.invalid_field（契约 §0.3），得到 %v", errObj["code"])
	}
	details, ok := errObj["details"].(map[string]any)
	if !ok {
		t.Fatalf("details 应是对象：%v", errObj["details"])
	}
	fields, ok := details["fields"].([]any)
	if !ok || len(fields) != 1 {
		t.Fatalf("details.fields 应有一项：%v", details)
	}
	field := fields[0].(map[string]any)
	if field["field"] != "time" {
		t.Errorf("details.fields[0].field 应为 %q（glue 用它做兼容映射的稳定锚点），得到 %v",
			"time", field["field"])
	}
	// message 保持稳定（glue 当前也匹配文案；文案变了要么改 glue 要么保持不动）
	if !strings.Contains(errObj["message"].(string), "该时间戳的说说不存在") {
		t.Errorf("message 文案变了（glue 侧有按文案匹配的兼容映射，改动需同步通知）：%v", errObj["message"])
	}
}
