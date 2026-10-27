// cmd/seeddev 向本地开发库灌固定种子数据。
//
// ⚠️ 只允许对 *_dev / *_test 库使用：程序里强制校验库名，库名不带 _dev/_test 后缀直接拒绝。
// 幂等：先按种子主键删旧再插入；-wipe 清空整个集合。
//
//	go run ./cmd/seeddev            # 使用当前环境（.env.dev）
//	go run ./cmd/seeddev -wipe     # 清空种子集合全部内容
//
// 架构说明：本工具不直接 import mongo 驱动（checks.sh 的约束对 cmd/ 同样生效），
// 写入全部走 internal/store 的仓储接口——顺便验证这些接口的真实可用性。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	// mongo 适配器注册进驱动表（注册表模式）
	_ "github.com/LYXOfficial/ariablognext-backend-go/internal/store/mongo"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/textutil"
)

// 种子主键清单：非 -wipe 模式下按这些键删旧插新，不碰库里的其它数据。
var seedSlugs = []string{"hello-go", "cloud-note", "search-lab", "special-chars", "no-tags"}
var seedLinkGroups = []string{"友达", "收藏"}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	wipe := flag.Bool("wipe", false, "清空种子集合全部内容（不只是种子主键）")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("配置加载失败", "err", err)
		os.Exit(1)
	}
	// 安全闸：种子工具绝不允许碰非 dev/test 库
	if !strings.HasSuffix(cfg.Database.Name, "_dev") && !strings.HasSuffix(cfg.Database.Name, "_test") {
		slog.Error("种子数据只能灌进 *_dev / *_test 库", "db", cfg.Database.Name)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, cfg)
	if err != nil {
		slog.Error("存储初始化失败（mongod 在跑吗）", "err", err)
		os.Exit(1)
	}
	defer st.Close(context.Background())

	if err := seed(ctx, st, *wipe); err != nil {
		slog.Error("种子写入失败", "err", err)
		os.Exit(1)
	}
	slog.Info("种子数据就绪", "db", cfg.Database.Name)
}

func seed(ctx context.Context, st store.Store, wipe bool) error {
	now := time.Now().Unix()

	// ---- Posts ----
	if wipe {
		slugs, err := st.Posts().Slugs(ctx)
		if err != nil {
			return err
		}
		for _, slug := range slugs {
			if _, err := st.Posts().Delete(ctx, slug); err != nil {
				return err
			}
		}
	} else {
		for _, slug := range seedSlugs {
			// 幂等：按主键删旧（不存在也安全）
			if _, err := st.Posts().Delete(ctx, slug); err != nil {
				return err
			}
		}
	}
	for _, p := range seedPosts(now) {
		// searchText 由 textutil 生成：让 dev 库覆盖「有索引字段」快路径；
		// 「无 searchText 的 fallback」由集成测试对无字段文档单独验证
		p.SearchText = textutil.BuildSearchText(p.Title, p.MdContent)
		if err := st.Posts().Create(ctx, p); err != nil {
			return err
		}
	}

	// ---- Speaks ----
	if wipe {
		page, err := st.Speaks().Page(ctx, store.ListFilter{Page: 1, PageSize: 1000})
		if err != nil {
			return err
		}
		for _, sp := range page.Items {
			if _, err := st.Speaks().Delete(ctx, sp.Time); err != nil {
				return err
			}
		}
	}
	for _, sp := range seedSpeaks(now) {
		if _, err := st.Speaks().Delete(ctx, sp.Time); err != nil {
			return err
		}
		if err := st.Speaks().Create(ctx, sp); err != nil {
			return err
		}
	}

	// ---- FLinks ----
	for _, name := range seedLinkGroups {
		if _, err := st.Links().DeleteGroup(ctx, name); err != nil {
			return err
		}
	}
	for _, g := range seedGroups() {
		if err := st.Links().AddGroup(ctx, g); err != nil {
			return err
		}
		for _, l := range g.Links {
			if err := st.Links().AddLink(ctx, g.Name, l); err != nil {
				return err
			}
		}
	}

	// ---- Users（仅 dev：给联调用测试管理员；生产 Users 不受影响）----
	// 密码 sha256("seed-admin-password") 的小写 hex，与旧后端算法一致
	sum := sha256.Sum256([]byte("seed-admin-password"))
	if err := st.(store.UserWriter).UpsertUser(ctx, "seedadmin", hex.EncodeToString(sum[:])); err != nil {
		return err
	}

	// ---- webSiteInfo ----
	if _, err := st.Sites().SetInt(ctx, "latestUpdateTime", now); err != nil {
		return err
	}
	return nil
}

func mustStr(s string) *string { return &s }

func mustInt(n int) *int { return &n }

// seedPosts 的正文刻意包含：外挂标签（Folding）、代码块、中英混排、
// 不同月份的发布时间（归档分桶测试）、特殊字符（字面搜索测试）。
func seedPosts(now int64) []model.Post {
	day := int64(86400)
	base := now - 400*day // 散布到 13 个月里，让归档有多个桶
	mk := func(i int, slug, title, category string, tags []string, md string) model.Post {
		plain := textutil.CleanMarkdown(md)
		return model.Post{
			Slug: slug, Title: title, Category: category, Tags: tags,
			BannerImg:   "https://img.0v0.my/seed/" + slug + ".png",
			Description: mustStr("第 " + strconv.Itoa(i) + " 篇种子文章的摘要"),
			CoverFit:    mustStr("center"),
			PublishTime: base + int64(i)*day*7, LastUpdatedTime: base + int64(i)*day*7,
			WordCount: mustInt(len([]rune(plain))/2 + 100),
			// 存量契约：plainContent 截断到 201 字
			PlainContent: truncateRunes(plain, 201),
			MdContent:    md,
		}
	}
	return []model.Post{
		mk(0, "hello-go", "你好 Go", "技术", []string{"go", "博客"},
			"# 你好\n\n这是**种子**文章，带 `代码` 与 [链接](https://example.com)。\n\n<Folding summary=\"点开\">\n折叠正文 fold 说明\n</Folding>\n\n正文有 go 关键词。"),
		mk(1, "cloud-note", "云端随笔", "随笔", []string{"生活"},
			"# 随笔\n\n普通段落，用于测试分类过滤与归档分桶。"),
		mk(2, "search-lab", "搜索实验室 Go 指南", "技术", []string{"go", "搜索"},
			"# 搜索\n\n正文包含 蒙古 与 mongodb 字样，用于多关键词 AND 测试。搜索 关键词齐活。"),
		mk(3, "special-chars", "特殊字符 c++ (编译) [_notes]", "技术", []string{"c++"},
			"# 特殊字符\n\n标题与正文都带 .*[]() 等元字符，验证字面搜索。c++ 是重点。"),
		mk(4, "no-tags", "无标签文章", "随笔", []string{},
			"# 无标签\n\n这篇没有标签，related 应跳过它。"),
	}
}

func seedSpeaks(now int64) []model.Speak {
	return []model.Speak{
		{Time: now - 3600, Content: "第一条说说 **加粗**", PlainContent: "第一条说说 加粗"},
		{Time: now - 7200, Content: "第二条说说", PlainContent: "第二条说说"},
		{Time: now - 86400, Content: "昨天的一条", PlainContent: "昨天的一条"},
	}
}

func seedGroups() []model.LinkGroup {
	return []model.LinkGroup{
		{Name: "友达", Description: "种子的友达分组", Order: 0, Links: []model.Link{
			{ID: "seed-link-1", Name: "示例站", URL: "https://example.com",
				Avatar: "https://img.0v0.my/seed/av1.png", Description: "示例友链",
				Color: "#c95d7f", Latency: 32.5},
			{ID: "seed-link-2", Name: "无斜杠站", URL: "https://noslash.example.com",
				Avatar: "", Description: "测 FindByURL 尾斜杠兼容", Color: "#888888", Latency: 0},
		}},
		{Name: "收藏", Description: "分组 order 缺省按 0", Order: 0, Links: []model.Link{
			{ID: "seed-link-3", Name: "收藏站", URL: "https://fav.example.com",
				Avatar: "", Description: "", Color: "", Latency: 0},
		}},
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
