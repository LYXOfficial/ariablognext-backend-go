package mongo

// 回归：关键词回退路径的 context 必须跟着自己的文章走。
// 2026-10-28 线上故障：matched[] 与 contexts[] 是平行切片，排序只排了前者，
// 结果第一篇显示第二篇的正文（北京游记的摘要里出现重庆地铁）。

import (
	"strings"
	"testing"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/textutil"
)

func TestKeywordContextFollowsItsOwnPost(t *testing.T) {
	// 两篇：B（更新）正文含「广场」在重庆语境；A（更旧）正文含「广场」在北京语境。
	// 排序后 B 在前，如果 context 用错位数组就会把 A 的片段挂到 B 上。
	postA := model.Post{
		Slug: "a", Title: "北京欢迎您", PublishTime: 100,
		PlainContent: "地铁到王府井换乘一号线后便是天安门广场，国博在天安门东边。",
	}
	postB := model.Post{
		Slug: "b", Title: "记一次在CRT的小运转", PublishTime: 200,
		PlainContent: "坐10号线到龙头寺，南广场的换乘真的长。",
	}
	hits := []keywordHit{
		{post: postA, text: textutil.BuildSearchText(postA.Title, postA.PlainContent)},
		{post: postB, text: textutil.BuildSearchText(postB.Title, postB.PlainContent)},
	}

	items := buildKeywordItems(hits, store.PostFilter{Page: 1, PageSize: 10}, "广场")
	if len(items) != 2 {
		t.Fatalf("应返回 2 条，实际 %d", len(items))
	}
	// 排序：publishTime 降序 → b 在前
	if items[0].Slug != "b" || items[1].Slug != "a" {
		t.Fatalf("排序应为 b,a，实际 %s,%s", items[0].Slug, items[1].Slug)
	}
	if items[0].Context == nil || !strings.Contains(*items[0].Context, "龙头寺") {
		t.Errorf("b 的片段应来自 b 自己（龙头寺），实际 %v", ctxOf(items[0]))
	}
	if items[1].Context == nil || !strings.Contains(*items[1].Context, "王府井") {
		t.Errorf("a 的片段应来自 a 自己（王府井），实际 %v", ctxOf(items[1]))
	}
}

func ctxOf(p model.Post) string {
	if p.Context == nil {
		return "<nil>"
	}
	return *p.Context
}

// 分页后同样不能错位。
func TestKeywordContextAfterPagination(t *testing.T) {
	var hits []keywordHit
	for i := 1; i <= 4; i++ {
		p := model.Post{
			Slug: string(rune('a' + i - 1)), Title: "标题", PublishTime: int64(i * 100),
			PlainContent: "第" + string(rune('0'+i)) + "篇提到广场",
		}
		hits = append(hits, keywordHit{post: p, text: textutil.BuildSearchText(p.Title, p.PlainContent)})
	}
	// 第 2 页（pageSize=2）应拿到 publishTime 第二、第三的两篇
	items := buildKeywordItems(hits, store.PostFilter{Page: 2, PageSize: 2}, "广场")
	if len(items) != 2 {
		t.Fatalf("第 2 页应 2 条，实际 %d", len(items))
	}
	for _, it := range items {
		want := "第" + string(rune('0'+int(it.PublishTime/100))) + "篇"
		if it.Context == nil || !strings.Contains(*it.Context, want) {
			t.Errorf("%s 的片段应含 %q，实际 %v", it.Slug, want, ctxOf(it))
		}
	}
}
