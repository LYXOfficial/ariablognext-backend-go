package mongo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// errNotImplemented 标记「本阶段还没实现的仓储方法」。
// 编译期就保证接口齐全，行为上绝不静默返回假数据——宁可显式报错。
var errNotImplemented = errors.New("该仓储方法尚未实现（对应功能在后续阶段开放）")

// ---------------- 草稿 ----------------

type draftRepo struct {
	coll *mongo.Collection
	ro   bool
}

var _ store.DraftRepository = (*draftRepo)(nil)

func (r *draftRepo) Page(ctx context.Context, f store.DraftFilter) (store.Page[model.Draft], error) {
	if f.CountOnly {
		n, err := r.coll.CountDocuments(ctx, bson.M{})
		if err != nil {
			return store.Page[model.Draft]{}, wrapMongo(err)
		}
		return store.Page[model.Draft]{Count: n}, nil
	}
	opts := []options.Lister[options.FindOptions]{
		options.Find().SetSort(bson.D{{Key: "publishTime", Value: -1}}),
	}
	if f.Keyword != "" {
		opts = append(opts, options.Find())
	}
	cursor, err := r.coll.Find(ctx, draftQuery(f), opts...)
	if err != nil {
		return store.Page[model.Draft]{}, wrapMongo(err)
	}
	items := []model.Draft{}
	if err := cursor.All(ctx, &items); err != nil {
		return store.Page[model.Draft]{}, wrapMongo(err)
	}
	for i := range items {
		items[i].Normalize()
	}
	count, err := r.coll.CountDocuments(ctx, draftQuery(f))
	if err != nil {
		return store.Page[model.Draft]{}, wrapMongo(err)
	}
	page := store.Page[model.Draft]{Items: items, Count: count}
	if !f.Full && f.PageSize > 0 {
		page.HasMore = int64(f.Page*f.PageSize) < count
	}
	return page, nil
}

// draftQuery 组装草稿过滤。keyword 走标题模糊（等价旧 searchDraftsByTitle）；
// 投影排除 searchText，正文按 withContent 决定。
func draftQuery(f store.DraftFilter) bson.M {
	q := bson.M{}
	if f.Keyword != "" {
		q["title"] = bson.M{"$regex": regexpEscape(f.Keyword), "$options": "i"}
	}
	return q
}

func (r *draftRepo) BySlug(ctx context.Context, slug string) (*model.Draft, error) {
	var d model.Draft
	err := r.coll.FindOne(ctx, bson.M{"slug": slug}).Decode(&d)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, store.ErrNotFound
		}
		return nil, wrapMongo(err)
	}
	d.Normalize()
	return &d, nil
}

func (r *draftRepo) Slugs(ctx context.Context) ([]string, error) {
	cursor, err := r.coll.Find(ctx, bson.M{},
		options.Find().SetProjection(bson.M{"slug": 1}).SetSort(bson.D{{Key: "publishTime", Value: -1}}))
	if err != nil {
		return nil, wrapMongo(err)
	}
	docs := []struct {
		Slug string `bson:"slug"`
	}{}
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, wrapMongo(err)
	}
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.Slug)
	}
	return out, nil
}

func (r *draftRepo) Count(ctx context.Context) (int64, error) {
	return r.coll.CountDocuments(ctx, bson.M{})
}

// SearchTexts 实现 store.SearchTextReader（backfill 用）。
func (r *draftRepo) SearchTexts(ctx context.Context) (map[string]string, error) {
	cursor, err := r.coll.Find(ctx, bson.M{},
		options.Find().SetProjection(bson.M{"slug": 1, "searchText": 1}))
	if err != nil {
		return nil, wrapMongo(err)
	}
	rows := []struct {
		Slug       string `bson:"slug"`
		SearchText string `bson:"searchText"`
	}{}
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, wrapMongo(err)
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Slug] = row.SearchText
	}
	return out, nil
}

func (r *draftRepo) Create(ctx context.Context, d model.Draft) error {
	if r.ro {
		return store.ErrReadOnly
	}
	_, err := r.coll.InsertOne(ctx, d)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return store.ErrDuplicate
		}
		return wrapMongo(err)
	}
	return nil
}

func (r *draftRepo) UpdateMeta(ctx context.Context, slug string, m model.PostMeta) error {
	if r.ro {
		return store.ErrReadOnly
	}
	set := postMetaSet(m)
	res, err := r.coll.UpdateOne(ctx, bson.M{"slug": slug}, bson.M{"$set": set})
	if err != nil {
		return wrapMongo(err)
	}
	if res.MatchedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (r *draftRepo) UpdateContent(ctx context.Context, slug string, c model.PostContent) error {
	if r.ro {
		return store.ErrReadOnly
	}
	res, err := r.coll.UpdateOne(ctx, bson.M{"slug": slug}, bson.M{"$set": bson.M{
		"mdContent":    c.Markdown,
		"plainContent": c.PlainContent,
		"searchText":   c.SearchText,
		"wordCount":    c.WordCount,
	}})
	if err != nil {
		return wrapMongo(err)
	}
	if res.MatchedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (r *draftRepo) Delete(ctx context.Context, slug string) (bool, error) {
	if r.ro {
		return false, store.ErrReadOnly
	}
	res, err := r.coll.DeleteOne(ctx, bson.M{"slug": slug})
	if err != nil {
		return false, wrapMongo(err)
	}
	return res.DeletedCount > 0, nil
}

// ---------------- 摘要缓存 ----------------

type summaryRepo struct {
	coll *mongo.Collection
	ro   bool
}

var _ store.SummaryRepository = (*summaryRepo)(nil)

func (r *summaryRepo) Get(ctx context.Context, slug string) (string, time.Time, error) {
	// ⚠️ lastRefreshTime 在存量数据里是 mongo Date（旧后端写 datetime.now()），
	// 不是 unix 秒。用 bson.DateTime 解码才能同时吃下存量 Date；
	// 写侧 Upsert 也保持写 Date，跟旧数据同构，避免同字段两种类型。
	row := struct {
		Summary         string        `bson:"summary"`
		LastRefreshTime bson.DateTime `bson:"lastRefreshTime"`
	}{}
	err := r.coll.FindOne(ctx, bson.M{"slug": slug}).Decode(&row)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return "", time.Time{}, store.ErrNotFound
		}
		return "", time.Time{}, wrapMongo(err)
	}
	return row.Summary, row.LastRefreshTime.Time(), nil
}

func (r *summaryRepo) Upsert(ctx context.Context, slug, summary string, at time.Time) error {
	if r.ro {
		return store.ErrReadOnly
	}
	// 与存量数据同构：lastRefreshTime 写 mongo Date，不写 unix 秒
	_, err := r.coll.UpdateOne(ctx, bson.M{"slug": slug}, bson.M{"$set": bson.M{
		"slug": slug, "summary": summary, "lastRefreshTime": at,
	}}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return wrapMongo(err)
	}
	return nil
}

// ---------------- 说说 ----------------

type speakRepo struct {
	coll *mongo.Collection
	ro   bool
}

var _ store.SpeakRepository = (*speakRepo)(nil)

func (r *speakRepo) Page(ctx context.Context, f store.ListFilter) (store.Page[model.Speak], error) {
	if f.CountOnly {
		n, err := r.coll.CountDocuments(ctx, bson.M{})
		if err != nil {
			return store.Page[model.Speak]{}, wrapMongo(err)
		}
		return store.Page[model.Speak]{Count: n}, nil
	}
	cursor, err := r.coll.Find(ctx, bson.M{},
		options.Find().SetSort(bson.D{{Key: "time", Value: -1}}).
			SetSkip(int64((f.Page-1)*f.PageSize)).SetLimit(int64(f.PageSize)))
	if err != nil {
		return store.Page[model.Speak]{}, wrapMongo(err)
	}
	items := []model.Speak{}
	if err := cursor.All(ctx, &items); err != nil {
		return store.Page[model.Speak]{}, wrapMongo(err)
	}
	count, err := r.coll.CountDocuments(ctx, bson.M{})
	if err != nil {
		return store.Page[model.Speak]{}, wrapMongo(err)
	}
	return store.Page[model.Speak]{
		Items: items, Count: count,
		HasMore: int64(f.Page*f.PageSize) < count,
	}, nil
}

func (r *speakRepo) Count(ctx context.Context) (int64, error) {
	return r.coll.CountDocuments(ctx, bson.M{})
}

func (r *speakRepo) Create(ctx context.Context, s model.Speak) error {
	if r.ro {
		return store.ErrReadOnly
	}
	if _, err := r.coll.InsertOne(ctx, s); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return store.ErrDuplicate
		}
		return wrapMongo(err)
	}
	return nil
}

func (r *speakRepo) Update(ctx context.Context, time int64, content, plainContent string) (bool, error) {
	if r.ro {
		return false, store.ErrReadOnly
	}
	res, err := r.coll.UpdateOne(ctx, bson.M{"time": time},
		bson.M{"$set": bson.M{"content": content, "plainContent": plainContent}})
	if err != nil {
		return false, wrapMongo(err)
	}
	return res.MatchedCount > 0, nil
}

func (r *speakRepo) Delete(ctx context.Context, time int64) (bool, error) {
	if r.ro {
		return false, store.ErrReadOnly
	}
	res, err := r.coll.DeleteOne(ctx, bson.M{"time": time})
	if err != nil {
		return false, wrapMongo(err)
	}
	return res.DeletedCount > 0, nil
}

// ---------------- 友链 ----------------

type linkRepo struct {
	coll *mongo.Collection
	ro   bool
}

var _ store.LinkRepository = (*linkRepo)(nil)

func (r *linkRepo) Groups(ctx context.Context) ([]model.LinkGroup, error) {
	// 排序由数据库完成（旧后端是拉全量后在应用层 sort，已修正）。
	// 缺 order 字段的分组按 0 处理。
	cursor, err := r.coll.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "order", Value: 1}}))
	if err != nil {
		return nil, wrapMongo(err)
	}
	rows := []struct {
		Name        string       `bson:"name"`
		Description string       `bson:"description"`
		Order       *int         `bson:"order"`
		Links       []model.Link `bson:"links"`
	}{}
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, wrapMongo(err)
	}
	out := make([]model.LinkGroup, 0, len(rows))
	for _, row := range rows {
		order := 0
		if row.Order != nil {
			order = *row.Order
		}
		links := row.Links
		if links == nil {
			links = []model.Link{}
		}
		out = append(out, model.LinkGroup{Name: row.Name, Description: row.Description, Order: order, Links: links})
	}
	return out, nil
}

func (r *linkRepo) GroupByName(ctx context.Context, name string) (*model.LinkGroup, error) {
	groups, err := r.Groups(ctx)
	if err != nil {
		return nil, err
	}
	for i := range groups {
		if groups[i].Name == name {
			return &groups[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (r *linkRepo) LinkCount(ctx context.Context) (int64, error) {
	// 友链总数 = 分组里 links 数组长度之和，在数据库侧聚合
	pipeline := mongo.Pipeline{
		{{Key: "$project", Value: bson.M{"n": bson.M{"$size": bson.M{"$ifNull": []any{"$links", []any{}}}}}}},
		{{Key: "$group", Value: bson.M{"_id": nil, "total": bson.M{"$sum": "$n"}}}},
	}
	cursor, err := r.coll.Aggregate(ctx, pipeline)
	if err != nil {
		return 0, wrapMongo(err)
	}
	rows := []struct {
		Total int64 `bson:"total"`
	}{}
	if err := cursor.All(ctx, &rows); err != nil {
		return 0, wrapMongo(err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].Total, nil
}

func (r *linkRepo) FindByURL(ctx context.Context, url string) (*store.LinkRef, error) {
	groups, err := r.Groups(ctx)
	if err != nil {
		return nil, err
	}
	want := trimTrailingSlash(url)
	for _, g := range groups {
		for _, l := range g.Links {
			if trimTrailingSlash(l.URL) == want {
				return &store.LinkRef{Group: g.Name, Link: l}, nil
			}
		}
	}
	return nil, store.ErrNotFound
}

func (r *linkRepo) AddLink(ctx context.Context, group string, l model.Link) error {
	if r.ro {
		return store.ErrReadOnly
	}
	res, err := r.coll.UpdateOne(ctx, bson.M{"name": group},
		bson.M{"$push": bson.M{"links": l}})
	if err != nil {
		return wrapMongo(err)
	}
	if res.MatchedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (r *linkRepo) UpdateLink(ctx context.Context, group, id string, patch store.LinkPatch) (bool, error) {
	if r.ro {
		return false, store.ErrReadOnly
	}
	// links.$ 位置投影：定位到数组里匹配 id 的那条
	set := bson.M{}
	if patch.Name != "" {
		set["links.$.name"] = patch.Name
	}
	if patch.URL != "" {
		set["links.$.url"] = patch.URL
	}
	if patch.Avatar != "" {
		set["links.$.avatar"] = patch.Avatar
	}
	if patch.Description != "" {
		set["links.$.description"] = patch.Description
	}
	if patch.Color != "" {
		set["links.$.color"] = patch.Color
	}
	res, err := r.coll.UpdateOne(ctx,
		bson.M{"name": group, "links.id": id},
		bson.M{"$set": set})
	if err != nil {
		return false, wrapMongo(err)
	}
	return res.MatchedCount > 0, nil
}

// MoveLink 把友链从一组挪到另一组。Mongo 单机没有跨文档事务，
// 用「先 push 到目标组（不存在则自动创建）→ 再 pull 源组；pull 失败回滚 push」的补偿顺序。
//
// ⚠️ 目标组必须 upsert：早先这里没写 SetUpsert(true)，目标组不存在时 $push 匹配 0 条
// **不报错**，紧接着 $pull 却真的把源组里的链接删了 —— 链接凭空消失（集成测试抓到的数据丢失）。
// 契约/docs/testing.md §4.2 也明确要求「目标组不存在时自动创建」。
func (r *linkRepo) MoveLink(ctx context.Context, fromGroup, toGroup, id string) error {
	if r.ro {
		return store.ErrReadOnly
	}
	// 先确认源存在，避免「源根本没有却 push 成功」的中间态
	ref, err := r.FindByGroupAndID(ctx, fromGroup, id)
	if err != nil {
		return err
	}
	if _, err := r.coll.UpdateOne(ctx, bson.M{"name": toGroup},
		bson.M{"$push": bson.M{"links": ref}},
		options.UpdateOne().SetUpsert(true)); err != nil {
		return wrapMongo(err)
	}
	res, err := r.coll.UpdateOne(ctx, bson.M{"name": fromGroup},
		bson.M{"$pull": bson.M{"links": bson.M{"id": id}}})
	if err != nil || res.MatchedCount == 0 {
		// 回滚：把刚 push 进目标组的条目拿掉，源组保持原样
		_, _ = r.coll.UpdateOne(ctx, bson.M{"name": toGroup},
			bson.M{"$pull": bson.M{"links": bson.M{"id": id}}})
		if err != nil {
			return wrapMongo(err)
		}
		return store.ErrNotFound
	}
	return nil
}

// FindByGroupAndID 在指定分组里找链接，不存在返回 ErrNotFound。
func (r *linkRepo) FindByGroupAndID(ctx context.Context, group, id string) (*model.Link, error) {
	g, err := r.GroupByName(ctx, group)
	if err != nil {
		return nil, err
	}
	for _, l := range g.Links {
		if l.ID == id {
			return &l, nil
		}
	}
	return nil, store.ErrNotFound
}

func (r *linkRepo) DeleteLink(ctx context.Context, group, id string) (bool, error) {
	if r.ro {
		return false, store.ErrReadOnly
	}
	res, err := r.coll.UpdateOne(ctx, bson.M{"name": group},
		bson.M{"$pull": bson.M{"links": bson.M{"id": id}}})
	if err != nil {
		return false, wrapMongo(err)
	}
	return res.MatchedCount > 0, nil
}

func (r *linkRepo) AddGroup(ctx context.Context, g model.LinkGroup) error {
	if r.ro {
		return store.ErrReadOnly
	}
	// 重名靠查询拦截：name 上没有 unique 索引（复数分组文档是数组外层）
	if _, err := r.GroupByName(ctx, g.Name); err == nil {
		return store.ErrDuplicate
	}
	g.Links = []model.Link{}
	if _, err := r.coll.InsertOne(ctx, bson.M{
		"name": g.Name, "description": g.Description, "order": g.Order, "links": g.Links,
	}); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return store.ErrDuplicate
		}
		return wrapMongo(err)
	}
	return nil
}

func (r *linkRepo) UpdateGroup(ctx context.Context, oldName string, g model.LinkGroup) (bool, error) {
	if r.ro {
		return false, store.ErrReadOnly
	}
	set := bson.M{"name": g.Name, "description": g.Description, "order": g.Order}
	res, err := r.coll.UpdateOne(ctx, bson.M{"name": oldName}, bson.M{"$set": set})
	if err != nil {
		return false, wrapMongo(err)
	}
	return res.MatchedCount > 0, nil
}

func (r *linkRepo) DeleteGroup(ctx context.Context, name string) (bool, error) {
	if r.ro {
		return false, store.ErrReadOnly
	}
	res, err := r.coll.DeleteOne(ctx, bson.M{"name": name})
	if err != nil {
		return false, wrapMongo(err)
	}
	return res.DeletedCount > 0, nil
}

func (r *linkRepo) SetLatency(ctx context.Context, updates []store.LatencyUpdate) (int64, error) {
	if r.ro {
		return 0, store.ErrReadOnly
	}
	var updated int64
	for _, u := range updates {
		res, err := r.coll.UpdateMany(ctx,
			bson.M{"links": bson.M{"$elemMatch": bson.M{"id": u.ID}}},
			bson.M{"$set": bson.M{"links.$.latency": u.Latency}})
		if err != nil {
			return updated, wrapMongo(err)
		}
		updated += res.ModifiedCount
	}
	return updated, nil
}

// ---------------- 友链申请 ----------------

type applicationRepo struct {
	coll *mongo.Collection
	ro   bool
}

var _ store.ApplicationRepository = (*applicationRepo)(nil)

func (r *applicationRepo) Create(ctx context.Context, a model.LinkApplication) (string, error) {
	if r.ro {
		return "", store.ErrReadOnly
	}
	// ⚠️ 不能直接 InsertOne(a)：结构体上 ID 字段对应 bson:"_id"，
	// 空字符串会被驱动当成「显式主键 ""」，导致 InsertedID 是 string 而不是 ObjectID，
	// 后续 ByID/Decide 也全部对不上。这里显式构造文档、不带 _id，让驱动生成 ObjectID。
	doc := bson.M{
		"type": a.Type, "name": a.Name, "link": a.Link, "avatar": a.Avatar,
		"descr": a.Descr, "color": a.Color, "email": a.Email,
		"status": a.Status, "reason": a.Reason, "createdAt": a.CreatedAt,
		"ip": a.IP,
	}
	if a.TargetURL != nil {
		doc["targetUrl"] = *a.TargetURL
	}
	if a.TargetGroup != nil {
		doc["targetGroup"] = *a.TargetGroup
	}
	if a.TargetID != nil {
		doc["targetId"] = *a.TargetID
	}
	if a.DecidedAt != nil {
		doc["decidedAt"] = *a.DecidedAt
	}
	res, err := r.coll.InsertOne(ctx, doc)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return "", store.ErrDuplicate
		}
		return "", wrapMongo(err)
	}
	if oid, ok := res.InsertedID.(bson.ObjectID); ok {
		return oid.Hex(), nil
	}
	return fmt.Sprint(res.InsertedID), nil
}

func (r *applicationRepo) ByID(ctx context.Context, id string) (*model.LinkApplication, error) {
	oid, err := primitiveFromHex(id)
	if err != nil {
		return nil, store.ErrNotFound
	}
	var a model.LinkApplication
	if err := r.coll.FindOne(ctx, bson.M{"_id": oid}).Decode(&a); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, store.ErrNotFound
		}
		return nil, wrapMongo(err)
	}
	return &a, nil
}

func (r *applicationRepo) CountPending(ctx context.Context) (int64, error) {
	return r.coll.CountDocuments(ctx, bson.M{"status": model.ApplicationStatusPending})
}

func (r *applicationRepo) ListPending(ctx context.Context, limit int) ([]model.LinkApplication, error) {
	// 按 createdAt 升序：最早的先审
	cursor, err := r.coll.Find(ctx, bson.M{"status": model.ApplicationStatusPending},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, wrapMongo(err)
	}
	out := []model.LinkApplication{}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, wrapMongo(err)
	}
	return out, nil
}

func (r *applicationRepo) FindIdentical(ctx context.Context, ident store.ApplicationIdentity) (*model.LinkApplication, error) {
	// 待审队列上限 20 条：全量取出在内存里逐字段比较，足够快也不用建复合索引
	pending, err := r.ListPending(ctx, 1000)
	if err != nil {
		return nil, err
	}
	for i := range pending {
		a := &pending[i]
		targetID := ""
		if a.TargetID != nil {
			targetID = *a.TargetID
		}
		if a.Type == ident.Type && a.Name == ident.Name && a.Link == ident.Link &&
			a.Avatar == ident.Avatar && a.Descr == ident.Descr && a.Color == ident.Color &&
			lower(a.Email) == lower(ident.Email) && targetID == ident.TargetID {
			cp := *a
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *applicationRepo) FindByContact(ctx context.Context, email, name string, limit int) ([]model.LinkApplication, error) {
	// 至少给一个条件；两个都给时按「或」匹配（申请人可能只记得站名或邮箱之一）
	conds := []bson.M{}
	if email != "" {
		conds = append(conds, bson.M{"email": bson.M{"$regex": regexpEscape(email), "$options": "i"}})
	}
	if name != "" {
		conds = append(conds, bson.M{"name": bson.M{"$regex": regexpEscape(name), "$options": "i"}})
	}
	if len(conds) == 0 {
		return []model.LinkApplication{}, nil
	}
	q := bson.M{"$or": conds}
	cursor, err := r.coll.Find(ctx, q,
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, wrapMongo(err)
	}
	out := []model.LinkApplication{}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, wrapMongo(err)
	}
	return out, nil
}

func (r *applicationRepo) SetScreening(ctx context.Context, id string, s model.AIScreening) (bool, error) {
	if r.ro {
		return false, store.ErrReadOnly
	}
	oid, err := primitiveFromHex(id)
	if err != nil {
		return false, nil
	}
	// 只更新 pending：已出结果的申请不许被初筛结果覆盖
	res, err := r.coll.UpdateOne(ctx,
		bson.M{"_id": oid, "status": model.ApplicationStatusPending},
		bson.M{"$set": bson.M{"aiScreening": s}})
	if err != nil {
		return false, wrapMongo(err)
	}
	return res.MatchedCount > 0, nil
}

func (r *applicationRepo) Decide(ctx context.Context, id, status, reason string, at time.Time) (bool, error) {
	if r.ro {
		return false, store.ErrReadOnly
	}
	oid, err := primitiveFromHex(id)
	if err != nil {
		return false, nil
	}
	res, err := r.coll.UpdateOne(ctx,
		bson.M{"_id": oid, "status": model.ApplicationStatusPending},
		bson.M{"$set": bson.M{"status": status, "reason": reason, "decidedAt": at}})
	if err != nil {
		return false, wrapMongo(err)
	}
	return res.MatchedCount > 0, nil
}

func (r *applicationRepo) DeleteDecidedByEmail(ctx context.Context, email string) (int64, error) {
	if r.ro {
		return 0, store.ErrReadOnly
	}
	// 同一邮箱重新提交时清掉旧的已出结果记录（pending 不动）
	res, err := r.coll.DeleteMany(ctx, bson.M{
		"email":  bson.M{"$regex": "^" + regexpEscape(email) + "$", "$options": "i"},
		"status": bson.M{"$ne": model.ApplicationStatusPending},
	})
	if err != nil {
		return 0, wrapMongo(err)
	}
	return res.DeletedCount, nil
}

func (r *applicationRepo) PurgeDecidedBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	if r.ro {
		return 0, store.ErrReadOnly
	}
	res, err := r.coll.DeleteMany(ctx, bson.M{
		"status":    bson.M{"$ne": model.ApplicationStatusPending},
		"createdAt": bson.M{"$lt": cutoff},
	})
	if err != nil {
		return 0, wrapMongo(err)
	}
	return res.DeletedCount, nil
}

// ---------------- 用户 / 站点信息 ----------------

type userRepo struct {
	coll *mongo.Collection
}

var _ store.UserRepository = (*userRepo)(nil)

func (r *userRepo) ByCredentials(ctx context.Context, user, passwordHex string) (*model.User, error) {
	// 密码哈希作为查询条件而不是取出来比对：多一次恒定时间比较没有意义，
	// 但少一次「把哈希读进内存」的暴露面
	var u model.User
	err := r.coll.FindOne(ctx, bson.M{"user": user, "password": passwordHex}).Decode(&u)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, store.ErrNotFound
		}
		return nil, wrapMongo(err)
	}
	return &u, nil
}

type siteRepo struct {
	coll *mongo.Collection
	ro   bool
}

var _ store.SiteRepository = (*siteRepo)(nil)

func (r *siteRepo) GetInt(ctx context.Context, key string) (int64, error) {
	row := struct {
		Value int64 `bson:"value"`
	}{}
	err := r.coll.FindOne(ctx, bson.M{"key": key}).Decode(&row)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return 0, store.ErrNotFound
		}
		return 0, wrapMongo(err)
	}
	return row.Value, nil
}

func (r *siteRepo) SetNow(ctx context.Context, key string) (int64, error) {
	return r.SetInt(ctx, key, time.Now().Unix())
}

// UpsertUser 实现 store.UserWriter：只给种子工具用。
func (r *userRepo) UpsertUser(ctx context.Context, user, passwordHex string) error {
	if _, err := r.coll.UpdateOne(ctx, bson.M{"user": user},
		bson.M{"$set": bson.M{"user": user, "password": passwordHex}},
		options.UpdateOne().SetUpsert(true)); err != nil {
		return wrapMongo(err)
	}
	return nil
}

func (r *siteRepo) SetInt(ctx context.Context, key string, value int64) (int64, error) {
	if r.ro {
		return 0, store.ErrReadOnly
	}
	_, err := r.coll.UpdateOne(ctx, bson.M{"key": key},
		bson.M{"$set": bson.M{"key": key, "value": value}},
		options.UpdateOne().SetUpsert(true))
	if err != nil {
		return 0, wrapMongo(err)
	}
	return value, nil
}

// ---------------- 小工具 ----------------

func trimTrailingSlash(s string) string { return strings.TrimRight(s, "/") }

func lower(s string) string { return strings.ToLower(s) }
