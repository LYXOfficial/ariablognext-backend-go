package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/app"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store/fake"
)

// P2 写接口测试：正常、缺参、未授权、不存在（testing.md §2.7 每接口四态）。
// 登录/鉴权链路全部走真 JWT（fake 存储 + 种子用户）。

const testUser = "seedadmin"
const testPassword = "seed-admin-password"

func seedUsers(st *fake.Store) {
	// 与 mongo 同语义：user + sha256(password) 十六进制双条件匹配
	sum := sha256.Sum256([]byte(testPassword))
	st.SetUserCredentials(testUser, hex.EncodeToString(sum[:]))
}

func login(t *testing.T, a *app.App, user, password string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"user": user, "password": password})
	status, raw, err := a.TestReq("POST", "/auth/login", bytes.NewReader(body),
		map[string]string{"Content-Type": "application/json"})
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("登录响应不是 JSON：%s", raw)
	}
	return status, obj
}

func TestLoginAndProtectedRoutes(t *testing.T) {
	a, st := testHandlerApp(t)
	seedUsers(st)

	// 未带 token：后台接口 401 + auth.*
	status, obj := do(t, a, "GET", "/drafts/list")
	if status != 401 {
		t.Fatalf("不带 token 应 401，得到 %d", status)
	}
	if code := obj["error"].(map[string]any)["code"]; code != "auth.missing_token" {
		t.Errorf("code = %v", code)
	}

	// 错误密码：403（auth.locked 与密码错误同文案）
	status, obj = login(t, a, testUser, "wrong-password")
	if status != 403 {
		t.Fatalf("错误密码应 403，得到 %d", status)
	}
	if code := obj["error"].(map[string]any)["code"]; code != "auth.locked" {
		t.Errorf("code = %v", code)
	}

	// 正确登录：200 + token
	status, obj = login(t, a, testUser, testPassword)
	if status != 200 {
		t.Fatalf("正确登录应 200：%v", obj)
	}
	token, _ := obj["data"].(map[string]any)["token"].(string)
	if token == "" {
		t.Fatal("登录响应缺 token")
	}

	// 带 token：/auth/verify 200 且 user 正确
	status, raw, err := a.TestReq("GET", "/auth/verify", nil,
		map[string]string{"Authorization": "Bearer " + token})
	if err != nil || status != 200 {
		t.Fatalf("verify 应 200：status=%d err=%v", status, err)
	}
	var obj2 map[string]any
	_ = json.Unmarshal(raw, &obj2)
	data := obj2["data"].(map[string]any)
	if data["user"] != testUser {
		t.Errorf("verify user = %v", data["user"])
	}
	if exp, ok := data["exp"].(float64); !ok || exp < float64(time.Now().Unix()) {
		t.Errorf("exp 应是未来的 unix 秒：%v", data["exp"])
	}

	// 带 token 的后台列表：200
	status, raw, err = a.TestReq("GET", "/drafts/list?pageSize=1", nil,
		map[string]string{"Authorization": token})
	if err != nil || status != 200 {
		t.Fatalf("带 token drafts/list 应 200：status=%d err=%v", status, err)
	}
	_ = raw
}

func TestPostsWriteFlow(t *testing.T) {
	a, st := testHandlerApp(t)
	seedUsers(st)
	_, loginResp := login(t, a, testUser, testPassword)
	token := loginResp["data"].(map[string]any)["token"].(string)
	auth := map[string]string{"Authorization": token, "Content-Type": "application/json"}

	// 未授权创建：401
	body := bytes.NewBufferString(`{"slug":"x","title":"x"}`)
	status, obj := doAuth(t, a, "POST", "/posts/create", body, nil)
	if status != 401 {
		t.Fatalf("未授权创建应 401：%d %v", status, obj)
	}

	// 创建：201，data.slug
	body = bytes.NewBufferString(`{"slug":"new-post","title":"新文章","category":"技术","tags":["t"],"publishTime":1750000000,"lastUpdatedTime":1750000000}`)
	status, raw, err := a.TestReq("POST", "/posts/create", body, auth)
	if err != nil || status != 201 {
		t.Fatalf("创建应 201：status=%d err=%v raw=%s", status, err, raw)
	}
	var created map[string]any
	_ = json.Unmarshal(raw, &created)
	if created["data"].(map[string]any)["slug"] != "new-post" {
		t.Errorf("create data = %v", created["data"])
	}

	// 重复创建：409 post.slug_conflict
	body = bytes.NewBufferString(`{"slug":"new-post","title":"x"}`)
	status, obj = doAuth(t, a, "POST", "/posts/create", body, auth)
	if status != 409 {
		t.Fatalf("重复 slug 应 409：%d", status)
	}
	if code := obj["error"].(map[string]any)["code"]; code != "post.slug_conflict" {
		t.Errorf("code = %v", code)
	}

	// 更新元信息：200 updated
	body = bytes.NewBufferString(`{"slug":"new-post","title":"改过的标题"}`)
	status, raw, err = a.TestReq("PUT", "/posts/update", body, auth)
	if err != nil || status != 200 {
		t.Fatalf("更新应 200：%d %v", status, err)
	}
	if !strings.Contains(string(raw), `"updated":true`) {
		t.Errorf("updated 响应：%s", raw)
	}

	// 更新不存在的：404
	body = bytes.NewBufferString(`{"slug":"nope","title":"x"}`)
	status, obj = doAuth(t, a, "PUT", "/posts/update", body, auth)
	if status != 404 {
		t.Fatalf("更新不存在应 404：%d", status)
	}

	// 写正文：wordCount 重算
	body = bytes.NewBufferString(`{"slug":"new-post","markdown":"# 标题\n\n正文内容 Go 语言"}`)
	status, raw, err = a.TestReq("PUT", "/posts/content", body, auth)
	if err != nil || status != 200 {
		t.Fatalf("写正文应 200：%d %v", status, err)
	}
	var contentResp map[string]any
	_ = json.Unmarshal(raw, &contentResp)
	if wc := contentResp["data"].(map[string]any)["wordCount"]; wc == nil || wc.(float64) <= 0 {
		t.Errorf("wordCount 未重算：%v", contentResp["data"])
	}

	// 删除：200 deleted:true（幂等，删两次都 200）
	for i := 0; i < 2; i++ {
		status, raw, err = a.TestReq("DELETE", "/posts/delete?slug=new-post", nil, auth)
		if err != nil || status != 200 {
			t.Fatalf("删除应 200（幂等）：%d", status)
		}
		if !strings.Contains(string(raw), `"deleted":true`) {
			t.Errorf("deleted 响应：%s", raw)
		}
	}
	_ = st
}

func TestSpeaksWriteFlow(t *testing.T) {
	a, st := testHandlerApp(t)
	seedUsers(st)
	_, loginResp := login(t, a, testUser, testPassword)
	token := loginResp["data"].(map[string]any)["token"].(string)
	auth := map[string]string{"Authorization": token, "Content-Type": "application/json"}

	// 创建说说（不传 time：服务端兜底当前）
	body := bytes.NewBufferString(`{"content":"**新**说说"}`)
	status, raw, err := a.TestReq("POST", "/speaks/create", body, auth)
	if err != nil || status != 200 {
		t.Fatalf("创建说说应 200：%d %v", status, err)
	}
	// 更新：先查出 time
	status, raw, _ = a.TestReq("GET", "/speaks/list?pageSize=1", nil, nil)
	var list map[string]any
	_ = json.Unmarshal(raw, &list)
	sp := list["data"].([]any)[0].(map[string]any)
	when := sp["time"].(float64)
	// 更新与删除
	body = bytes.NewBufferString(`{"time":` + itoa64(when) + `,"content":"改"}`)
	status, _, err = a.TestReq("PUT", "/speaks/update", body, auth)
	if err != nil || status != 200 {
		t.Fatalf("更新说说应 200：%d", status)
	}
	status, _, err = a.TestReq("DELETE", "/speaks/delete?time="+itoa64(when), nil, auth)
	if err != nil || status != 200 {
		t.Fatalf("删除说说应 200：%d", status)
	}
	// 再删一次仍 200（幂等）
	status, _, _ = a.TestReq("DELETE", "/speaks/delete?time="+itoa64(when), nil, auth)
	if status != 200 {
		t.Fatalf("幂等删除应 200：%d", status)
	}
}

func TestLinksWriteFlow(t *testing.T) {
	a, st := testHandlerApp(t)
	seedUsers(st)
	_, loginResp := login(t, a, testUser, testPassword)
	token := loginResp["data"].(map[string]any)["token"].(string)
	auth := map[string]string{"Authorization": token, "Content-Type": "application/json"}

	// 新分组：重复名 409 link.group_conflict
	body := bytes.NewBufferString(`{"name":"测试组","description":"d","order":5}`)
	if status, _, err := a.TestReq("POST", "/links/groups/create", body, auth); err != nil || status != 200 {
		t.Fatalf("建组应 200：%d %v", status, err)
	}
	body = bytes.NewBufferString(`{"name":"测试组"}`)
	status, obj := doAuth(t, a, "POST", "/links/groups/create", body, auth)
	if status != 409 {
		t.Fatalf("重名建组应 409：%d", status)
	}
	if code := obj["error"].(map[string]any)["code"]; code != "link.group_conflict" {
		t.Errorf("code = %v", code)
	}

	// 组内加友链 → 移动到另一组 → 删除
	body = bytes.NewBufferString(`{"name":"新组2","order":6}`)
	_, _, _ = a.TestReq("POST", "/links/groups/create", body, auth)
	body = bytes.NewBufferString(`{"group":"测试组","id":"l-1","name":"链","url":"https://l.example","color":"#f08"}`)
	if status, obj2, err := a.TestReq("POST", "/links/create", body, auth); err != nil || status != 200 {
		t.Fatalf("加友链应 200：%d %v %v", status, obj2, err)
	}
	body = bytes.NewBufferString(`{"fromGroup":"测试组","toGroup":"新组2","id":"l-1"}`)
	if status, _, err := a.TestReq("POST", "/links/move", body, auth); err != nil || status != 200 {
		t.Fatalf("移动应 200：%d", status)
	}
	// 验证移过去了
	status, raw, _ := a.TestReq("GET", "/links/list", nil, nil)
	var links map[string]any
	_ = json.Unmarshal(raw, &links)
	found := false
	for _, g := range links["data"].([]any) {
		gm := g.(map[string]any)
		if gm["name"] == "新组2" {
			for _, l := range gm["links"].([]any) {
				if l.(map[string]any)["id"] == "l-1" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("友链没有出现在目标分组")
	}
	status, _, _ = a.TestReq("DELETE", "/links/delete?group=新组2&id=l-1", nil, auth)
	if status != 200 {
		t.Fatalf("删除友链应 200：%d", status)
	}
}

func TestSiteUpdateTimeBody(t *testing.T) {
	a, st := testHandlerApp(t)
	seedUsers(st)
	_, loginResp := login(t, a, testUser, testPassword)
	token := loginResp["data"].(map[string]any)["token"].(string)
	auth := map[string]string{"Authorization": token, "Content-Type": "application/json"}

	// 自定义时间戳
	body := bytes.NewBufferString(`{"time":1234567890}`)
	status, raw, err := a.TestReq("POST", "/site/update-time", body, auth)
	if err != nil || status != 200 {
		t.Fatalf("update-time 应 200：%d %v", status, err)
	}
	if !strings.Contains(string(raw), "1234567890") {
		t.Errorf("自定义时间戳未生效：%s", raw)
	}
	// site/info 读回
	status, raw, _ = a.TestReq("GET", "/site/info", nil, nil)
	if !strings.Contains(string(raw), "1234567890") {
		t.Errorf("读回错误：%s", raw)
	}
}

// doAuth 带可选头的 JSON 请求。
func doAuth(t *testing.T, a *app.App, method, path string, body *bytes.Buffer, header map[string]string) (int, map[string]any) {
	t.Helper()
	status, raw, err := a.TestReq(method, path, body, header)
	if err != nil {
		t.Fatalf("%s %s：%v", method, path, err)
	}
	var obj map[string]any
	_ = json.Unmarshal(raw, &obj)
	return status, obj
}

func itoa64(f float64) string {
	return strconvFormatInt(int64(f))
}

func strconvFormatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	buf := []byte{}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
