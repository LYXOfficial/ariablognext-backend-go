package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// 不需要数据库的单元测试：错误映射与纯函数。
// 需要真实库的行为（过滤、聚合、索引）走 -tags=integration 的集成测试。

func TestWrapMongoSentinels(t *testing.T) {
	if wrapMongo(nil) != nil {
		t.Error("nil 应透传")
	}
	if wrapMongo(store.ErrNotFound) != store.ErrNotFound {
		t.Error("哨兵应原样透传")
	}
	// 驱动的「没有文档」必须转成 store.ErrNotFound，
	// service 层不许出现 mongo.ErrNoDocuments（store-interfaces.md §9）
	if wrapMongo(mongo.ErrNoDocuments) != store.ErrNotFound {
		t.Errorf("ErrNoDocuments 应映射 ErrNotFound，得到 %v", wrapMongo(mongo.ErrNoDocuments))
	}
	// 重复键错误没法凭空构造（要真的触发索引），这里验证普通错误走兜底包装
	plain := fmt.Errorf("boom")
	wrapped := wrapMongo(plain)
	if wrapped == store.ErrNotFound || wrapped == store.ErrDuplicate {
		t.Error("普通错误不该被误判成哨兵")
	}
	if !errors.Is(wrapped, plain) {
		t.Error("普通错误应保留 %w 包装链")
	}
}

func TestUniqueIndexHasUniqueOption(t *testing.T) {
	m := uniqueIndex("slug")
	// keys 用 bson 文档，这里只断言 Options 非 nil（unique 保护是写竞态安全的根基）
	if m.Options == nil {
		t.Fatal("uniqueIndex 必须带 Options")
	}
	keys, ok := m.Keys.(bson.D)
	if !ok || len(keys) == 0 {
		t.Errorf("keys 应是 bson.D 且非空：%#v", m.Keys)
	}
}

func TestRegexpEscape(t *testing.T) {
	// 用户输入里的元字符必须被转义，否则 .* 就是注入面
	if got := regexpEscape("a.b*c"); got != `a\.b\*c` {
		t.Errorf("regexpEscape = %q", got)
	}
}

func TestYearMonthRange(t *testing.T) {
	sh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	start, end := yearMonthRange(2026, 2, sh)
	// 2026-02-01 00:00 +08:00 = 2026-01-31 16:00 UTC
	wantStart := time.Date(2026, 1, 31, 16, 0, 0, 0, time.UTC).Unix()
	wantEnd := time.Date(2026, 2, 28, 16, 0, 0, 0, time.UTC).Unix() // 2026-03-01 00:00 +08 = 2026-02-28 16:00 UTC，2026 年 2 月只有 28 天
	if start != wantStart || end != wantEnd {
		t.Errorf("yearMonthRange(2026,2,sh) = (%d,%d)，应为 (%d,%d)", start, end, wantStart, wantEnd)
	}
	// 整年区间
	start, end = yearMonthRange(2026, 0, sh)
	if end-start != 365*86400 {
		t.Errorf("2026 全年应为 365 天，得到 %d 秒", end-start)
	}
	// 跨年归一化：13 月 = 次年 1 月
	start, end = yearMonthRange(2025, 13, sh)
	wantStart = time.Date(2025, 12, 31, 16, 0, 0, 0, time.UTC).Unix()
	if start != wantStart {
		t.Errorf("13 月应归一化为次年 1 月，start=%d want=%d", start, wantStart)
	}
}

func TestSplitKeywords(t *testing.T) {
	// 多关键词 AND：空白分隔；全空白 = 不过滤
	if got := SplitKeywords("go  部署   教程"); len(got) != 3 || got[0] != "go" || got[2] != "教程" {
		t.Errorf("SplitKeywords = %#v", got)
	}
	if got := SplitKeywords("   "); len(got) != 0 {
		t.Errorf("全空白应返回空：%#v", got)
	}
}

func TestBuildPostQueryMultiKeywordAndLiteral(t *testing.T) {
	sh, _ := time.LoadLocation("Asia/Shanghai")

	// 多关键词 → $and，每个关键词是「快慢双路」$or 片段，且都被 QuoteMeta
	q, err := buildPostQuery(store.PostFilter{Keyword: "go 部署"}, sh)
	if err != nil {
		t.Fatal(err)
	}
	ands, ok := q["$and"].([]bson.M)
	if !ok || len(ands) != 2 {
		t.Fatalf("两个关键词应产生 $and：%#v", q["$and"])
	}
	firstOr := ands[0]["$or"].([]bson.M)
	if firstOr[0]["searchText"].(bson.M)["$regex"] != "go" {
		t.Errorf("第一个关键词快路径：%v", firstOr[0])
	}
	if _, hasExpr := firstOr[1]["$and"]; !hasExpr {
		t.Errorf("慢路径（无 searchText fallback）缺失：%v", firstOr[1])
	}

	// 特殊字符必须按字面（QuoteMeta）：`.*)` 不允许变成正则语义。
	// 输入按空白分成 4 个关键词，每个都该是「转义后的字面串」。
	q, err = buildPostQuery(store.PostFilter{Keyword: `c++ (编译) [_notes] .*)`}, sh)
	if err != nil {
		t.Fatal(err)
	}
	ands, ok = q["$and"].([]bson.M)
	if !ok || len(ands) != 4 {
		t.Fatalf("4 个关键词应产生长度 4 的 $and：%#v", q["$and"])
	}
	for i, want := range []string{regexp.QuoteMeta("c++"), regexp.QuoteMeta("(编译)"),
		regexp.QuoteMeta("[_notes]"), regexp.QuoteMeta(".*)")} {
		or := ands[i]["$or"].([]bson.M)
		got := or[0]["searchText"].(bson.M)["$regex"]
		if got != want {
			t.Errorf("关键词 %d：got %v want %v", i, got, want)
		}
	}

	// TitleOnly 打在 title 字段上（title 恒存在，无双路）
	q, _ = buildPostQuery(store.PostFilter{Keyword: "go", TitleOnly: true}, sh)
	titleCond, ok := q["title"].(bson.M)
	if !ok || titleCond["$regex"] != "go" {
		t.Errorf("titleOnly 应查 title：%#v", q)
	}
	// 单关键词全文搜索：$or 在顶层
	q, _ = buildPostQuery(store.PostFilter{Keyword: "go"}, sh)
	if _, ok := q["$or"]; !ok {
		t.Errorf("单关键词全文搜索应是顶层 $or：%#v", q)
	}
}

// 只读闸门：连生产库做验证时，所有写方法必须在第一行就返回 ErrReadOnly
// （不依赖 nil collection 报错——闸门要在碰驱动之前生效）。
func TestReadOnlyGateBlocksWrites(t *testing.T) {
	post := &postRepo{ro: true}
	draft := &draftRepo{ro: true}
	speak := &speakRepo{ro: true}
	link := &linkRepo{ro: true}
	app := &applicationRepo{ro: true}
	site := &siteRepo{ro: true}
	ctx := context.Background()

	checks := map[string]error{
		"Posts.Create":        post.Create(ctx, model.Post{Slug: "x"}),
		"Posts.UpdateMeta":    post.UpdateMeta(ctx, "x", model.PostMeta{}),
		"Posts.UpdateContent": post.UpdateContent(ctx, "x", model.PostContent{}),
		"Drafts.Create":       draft.Create(ctx, model.Draft{Slug: "x"}),
		"Drafts.UpdateMeta":   draft.UpdateMeta(ctx, "x", model.PostMeta{}),
		"Speaks.Create":       speak.Create(ctx, model.Speak{Time: 1}),
		"Speaks.Update":       errOf(speak.Update(ctx, 1, "a", "b")),
		"Links.AddGroup":      link.AddGroup(ctx, model.LinkGroup{Name: "g"}),
		"Links.AddLink":       link.AddLink(ctx, "g", model.Link{ID: "1"}),
		"Links.DeleteGroup":   errOf(link.DeleteGroup(ctx, "g")),
		"Applications.Decide": errOf(app.Decide(ctx, "id", "approved", "", time.Now())),
		"Applications.Purge":  errOf(app.PurgeDecidedBefore(ctx, time.Now())),
		"Sites.SetInt":        errOf(site.SetInt(ctx, "k", 1)),
	}
	for name, err := range checks {
		if !errors.Is(err, store.ErrReadOnly) {
			t.Errorf("%s 在只读模式应返回 ErrReadOnly，实际 %v", name, err)
		}
	}
}

// errOf 取 (T, error) 里的 error。
func errOf[T any](_ T, err error) error { return err }

// 导出形态必须与旧 FastAPI json 一致（契约 §3.9「collections 内部结构与旧接口完全一致」）：
// Date → ISO 字符串、int64 → 数字、ObjectID → hex 字符串，绝不能出现 {"$date":…}/{"$numberLong":…}。
func TestToPlainJSONShape(t *testing.T) {
	doc := bson.M{
		"_id":         bson.NewObjectID(),
		"createdAt":   bson.DateTime(1793039171614),
		"publishTime": int64(1758482726),
		"wordCount":   int32(122),
		"nested":      bson.M{"decidedAt": bson.DateTime(1793039171614)},
		"tags":        bson.A{"go", "博客"},
		"nothing":     nil,
	}
	out := toPlainJSON(doc).(map[string]any)

	// Date → Python isoformat 形态的字符串
	if got, ok := out["createdAt"].(string); !ok || got != "2026-10-26T18:26:11.614000" {
		t.Errorf("createdAt 应为 ISO 字符串，得到 %#v", out["createdAt"])
	}
	// 嵌套文档里的 Date 同样转换
	nested := out["nested"].(map[string]any)
	if _, ok := nested["decidedAt"].(string); !ok {
		t.Errorf("嵌套 Date 未转换：%#v", nested["decidedAt"])
	}
	// int64 保持数字
	if v, ok := out["publishTime"].(int64); !ok || v != 1758482726 {
		t.Errorf("publishTime 应为数字，得到 %#v", out["publishTime"])
	}
	// ObjectID → 24 位 hex
	if s, ok := out["_id"].(string); !ok || len(s) != 24 {
		t.Errorf("_id 应为 24 位 hex 字符串，得到 %#v", out["_id"])
	}
	// 数组递归 + nil 保留
	if tags, ok := out["tags"].([]any); !ok || len(tags) != 2 {
		t.Errorf("数组未递归：%#v", out["tags"])
	}
	if v, exists := out["nothing"]; !exists || v != nil {
		t.Errorf("null 应保留：%#v", out["nothing"])
	}
	// 序列化后不得出现扩展 JSON 标记
	raw, _ := json.Marshal(out)
	for _, bad := range []string{"$date", "$numberLong", "$oid"} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("导出里不该出现 %s：%s", bad, raw)
		}
	}
}

// 导出的确定性：同一份数据两次导出必须字节级一致（按 _id 排序）。
// 背景（glue 在 GO-008 反馈）：Mongo 自然顺序不稳定，备份仓库做校验和/逐行 diff 会出现假差异。
func TestExportDeterministicOrdering(t *testing.T) {
	// 无法在纯单测里连库，这里验证「排序+序列化」这段纯逻辑：
	// 用相同内容构造两种顺序，经 toPlainJSON + 排序后应得到相同字节。
	ids := []bson.ObjectID{bson.NewObjectID(), bson.NewObjectID(), bson.NewObjectID()}
	mk := func(order []int) []byte {
		docs := make([]bson.M, 0, len(order))
		for _, i := range order {
			docs = append(docs, bson.M{"_id": ids[i], "v": i})
		}
		for _, d := range docs {
			d["_id"] = idString(d["_id"])
		}
		sort.SliceStable(docs, func(a, b int) bool {
			x, _ := docs[a]["_id"].(string)
			y, _ := docs[b]["_id"].(string)
			return x < y
		})
		out := make([]any, 0, len(docs))
		for _, d := range docs {
			out = append(out, toPlainJSON(d))
		}
		raw, _ := json.Marshal(out)
		return raw
	}
	a := mk([]int{0, 1, 2})
	b := mk([]int{2, 0, 1})
	if string(a) != string(b) {
		t.Errorf("不同插入顺序应导出相同字节：\n%s\n%s", a, b)
	}
}
