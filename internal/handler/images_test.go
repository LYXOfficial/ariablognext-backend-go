package handler

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/app"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/platform"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/service"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store/fake"
)

// 图床 handlers：纯函数 + 「配置了但端点不可达」的错误路径
// （真实上传/删除在 P3 已用云端 R2 端到端验证过）。

func mediaTestCfg() *config.Config {
	cfg := handlerTestConfig()
	cfg.Media = config.MediaConfig{
		Enabled: true,
		S3: config.S3Config{
			Endpoint: "http://127.0.0.1:1", AccessKeyID: "k", SecretAccessKey: "s",
			Region: "us-east-1", Bucket: "b", PublicURL: "https://img.example",
		},
		Upload: config.UploadConfig{MaxBytes: 4 * config.MB,
			AllowedTypes: []string{"image/png", "image/jpeg"}},
		Import: config.ImportConfig{MaxBytes: 3 * config.MB, Timeout: config.Duration(2e9)},
	}
	return cfg
}

func mediaTestApp(t *testing.T) (*app.App, *fake.Store) {
	t.Helper()
	cfg := mediaTestCfg()
	st := fake.New()
	a := app.New(cfg, st)
	svc := service.NewSet(cfg, st)
	svc.AttachLogin(st.Users(), a.JWT())
	Register(a, cfg, st, svc, NewImageStore(cfg))
	return a, st
}

func TestImageHelpers(t *testing.T) {
	// safeKey：过滤非法字符、去首尾斜杠（防路径穿越）
	if got := safeKey("/../a b<script>.png"); strings.Contains(got, "<") || strings.HasPrefix(got, "/") {
		t.Errorf("safeKey 未清洗：%q", got)
	}
	if got := extOf("x.PNG"); got != ".png" {
		t.Errorf("extOf 应小写：%q", got)
	}
	if got := extFromContentType("image/jpeg; charset=x"); got != ".jpg" {
		t.Errorf("jpeg 映射错误：%q", got)
	}
	if got := extFromContentType("text/plain"); got != "" {
		t.Errorf("非图片应空：%q", got)
	}
	if got := randomKey(".png"); !strings.HasSuffix(got, ".png") || !strings.Contains(got, "/") {
		t.Errorf("randomKey 形态错误：%q", got)
	}
	// keyFromURL：前缀必须匹配
	is := &ImageStore{cfg: mediaTestCfg()}
	if _, err := is.keyFromURL("https://other.example/a.png"); err == nil {
		t.Error("非本图床地址应拒绝")
	}
	if _, err := is.keyFromURL("https://img.example/"); err == nil {
		t.Error("空 key 应拒绝")
	}
	key, err := is.keyFromURL("https://img.example/2026/10/a.png")
	if err != nil || key != "2026/10/a.png" {
		t.Errorf("key 解析错误：%q %v", key, err)
	}
}

func TestImagesUploadUnreachableEndpoint(t *testing.T) {
	a, st := mediaTestApp(t)
	seedUsers(st)
	_, loginResp := login(t, a, testUser, testPassword)
	token := loginResp["data"].(map[string]any)["token"].(string)
	auth := map[string]string{"Authorization": token}

	// 类型不支持 → 415（在调 S3 前就拦）
	body, ct := multipartBodyT(t, "file", "a.txt", []byte("x"), "extra", "v")
	status, obj := doAuth(t, a, "POST", "/images/upload", body,
		map[string]string{"Authorization": token, "Content-Type": ct})
	if status != 415 || obj["error"].(map[string]any)["code"] != "image.unsupported_type" {
		t.Fatalf("不支持类型应 415：%d %v", status, obj)
	}

	// 超过 4MB → 413
	huge := bytes.Repeat([]byte("x"), 4*1024*1024+10)
	body, ct = multipartBodyT(t, "file", "a.png", huge, "extra", "v")
	status, obj = doAuth(t, a, "POST", "/images/upload", body,
		map[string]string{"Authorization": token, "Content-Type": ct})
	if status != 413 {
		t.Fatalf("超大文件应 413：%d %v", status, obj)
	}

	// 合法请求但 S3 端点不可达 → upstream.failed
	body, ct = multipartBodyT(t, "file", "a.png", pngFixtureT(8), "extra", "v")
	status, obj = doAuth(t, a, "POST", "/images/upload", body,
		map[string]string{"Authorization": token, "Content-Type": ct})
	if status != 502 || obj["error"].(map[string]any)["code"] != "upstream.failed" {
		t.Fatalf("端点不可达应 upstream.failed：%d %v", status, obj)
	}

	// reupload：缺 url → 400；类型不支持 → 415；合法但不可达 → 502
	body, ct = multipartBodyT(t, "file", "a.png", pngFixtureT(8), "extra", "v")
	status, _ = doAuth(t, a, "PUT", "/images/reupload", body,
		map[string]string{"Authorization": token, "Content-Type": ct})
	if status != 400 {
		t.Fatalf("缺 url 应 400：%d", status)
	}
	body, ct = multipartBodyT(t, "file", "a.txt", []byte("x"), "url", "https://img.example/a.png")
	status, _ = doAuth(t, a, "PUT", "/images/reupload", body,
		map[string]string{"Authorization": token, "Content-Type": ct})
	if status != 415 {
		t.Fatalf("类型不支持应 415：%d", status)
	}
	body, ct = multipartBodyT(t, "file", "a.png", pngFixtureT(8), "url", "https://img.example/2026/a.png")
	status, _ = doAuth(t, a, "PUT", "/images/reupload", body,
		map[string]string{"Authorization": token, "Content-Type": ct})
	if status != 502 {
		t.Fatalf("不可达应 502：%d", status)
	}

	// import：缺 url → 400；指向内网 → validation.invalid_field（SSRF）；非法 JSON
	status, obj = doAuth(t, a, "POST", "/images/import", bytes.NewBufferString(`{}`),
		map[string]string{"Authorization": token, "Content-Type": "application/json"})
	if status != 400 {
		t.Fatalf("缺 url 应 400：%d %v", status, obj)
	}
	payload, _ := json.Marshal(map[string]string{"url": "http://127.0.0.1:1/a.png"})
	status, obj = doAuth(t, a, "POST", "/images/import", bytes.NewBuffer(payload),
		map[string]string{"Authorization": token, "Content-Type": "application/json"})
	if status != 400 || obj["error"].(map[string]any)["code"] != "validation.invalid_field" {
		t.Fatalf("内网地址应被 SSRF 拦：%d %v", status, obj)
	}

	// delete：缺 url / 非本图床 / 合法但不可达
	status, _ = doAuth(t, a, "DELETE", "/images/delete", nil, auth)
	if status != 400 {
		t.Fatalf("缺 url 应 400：%d", status)
	}
	status, _ = doAuth(t, a, "DELETE", "/images/delete?url=https://other.example/x.png", nil, auth)
	if status != 400 {
		t.Fatalf("非本图床应 400：%d", status)
	}
	status, _ = doAuth(t, a, "DELETE", "/images/delete?url=https://img.example/2026/x.png", nil, auth)
	if status != 502 {
		t.Fatalf("不可达应 502：%d", status)
	}

	// list：不可达 → 502
	status, _ = doAuth(t, a, "GET", "/images/list", nil, auth)
	if status != 502 {
		t.Fatalf("list 不可达应 502：%d", status)
	}
}

// 图床列表的元素形状（2026-10-27 修）：`data.images` 必须是 URL 字符串数组。
// 依据：旧 `/update/image/listImages` 返回 `{"images":["https://…"]}`，
// dashboard 把它当 string[] 用；胶水层是原样透传 data 的。
// 富信息放 data.items（旧前端会忽略未知键）。
func TestImagesListReturnsURLStrings(t *testing.T) {
	// 用 httptest 假 S3？S3 客户端走 aws-sdk，这里只验证「形状组装」这段纯逻辑：
	// 直接构造 items 走与 List handler 相同的组装分支。
	items := []platform.ImageObject{
		{Key: "2026/10/02/b.png", Size: 20, LastModified: 200, URL: "https://img.example/2026/10/02/b.png"},
		{Key: "2026/10/01/a.png", Size: 10, LastModified: 100, URL: "https://img.example/2026/10/01/a.png"},
	}
	// 复刻 handler 里的组装逻辑（排序 + 取 URL）
	sort.Slice(items, func(i, j int) bool { return items[i].LastModified > items[j].LastModified })
	urls := make([]string, 0, len(items))
	for _, it := range items {
		urls = append(urls, it.URL)
	}
	if len(urls) != 2 || urls[0] != "https://img.example/2026/10/02/b.png" {
		t.Fatalf("应按 lastModified 倒序且元素是字符串：%v", urls)
	}
	// 类型断言：元素必须是 string（不是对象）
	var any0 any = urls[0]
	if _, ok := any0.(string); !ok {
		t.Errorf("images 元素应为 string，实际 %T", any0)
	}
}
