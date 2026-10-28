package fake

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

// fake 是 handler/service 单测的地基，本身要保证：
// 编译期实现全部接口（var _ 断言）+ 基础行为正确。
var (
	_ store.Store              = (*Store)(nil)
	_ store.RawExporter        = (*Store)(nil)
	_ store.TaxonomyRepository = postRepo{}
)

func TestFakePostPageFiltersAndPagination(t *testing.T) {
	s := New()
	s.SetPosts([]model.Post{
		{Slug: "a", Title: "Go 入门", Category: "技术", Tags: []string{"go"}, PublishTime: 100, PlainContent: "正文 A"},
		{Slug: "b", Title: "随笔一篇", Category: "随笔", Tags: []string{"life", "go"}, PublishTime: 200, PlainContent: "正文 B"},
		{Slug: "c", Title: "Go 进阶", Category: "技术", Tags: []string{"go"}, PublishTime: 300, PlainContent: "正文 C"},
	})
	ctx := context.Background()
	sh := time.UTC

	// keyword 只命中标题里的两篇「Go」；count 是过滤后的总数不是页大小
	page, err := s.Posts().Page(ctx, store.PostFilter{Page: 1, PageSize: 1, Keyword: "go"}, sh)
	if err != nil {
		t.Fatal(err)
	}
	if page.Count != 2 {
		t.Errorf("count = %d，应为 2", page.Count)
	}
	if len(page.Items) != 1 {
		t.Fatalf("本页条数 = %d", len(page.Items))
	}
	if !page.HasMore {
		t.Error("第 1 页应该 hasMore")
	}
	if page.Items[0].Slug != "c" {
		t.Errorf("默认按 publishTime 倒序，应为 c，得到 %s", page.Items[0].Slug)
	}

	// tag 过滤 + 分页第二页
	page, err = s.Posts().Page(ctx, store.PostFilter{Page: 2, PageSize: 2, Tag: "go"}, sh)
	if err != nil {
		t.Fatal(err)
	}
	if page.Count != 3 || len(page.Items) != 1 {
		t.Errorf("tag 过滤分页异常：count=%d items=%d", page.Count, len(page.Items))
	}

	// CountOnly：不产出列表主体
	page, err = s.Posts().Page(ctx, store.PostFilter{Page: 1, PageSize: 16, CountOnly: true, Category: "随笔"}, sh)
	if err != nil {
		t.Fatal(err)
	}
	if page.Count != 1 || page.Items != nil {
		t.Errorf("countOnly 应只返回 count：count=%d items=%v", page.Count, page.Items)
	}
}

func TestFakePostBySlugAndCreateDuplicate(t *testing.T) {
	s := New()
	ctx := context.Background()
	if _, err := s.Posts().BySlug(ctx, "x"); err != store.ErrNotFound {
		t.Errorf("不存在的 slug 应返回 ErrNotFound，得到 %v", err)
	}
	p := model.Post{Slug: "a", Title: "A", Tags: []string{"t"}}
	if err := s.Posts().Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.Posts().Create(ctx, p); err != store.ErrDuplicate {
		t.Errorf("重复 slug 应返回 ErrDuplicate，得到 %v", err)
	}
}

func TestFakePostDeleteIdempotent(t *testing.T) {
	s := New()
	ctx := context.Background()
	s.SetPosts([]model.Post{{Slug: "a"}})
	ok, err := s.Posts().Delete(ctx, "a")
	if err != nil || !ok {
		t.Fatalf("删除已有文章：ok=%v err=%v", ok, err)
	}
	ok, err = s.Posts().Delete(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("删除不存在的文章应返回 false（幂等）")
	}
}

func TestFakeTaxonomyAndArchive(t *testing.T) {
	// 2026-01-01 00:30 +08:00 = 2025-12-31 16:30 UTC —— 时区语义要对
	sh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	localJan := time.Date(2026, 1, 1, 0, 30, 0, 0, sh).Unix()
	// UTC 12月31日傍晚 → 上海已是 1月1日，正是旧后端 UTC 分桶会算错月界的边界
	utcDec := time.Date(2025, 12, 31, 18, 30, 0, 0, time.UTC).Unix()

	s := New()
	s.SetPosts([]model.Post{
		{Slug: "a", Category: "技术", Tags: []string{"go", "rust"}, PublishTime: localJan},
		{Slug: "b", Category: "技术", Tags: []string{"go"}, PublishTime: utcDec},
	})
	buckets, err := s.Taxonomy().ArchiveBuckets(context.Background(), sh)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 || buckets[0].Year != 2026 || buckets[0].Month != 1 || buckets[0].Count != 2 {
		t.Errorf("归档分桶应按上海时区归入 2026-01：%+v", buckets)
	}
	cats, _ := s.Taxonomy().Categories(context.Background())
	if len(cats) != 1 || cats[0].Count != 2 {
		t.Errorf("分类计数错误：%+v", cats)
	}
	tags, _ := s.Taxonomy().Tags(context.Background())
	if len(tags) != 2 {
		t.Errorf("标签计数错误：%+v", tags)
	}
}

func TestFakeExportAll(t *testing.T) {
	s := New()
	s.SetPosts([]model.Post{{Slug: "a"}})
	data, err := s.ExportAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := data["Posts"]; !ok {
		t.Errorf("导出应包含 Posts 集合：%v", keysOf(data))
	}
	var posts []map[string]any
	raw, _ := json.Marshal(data["Posts"])
	if err := json.Unmarshal(raw, &posts); err != nil || len(posts) != 1 {
		t.Errorf("导出的 Posts 应是文档数组：%v", err)
	}
}

func TestFakeSpeakPageOrderAndLinkCount(t *testing.T) {
	s := New()
	s.SetSpeaks([]model.Speak{{Time: 1}, {Time: 3}, {Time: 2}})
	page, err := s.Speaks().Page(context.Background(), store.ListFilter{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if page.Count != 3 || page.Items[0].Time != 3 {
		t.Errorf("说说应按 time 倒序：count=%d first=%d", page.Count, page.Items[0].Time)
	}

	s.SetGroups([]model.LinkGroup{
		{Name: "g1", Links: []model.Link{{ID: "1"}, {ID: "2"}}},
		{Name: "g2", Links: []model.Link{{ID: "3"}}},
	})
	n, err := s.Links().LinkCount(context.Background())
	if err != nil || n != 3 {
		t.Errorf("LinkCount 应为链接条目总数 3：%d %v", n, err)
	}
}

func keysOf(m map[string][]json.RawMessage) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

// 同义词/别名：搜「图床」应命中只写了 R2 的文章（原词结果优先）。
func TestKeywordAliasMatch(t *testing.T) {
	st := New()
	st.SetPosts([]model.Post{
		{Slug: "a", Title: "图床迁移", MdContent: "把图片放到图床。", PublishTime: 2},
		{Slug: "b", Title: "R2 使用记", MdContent: "用 R2 当图床后端。", PublishTime: 1},
		{Slug: "c", Title: "无关", MdContent: "今天天气不错。", PublishTime: 3},
	})
	page, err := st.Posts().Page(context.Background(),
		store.PostFilter{Page: 1, PageSize: 10, Keyword: "图床", Aliases: []string{"R2"}},
		time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if page.Count != 2 {
		t.Fatalf("应命中 a(原词) + b(别名)，实际 %d：%+v", page.Count, page.Items)
	}
	for _, p := range page.Items {
		if p.Slug == "c" {
			t.Error("无关文章不该命中")
		}
	}
}
