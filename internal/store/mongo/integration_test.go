//go:build integration

// 集成测试：连本地 mongod 验证仓储的真实行为（TASKS/docs/testing.md §4）。
// 运行：
//
//	MONGODB_URI_TEST=mongodb://127.0.0.1:27017 TEST_DB_NAME=AriaBlogNext_test \
//	  go test -tags=integration ./internal/store/mongo/ -v
//
// 没有设置 MONGODB_URI_TEST 时整体 skip（go test ./... 在任何机器都能跑）。
// ⚠️ 只连 *_test / *_dev 库；TestMain 结束会 Drop 掉该库。
package mongo

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

var testStore *Store
var testLoc *time.Location

func TestMain(m *testing.M) {
	// 环境变量读取统一走 internal/config（架构守卫：os.Getenv 只允许出现在 config 包）。
	// 这个文件带 integration build tag，仅集成测试时编译。
	uri, dbName, ok := config.TestDatabase()
	if !ok {
		// 没配测试库：整套跳过（不是失败）
		os.Exit(0)
	}
	if dbName != "AriaBlogNext_test" && dbName[len(dbName)-4:] != "_dev" {
		panic("集成测试只允许连 *_test 或 *_dev 库，拒绝：" + dbName)
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic(err)
	}
	testLoc = loc

	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Type: "mongo", URI: uri, Name: dbName,
			Pool: config.PoolConfig{Max: 5, Min: 0, IdleTimeout: config.Duration(time.Minute)},
		},
	}
	ctx := context.Background()
	st, err := Open(ctx, cfg)
	if err != nil {
		panic(err)
	}
	// 每次从干净库开始
	if err := st.db.Drop(ctx); err != nil {
		panic(err)
	}
	if err := st.ensureIndexes(ctx); err != nil {
		panic(err)
	}
	testStore = st

	code := m.Run()
	_ = st.db.Drop(context.Background())
	_ = st.Close(context.Background())
	os.Exit(code)
}

// seedPost 造一篇文章。
func seedPost(t *testing.T, p model.Post) {
	t.Helper()
	if err := testStore.Posts().Create(context.Background(), p); err != nil {
		t.Fatalf("造数据失败：%v", err)
	}
}

// ---- 文章：时区、分页、CountOnly、Full、重复键 ----

func TestIntegrationPostYearMonthTimezone(t *testing.T) {
	ctx := context.Background()
	// 上海时间 2026-01-01 00:30（= UTC 2025-12-31 16:30）与 2025-12-31 23:30（= UTC 15:30）
	janEarly := time.Date(2026, 1, 1, 0, 30, 0, 0, testLoc).Unix()
	decLate := time.Date(2025, 12, 31, 23, 30, 0, 0, testLoc).Unix()
	seedPost(t, model.Post{Slug: "tz-jan", Title: "一月", PublishTime: janEarly})

	// 2026-01 只应包含 janEarly，不包含 decLate（UTC 视角会把 decLate 也算进 2026-01 的边界陷阱）
	page, err := testStore.Posts().Page(ctx, store.PostFilter{Page: 1, PageSize: 10, Year: 2026, Month: 1}, testLoc)
	if err != nil {
		t.Fatal(err)
	}
	if page.Count != 1 || len(page.Items) != 1 || page.Items[0].Slug != "tz-jan" {
		t.Errorf("2026-01 应只有 tz-jan，得到 %d 条（count=%d）", len(page.Items), page.Count)
	}
	// 2025-12 应包含 decLate
	seedPost(t, model.Post{Slug: "tz-dec", Title: "十二月", PublishTime: decLate})
	page, _ = testStore.Posts().Page(ctx, store.PostFilter{Page: 1, PageSize: 10, Year: 2025, Month: 12}, testLoc)
	if page.Count != 1 || page.Items[0].Slug != "tz-dec" {
		t.Errorf("2025-12 应只有 tz-dec，count=%d", page.Count)
	}
	_, _ = testStore.Posts().Delete(ctx, "tz-jan")
	_, _ = testStore.Posts().Delete(ctx, "tz-dec")
}

func TestIntegrationPostPagingAndCountOnly(t *testing.T) {
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		seedPost(t, model.Post{Slug: "pg-" + itoaI(i), Title: "分页", PublishTime: int64(1750000000 + i)})
	}
	// 默认倒序 + 分页：count 是过滤后总数，不是本页条数
	page, err := testStore.Posts().Page(ctx, store.PostFilter{Page: 2, PageSize: 2}, testLoc)
	if err != nil {
		t.Fatal(err)
	}
	if page.Count != 5 || len(page.Items) != 2 || !page.HasMore {
		t.Errorf("分页异常：count=%d len=%d hasMore=%v", page.Count, len(page.Items), page.HasMore)
	}
	if page.Items[0].Slug != "pg-2" { // 倒序：pg-4,pg-3 | pg-2,pg-1
		t.Errorf("第 2 页首条应为 pg-2，得到 %v", page.Items[0].Slug)
	}
	// CountOnly：Items 必须为 nil（实现方不许跑 find）
	page, err = testStore.Posts().Page(ctx, store.PostFilter{CountOnly: true}, testLoc)
	if err != nil || page.Count != 5 || page.Items != nil {
		t.Errorf("CountOnly 异常：count=%d items=%v err=%v", page.Count, page.Items, err)
	}
	// Full：不算 hasMore
	page, _ = testStore.Posts().Page(ctx, store.PostFilter{Full: true}, testLoc)
	if len(page.Items) != 5 || page.HasMore {
		t.Errorf("Full 异常：len=%d hasMore=%v", len(page.Items), page.HasMore)
	}
	for i := 0; i < 5; i++ {
		_, _ = testStore.Posts().Delete(ctx, "pg-"+itoaI(i))
	}
}

func TestIntegrationPostDuplicateSlug(t *testing.T) {
	ctx := context.Background()
	seedPost(t, model.Post{Slug: "dup", Title: "原始"})
	err := testStore.Posts().Create(ctx, model.Post{Slug: "dup", Title: "重复"})
	if err != store.ErrDuplicate {
		t.Errorf("重复 slug 应 ErrDuplicate（unique index），得到 %v", err)
	}
	_, _ = testStore.Posts().Delete(ctx, "dup")
}

func TestIntegrationTaxonomy(t *testing.T) {
	ctx := context.Background()
	seedPost(t, model.Post{Slug: "tax-a", Category: "技术", Tags: []string{"go", "db"}, PublishTime: time.Date(2026, 3, 1, 10, 0, 0, 0, testLoc).Unix()})
	seedPost(t, model.Post{Slug: "tax-b", Category: "随笔", Tags: []string{"go"}, PublishTime: time.Date(2025, 12, 20, 10, 0, 0, 0, testLoc).Unix()})

	cats, err := testStore.Posts().(store.TaxonomyRepository).Categories(ctx)
	if err != nil || len(cats) != 2 {
		t.Errorf("分类聚合异常：%+v %v", cats, err)
	}
	tags, _ := testStore.Posts().(store.TaxonomyRepository).Tags(ctx)
	if len(tags) != 2 {
		t.Errorf("标签聚合异常：%+v", tags)
	}
	for _, tg := range tags {
		if tg.Name == "go" && tg.Count != 2 {
			t.Errorf("go 计数应为 2，得到 %d", tg.Count)
		}
	}
	// 归档：按上海时区、倒序
	buckets, err := testStore.Posts().(store.TaxonomyRepository).ArchiveBuckets(ctx, testLoc)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 || buckets[0].Year != 2026 || buckets[0].Month != 3 || buckets[1].Year != 2025 || buckets[1].Month != 12 {
		t.Errorf("归档分桶/排序异常：%+v", buckets)
	}
	_, _ = testStore.Posts().Delete(ctx, "tax-a")
	_, _ = testStore.Posts().Delete(ctx, "tax-b")
}

// ---- 友链：移动原子性、尾斜杠兼容、延迟计数、order 排序 ----

func TestIntegrationLinks(t *testing.T) {
	ctx := context.Background()
	if err := testStore.Links().AddGroup(ctx, model.LinkGroup{Name: "A组", Order: 0}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.Links().AddGroup(ctx, model.LinkGroup{Name: "B组", Order: 5}); err != nil {
		t.Fatal(err)
	}
	// 缺 order 的分组按 0 处理（用原始 $set 造一个没有 order 字段的组）
	if _, err := testStore.db.Collection("FLinks").InsertOne(ctx, map[string]any{"name": "无order组", "links": []any{}}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.Links().AddLink(ctx, "A组", model.Link{ID: "l1", Name: "链1", URL: "https://one.example"}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.Links().AddLink(ctx, "A组", model.Link{ID: "l2", Name: "链2", URL: "https://two.example/"}); err != nil {
		t.Fatal(err)
	}

	// Groups 按 order 升序（A=0、无order=0、B=5）
	groups, err := testStore.Links().Groups(ctx)
	if err != nil || len(groups) != 3 {
		t.Fatalf("分组数异常：%d %v", len(groups), err)
	}
	if groups[len(groups)-1].Name != "B组" {
		t.Errorf("应按 order 升序，最后应为 B组：%+v", groups)
	}
	for _, g := range groups {
		if g.Name == "无order组" && g.Order != 0 {
			t.Errorf("缺 order 应视为 0，得到 %d", g.Order)
		}
	}

	// FindByURL 尾斜杠兼容（库里存的是带斜杠的）
	if _, err := testStore.Links().FindByURL(ctx, "https://two.example"); err != nil {
		t.Errorf("FindByURL 未兼容尾斜杠：%v", err)
	}
	if _, err := testStore.Links().FindByURL(ctx, "https://two.example/"); err != nil {
		t.Errorf("FindByURL 带斜杠应命中：%v", err)
	}

	// MoveLink：源减目标增、总数不变
	before, _ := testStore.Links().LinkCount(ctx)
	if err := testStore.Links().MoveLink(ctx, "A组", "B组", "l1"); err != nil {
		t.Fatalf("移动失败：%v", err)
	}
	after, _ := testStore.Links().LinkCount(ctx)
	if before != after {
		t.Errorf("移动前后总数应不变：%d → %d", before, after)
	}
	ga, _ := testStore.Links().GroupByName(ctx, "A组")
	gb, _ := testStore.Links().GroupByName(ctx, "B组")
	if len(ga.Links) != 1 || ga.Links[0].ID != "l2" {
		t.Errorf("源组应只剩 l2：%+v", ga.Links)
	}
	if len(gb.Links) != 1 || gb.Links[0].ID != "l1" {
		t.Errorf("目标组应有 l1：%+v", gb.Links)
	}

	// 目标组不存在 → 自动创建（docs/testing.md §4.2），链接真正移过去
	if err := testStore.Links().MoveLink(ctx, "A组", "自动建组", "l2"); err != nil {
		t.Fatalf("目标组不存在应自动创建，得到 %v", err)
	}
	ga, _ = testStore.Links().GroupByName(ctx, "A组")
	if len(ga.Links) != 0 {
		t.Errorf("源组应已空：%+v", ga.Links)
	}
	created, err := testStore.Links().GroupByName(ctx, "自动建组")
	if err != nil || len(created.Links) != 1 || created.Links[0].ID != "l2" {
		t.Errorf("目标组应被创建且含 l2：%+v %v", created, err)
	}

	// 源分组/链接不存在 → ErrNotFound，且任何组的数据都不能丢
	if err := testStore.Links().MoveLink(ctx, "不存在源组", "B组", "l1"); err != store.ErrNotFound {
		t.Errorf("源组不存在应 ErrNotFound，得到 %v", err)
	}
	if err := testStore.Links().MoveLink(ctx, "B组", "A组", "no-such-link"); err != store.ErrNotFound {
		t.Errorf("源链接不存在应 ErrNotFound，得到 %v", err)
	}
	gb, _ = testStore.Links().GroupByName(ctx, "B组")
	if len(gb.Links) != 1 || gb.Links[0].ID != "l1" {
		t.Errorf("失败的移动不能动数据：%+v", gb.Links)
	}

	// SetLatency：只更新指定 id，返回受影响条数
	n, err := testStore.Links().SetLatency(ctx, []store.LatencyUpdate{
		{ID: "l1", Latency: 12.5}, {ID: "no-such-id", Latency: 1},
	})
	if err != nil || n != 1 {
		t.Errorf("SetLatency 应只更新 1 条，得到 %d（err=%v）", n, err)
	}
	gb, _ = testStore.Links().GroupByName(ctx, "B组")
	if gb.Links[0].Latency != 12.5 {
		t.Errorf("延迟未写入：%+v", gb.Links[0])
	}

	// 清理
	for _, g := range []string{"A组", "B组", "无order组", "自动建组"} {
		_, _ = testStore.Links().DeleteGroup(ctx, g)
	}
}

// ---- 说说 / 摘要 / 申请 / 用户 / 站点 ----

func TestIntegrationSpeaks(t *testing.T) {
	ctx := context.Background()
	for _, ts := range []int64{100, 300, 200} {
		if err := testStore.Speaks().Create(ctx, model.Speak{Time: ts, Content: "内容", PlainContent: "内容"}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := testStore.Speaks().Page(ctx, store.ListFilter{Page: 1, PageSize: 2})
	if err != nil || page.Count != 3 || len(page.Items) != 2 || page.Items[0].Time != 300 {
		t.Errorf("说说分页/倒序异常：%+v %v", page, err)
	}
	ok, err := testStore.Speaks().Update(ctx, 200, "改", "改")
	if err != nil || !ok {
		t.Errorf("更新失败：%v", err)
	}
	ok, _ = testStore.Speaks().Update(ctx, 999, "改", "改")
	if ok {
		t.Error("不存在的时间戳更新应返回 false")
	}
	for _, ts := range []int64{100, 200, 300} {
		if _, err := testStore.Speaks().Delete(ctx, ts); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIntegrationSummary(t *testing.T) {
	ctx := context.Background()
	if _, _, err := testStore.Summaries().Get(ctx, "s1"); err != store.ErrNotFound {
		t.Errorf("无缓存应 ErrNotFound，得到 %v", err)
	}
	at := time.Unix(1750000000, 0)
	if err := testStore.Summaries().Upsert(ctx, "s1", "摘要内容", at); err != nil {
		t.Fatal(err)
	}
	summary, refreshed, err := testStore.Summaries().Get(ctx, "s1")
	if err != nil || summary != "摘要内容" {
		t.Fatalf("摘要读回异常：%q %v", summary, err)
	}
	// lastRefreshTime 在库里是 Date（毫秒精度），秒级比较即可
	if refreshed.Unix() != at.Unix() {
		t.Errorf("refresh 时间异常：%v vs %v", refreshed, at)
	}
}

func TestIntegrationApplications(t *testing.T) {
	ctx := context.Background()
	id, err := testStore.Applications().Create(ctx, model.LinkApplication{
		Type: 0, Name: "申请站", Link: "https://app.example", Email: "app@example.com",
		Status: model.ApplicationStatusPending, CreatedAt: time.Now(),
	})
	if err != nil || id == "" {
		t.Fatalf("创建申请失败：%q %v", id, err)
	}
	// ByID：_id 必须是 ObjectID（空串/字符串主键会在这里暴露）
	if a, err := testStore.Applications().ByID(ctx, id); err != nil || a.Name != "申请站" {
		t.Fatalf("ByID 异常：%+v %v", a, err)
	}
	// 判重：归一化字段完全相同才命中
	dup, _ := testStore.Applications().FindIdentical(ctx, store.ApplicationIdentity{
		Type: 0, Name: "申请站", Link: "https://app.example", Email: "APP@example.com",
	})
	if dup == nil {
		t.Error("FindIdentical 应命中（邮箱大小写不敏感）")
	}
	// 初筛回写只作用于 pending
	if ok, err := testStore.Applications().SetScreening(ctx, id, model.AIScreening{Verdict: "pass", Model: "m"}); err != nil || !ok {
		t.Errorf("SetScreening 异常：%v %v", ok, err)
	}
	// 审批后 SetScreening 不再生效
	if ok, _ := testStore.Applications().Decide(ctx, id, model.ApplicationStatusApproved, "", time.Now()); !ok {
		t.Error("Decide 应命中 pending")
	}
	if ok, _ := testStore.Applications().SetScreening(ctx, id, model.AIScreening{Verdict: "review"}); ok {
		t.Error("已出结果的申请不该被初筛覆盖")
	}
	// PurgeDecidedBefore：只清已出结果且超期的，pending 不动
	old := time.Now().Add(-20 * 24 * time.Hour)
	future := time.Now().Add(time.Hour)
	pendingID, _ := testStore.Applications().Create(ctx, model.LinkApplication{
		Type: 0, Name: "老待审", Link: "https://old-pending.example",
		Status: model.ApplicationStatusPending, CreatedAt: old,
	})
	if n, err := testStore.Applications().PurgeDecidedBefore(ctx, future); err != nil || n != 1 {
		t.Errorf("应只清 1 条已出结果，得到 %d（err=%v）", n, err)
	}
	if _, err := testStore.Applications().ByID(ctx, pendingID); err != nil {
		t.Error("pending 不该被清理")
	}
	_, _ = testStore.Applications().DeleteDecidedByEmail(ctx, "none@example.com")
}

func TestIntegrationUsersAndSites(t *testing.T) {
	ctx := context.Background()
	if err := testStore.UpsertUser(ctx, "u1", "hash1"); err != nil {
		t.Fatal(err)
	}
	if u, err := testStore.Users().ByCredentials(ctx, "u1", "hash1"); err != nil || u.User != "u1" {
		t.Errorf("凭据匹配异常：%+v %v", u, err)
	}
	if _, err := testStore.Users().ByCredentials(ctx, "u1", "wrong-hash"); err != store.ErrNotFound {
		t.Errorf("密码错应 ErrNotFound，得到 %v", err)
	}
	v, err := testStore.Sites().SetInt(ctx, "latestUpdateTime", 1760000000)
	if err != nil || v != 1760000000 {
		t.Errorf("SetInt 异常：%d %v", v, err)
	}
	if got, err := testStore.Sites().GetInt(ctx, "latestUpdateTime"); err != nil || got != 1760000000 {
		t.Errorf("GetInt 异常：%d %v", got, err)
	}
}

func TestIntegrationExportAll(t *testing.T) {
	ctx := context.Background()
	seedPost(t, model.Post{Slug: "exp-1", Title: "导出", PublishTime: 1750000000})
	data, err := testStore.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	posts, ok := data["Posts"]
	if !ok || len(posts) == 0 {
		t.Fatalf("导出应包含 Posts：%v", len(data))
	}
	// 形态：_id 是字符串，Date 是 ISO 字符串（不是 {"$date":…}）
	var docs []map[string]any
	if err := jsonUnmarshalRaw(posts, &docs); err != nil {
		t.Fatal(err)
	}
	if _, ok := docs[0]["_id"].(string); !ok {
		t.Errorf("_id 应为字符串：%#v", docs[0]["_id"])
	}
	_, _ = testStore.Posts().Delete(ctx, "exp-1")
}

func itoaI(n int) string {
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
