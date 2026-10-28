package handler

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/app"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/service"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store/fake"
)

// golden 契约快照（docs/testing.md §5）：
// 用固定假数据渲染完整响应，逐字节比对，防止字段悄悄改名/消失。
//
//	go test ./internal/handler -run TestGolden -update   # 确认改契约时重新生成
//	go test ./internal/handler -run TestGolden           # 平时跑
var updateGolden = flag.Bool("update", false, "重新生成 golden 契约快照")

const goldenDir = "../../testdata/golden"

// goldenRequest 是一个快照用例。
type goldenRequest struct {
	name   string
	method string
	path   string
	token  string
}

// goldenEnv 构造固定数据与固定 requestId 的 App 环境。
// 时间戳全部写死常量——golden 里不许有会变的值。
func goldenEnv(t *testing.T) *app.App {
	t.Helper()
	cfg := handlerTestConfig()
	st := fake.New()
	pm := func(s string) *string { return &s }
	wc := func(n int) *int { return &n }
	st.SetPosts([]model.Post{
		{
			Slug: "golden-post", Title: "快照文章", Category: "技术", Tags: []string{"go", "测试"},
			BannerImg: "https://img.0v0.my/g.png", Description: pm("摘要"), CoverFit: pm("center"),
			PublishTime: 1750000000, LastUpdatedTime: 1750000100, WordCount: wc(120),
			PlainContent: "正文摘要", MdContent: "# 快照文章\n\n正文",
		},
		{
			Slug: "golden-post-2", Title: "第二篇", Category: "随笔", Tags: []string{"生活"},
			PublishTime: 1749990000, LastUpdatedTime: 1749990000,
			PlainContent: "另一段", MdContent: "正文二",
		},
	})
	st.SetSpeaks([]model.Speak{{Time: 1750000000, Content: "说说内容", PlainContent: "说说内容"}})
	st.SetGroups([]model.LinkGroup{{
		Name: "友达", Description: "分组说明", Order: 0,
		Links: []model.Link{{ID: "l1", Name: "友链", URL: "https://l.example",
			Avatar: "https://l.example/a.png", Description: "友链说明", Color: "#f08", Latency: 12.5}},
	}})
	st.SetSiteInfo(map[string]int64{"latestUpdateTime": 1750000200})
	a := app.New(cfg, st)
	svc := service.NewSet(cfg, st)
	svc.AttachLogin(st.Users(), a.JWT())
	Register(a, cfg, st, svc, NewImageStore(cfg))
	return a
}

func TestGolden(t *testing.T) {
	if err := os.MkdirAll(goldenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// requestId 在响应里会变：快照比对前统一替换成固定值（契约只说「要有这个键」）
	cases := []goldenRequest{
		{name: "posts-list", method: "GET", path: "/posts/list?pageSize=2"},
		{name: "posts-list-countonly", method: "GET", path: "/posts/list?countOnly=true"},
		{name: "posts-detail", method: "GET", path: "/posts/detail?slug=golden-post"},
		{name: "posts-navigation", method: "GET", path: "/posts/navigation?slug=golden-post"},
		{name: "tags-list", method: "GET", path: "/tags/list"},
		{name: "categories-list", method: "GET", path: "/categories/list"},
		{name: "archives-list", method: "GET", path: "/archives/list"},
		{name: "links-list", method: "GET", path: "/links/list"},
		{name: "speaks-list", method: "GET", path: "/speaks/list?pageSize=1"},
		{name: "site-info", method: "GET", path: "/site/info"},
		{name: "semantic-fallback", method: "GET", path: "/posts/semantic?query=x"},
		{name: "not-found", method: "GET", path: "/nope"},
		{name: "error-missing-param", method: "GET", path: "/posts/detail"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 每个用例独立的 App，避免跨用例状态污染
			a := goldenEnv(t)
			status, raw, err := a.TestReq(tc.method, tc.path, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			var obj map[string]any
			if err := json.Unmarshal(raw, &obj); err != nil {
				t.Fatalf("响应不是 JSON：%s", raw)
			}
			// requestId 归一化（golden 里必须是固定值）
			obj["requestId"] = "golden"
			body, err := json.MarshalIndent(map[string]any{
				"status": status,
				"body":   obj,
			}, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			body = append(body, '\n')

			path := filepath.Join(goldenDir, tc.name+".json")
			if *updateGolden {
				if err := os.WriteFile(path, body, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("缺 golden 文件（用 -update 生成）：%v", err)
			}
			if string(want) != string(body) {
				t.Errorf("golden 不一致：\n--- want ---\n%s\n--- got ---\n%s", want, body)
			}
		})
	}
}
