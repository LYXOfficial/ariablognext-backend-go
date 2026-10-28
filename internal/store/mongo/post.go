package mongo

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/textutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// postRepo：Posts 集合仓储，顺带实现 TaxonomyRepository（同集合）。
// 只读模式（ro）时所有写方法第一行直接拒绝——这是硬闸门，一行都不能省。
type postRepo struct {
	coll *mongo.Collection
	ro   bool
}

// 编译期断言：postRepo 必须同时满足两个接口。
var (
	_ store.PostRepository     = (*postRepo)(nil)
	_ store.TaxonomyRepository = (*postRepo)(nil)
)

// postProjectionFor 组装列表投影：绝不拉 mdContent（平均 8.5KB/篇，纯流量灾难）。
// 搜索命中时反向引入 searchText——在 Go 里算 context 片段用，
// 它只进影子结构 postRow，不进 Post 模型，自然也不会出现在 JSON 里。
func postProjectionFor(f store.PostFilter) bson.M {
	p := bson.M{}
	if !f.WithContent {
		p["mdContent"] = 0
	}
	if f.Keyword == "" {
		p["searchText"] = 0
	}
	return p
}

// contextRadius 是契约写死的 context 片段半径（api-draft §3.1.1：前后各 50 字）。
const contextRadius = 50

// truncateListPlain 把列表场景的 plainContent 截到契约长度（201 字符）。
// 只在列表/搜索路径调用：详情接口返回全文（与旧后端一致）。
// 必须在读路径截——存量文章的 plainContent 没截过（实测最大 5.6KB），
// 16 篇列表会带回 ~60KB 无用流量，是旧后端响应的 5 倍大。
func truncateListPlain(p *model.Post) {
	if len(p.PlainContent) > plainContentReadLimit {
		runes := []rune(p.PlainContent)
		if len(runes) > plainContentReadLimit {
			p.PlainContent = string(runes[:plainContentReadLimit])
		}
	}
}

// plainContentReadLimit 与 service 层写入时的 plainContentLimit 同源（201 字符）。
const plainContentReadLimit = 201

// postRow 是「搜索模式」的扫描载体：Post 全字段 + searchText。
// 单独存在的原因：searchText 是查询基建字段，不能泄漏进对外模型。
type postRow struct {
	model.Post `bson:",inline"`
	SearchText string `bson:"searchText"`
}

// buildPostQuery 把 PostFilter 翻译成 Mongo 过滤条件。
// keyword 必须 QuoteMeta：用户输入当成字面子串匹配，不当成正则执行
// （旧后端的搜索正则里含未转义元字符，行为不可预测，这里顺带修掉）。
func buildPostQuery(f store.PostFilter, loc *time.Location) (bson.M, error) {
	q := bson.M{}
	if f.Category != "" {
		q["category"] = f.Category
	}
	if f.Tag != "" {
		// 数组字段直接等值匹配 = 「包含该元素」语义，无需 $elemMatch
		q["tags"] = f.Tag
	}
	if f.Year > 0 {
		// 月界在 Go 侧按 loc 算，绝不用 $toDate（UTC，和本地月界不一致）
		start, end := yearMonthRange(f.Year, f.Month, loc)
		q["publishTime"] = bson.M{"$gte": start, "$lt": end}
	}
	if kws := SplitKeywords(f.Keyword); len(kws) > 0 {
		field := "searchText"
		if f.TitleOnly {
			field = "title"
		}
		// 每个关键词一个「顶层条件」（title 直接放键；全文搜索是 $or 快慢双路）。
		// 单关键词直接放顶层；多关键词全部收进 $and。
		conds := make([]bson.M, 0, len(kws))
		for _, kw := range kws {
			conds = append(conds, keywordMatch(field, kw))
		}
		if len(conds) == 1 {
			for k, v := range conds[0] {
				q[k] = v // title 条件键为 "title"；全文条件键为 "$or"——都在顶层才合法
			}
		} else {
			q["$and"] = conds
		}
	}
	return q, nil
}

// keywordMatch 生成单个关键词的匹配条件。
// TitleOnly 只查 title（字段恒存在）；全文搜索查 searchText，但存量数据
// 没有这个字段——云端库只读（用户铁律：绝不写生产库），searchText 无法回填，
// 所以必须有 fallback：searchText 缺失时退回 title+plainContent 拼接匹配。
// $expr 全表扫描在 83 篇（甚至几千篇）的规模下完全可接受，不建全文索引。
func keywordMatch(field, keyword string) bson.M {
	if field == "title" {
		return bson.M{field: bson.M{"$regex": regexp.QuoteMeta(keyword), "$options": "i"}}
	}
	concat := bson.M{"$concat": []any{
		bson.M{"$ifNull": []any{"$title", ""}},
		" ",
		bson.M{"$ifNull": []any{"$plainContent", ""}},
	}}
	return bson.M{"$or": []bson.M{
		{field: bson.M{"$regex": regexp.QuoteMeta(keyword), "$options": "i"}},
		{"$and": []bson.M{
			{field: bson.M{"$exists": false}},
			{"$expr": bson.M{"$regexMatch": bson.M{
				"input":   concat,
				"regex":   regexp.QuoteMeta(keyword),
				"options": "i",
			}}},
		}},
	}}
}

// SplitKeywords 把搜索词拆成多个关键词：按空白分隔，每个都是字面子串。
//   - 多关键词 AND 语义（都要命中才算匹配），全空白返回空 = 不过滤；
//   - 每个关键词用 QuoteMeta 转义：`.*[](){}|?+` 这些元字符按字面处理。
//     既是正确性（用户搜 "c++ (编译)" 就是搜这几个字），也是注入面收缩——
//     不转义等于把查询栏开成正则注入面。
func SplitKeywords(raw string) []string {
	fields := strings.Fields(raw)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// yearMonthRange 计算年（或年+月）在 loc 时区下的 unix 秒区间 [start, end)。
// month 为 0 表示整年。time.Date 自动归一化 month+1（13月 = 次年1月），不用手写闰年。
func yearMonthRange(year, month int, loc *time.Location) (int64, int64) {
	if month > 0 {
		start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc)
		end := time.Date(year, time.Month(month+1), 1, 0, 0, 0, 0, loc)
		return start.Unix(), end.Unix()
	}
	start := time.Date(year, 1, 1, 0, 0, 0, 0, loc)
	end := time.Date(year+1, 1, 1, 0, 0, 0, 0, loc)
	return start.Unix(), end.Unix()
}

func (r *postRepo) Page(ctx context.Context, f store.PostFilter, loc *time.Location) (store.Page[model.Post], error) {
	// 关键词搜索统一走 Go 侧全文匹配（pageFullText）：
	//   - 生产库没有 searchText（云端只读不回填），正则只能落 title/plainContent，
	//     而 plainContent 截断到 201 字，正文中段永远搜不到；
	//   - 更重要的：关键词语义（中文子串、英文整词、n-gram 部分匹配）只在 Go 里有
	//     一份实现，mongo 正则无法表达，两套并存必然出现「开发环境能搜到、线上搜不到」。
	if len(SplitKeywords(f.Keyword)) > 0 {
		return r.pageFullText(ctx, f, loc)
	}

	q, err := buildPostQuery(f, loc)
	if err != nil {
		return store.Page[model.Post]{}, err
	}

	// countOnly：完全跳过 find，只跑一次 count（省流量模式的意义所在）
	if f.CountOnly {
		n, err := r.coll.CountDocuments(ctx, q)
		if err != nil {
			return store.Page[model.Post]{}, wrapMongo(err)
		}
		return store.Page[model.Post]{Count: n}, nil
	}

	findOpts := []options.Lister[options.FindOptions]{
		options.Find().SetProjection(postProjectionFor(f)),
	}
	if f.OrderAsc {
		findOpts = append(findOpts, options.Find().SetSort(bson.D{{Key: "publishTime", Value: 1}}))
	} else {
		findOpts = append(findOpts, options.Find().SetSort(bson.D{{Key: "publishTime", Value: -1}}))
	}
	if f.Full {
		// full：不分页；条数上限（300）由 service 层在 count 之后拦截，仓储不做业务判断
	} else {
		findOpts = append(findOpts,
			options.Find().SetSkip(int64((f.Page-1)*f.PageSize)),
			options.Find().SetLimit(int64(f.PageSize)))
	}

	count, err := r.coll.CountDocuments(ctx, q)
	if err != nil {
		return store.Page[model.Post]{}, wrapMongo(err)
	}

	cursor, err := r.coll.Find(ctx, q, findOpts...)
	if err != nil {
		return store.Page[model.Post]{}, wrapMongo(err)
	}
	// 搜索命中时 searchText 也在投影里（算 context 用），但 Post 模型没有这个字段，
	// 所以扫描进「带 searchText 的影子结构」再转模型；普通查询直接扫模型，省一次转换。
	var items []model.Post
	if err := cursor.All(ctx, &items); err != nil {
		return store.Page[model.Post]{}, wrapMongo(err)
	}
	if items == nil {
		items = []model.Post{}
	}
	for i := range items {
		items[i].Normalize()
		truncateListPlain(&items[i])
	}

	page := store.Page[model.Post]{Items: items, Count: count}
	if !f.Full && f.PageSize > 0 {
		page.HasMore = int64(f.Page*f.PageSize) < count
	}
	return page, nil
}

// pageFullText 是「库里没有 searchText」时的全文检索实现：
//  1. 先用第一个关键词在服务端预过滤（title/mdContent，大小写不敏感，QuoteMeta 字面匹配），
//     把候选集从「全部文章」缩到「粗命中」（几十 KB 而不是 700KB）——预过滤是匹配域的
//     超集（原始 mdContent ⊇ 清洗后文本），Go 侧仍会逐篇完整过滤，正确性不受影响；
//  2. 在 Go 里还原每篇的可检索文本：优先用已有 searchText，没有就用 title+mdContent 现算
//     （现算会剥掉 Markdown 与外挂标签，所以搜 "fold" 不会命中 <Folding> 这类结构）；
//  3. 多关键词 AND、titleOnly、排序、分页、context 片段全部在 Go 里完成。
//
// postRowProjection 是全文检索需要加载的字段集合（context 需要 searchText/mdContent）。
var postRowProjection = bson.M{
	"slug": 1, "title": 1, "category": 1, "tags": 1, "bannerImg": 1,
	"description": 1, "coverFit": 1, "publishTime": 1, "lastUpdatedTime": 1,
	"wordCount": 1, "plainContent": 1, "mdContent": 1, "searchText": 1,
}

// pageFullText 是关键词搜索的唯一实现（生产库没有 searchText，必须走 Go 侧全文匹配）。
// 结果分两层：精确（tier 2）在前、模糊/别名（tier 1）在后，由 service 层再拼上语义结果。
func (r *postRepo) pageFullText(ctx context.Context, f store.PostFilter, loc *time.Location) (store.Page[model.Post], error) {
	f.Aliases = r.gateAliases(ctx, f.Aliases)
	kws := SplitKeywords(f.Keyword)
	noKeyword := f
	noKeyword.Keyword = ""
	noKeyword.Aliases = nil

	exact, err := r.exactHits(ctx, f, noKeyword, kws, loc)
	if err != nil {
		return store.Page[model.Post]{}, err
	}
	// 模糊层始终参与（不只精确为空时）：用户要求「精确在前、模糊在后」，
	// 若只在精确为空时兜底，就无法呈现这种分层。
	var fuzzy []keywordHit
	if !f.TitleOnly {
		fuzzy, err = r.fuzzyHits(ctx, f, noKeyword, loc)
		if err != nil {
			return store.Page[model.Post]{}, err
		}
	}
	seen := make(map[string]bool, len(exact))
	for _, h := range exact {
		seen[h.post.Slug] = true
	}
	merged := exact
	for _, h := range fuzzy {
		if !seen[h.post.Slug] {
			merged = append(merged, h)
		}
	}

	count := int64(len(merged))
	if f.CountOnly {
		return store.Page[model.Post]{Count: count}, nil
	}
	items := buildKeywordItems(merged, f, strings.ToLower(kws[0]))
	page := store.Page[model.Post]{Items: items, Count: count}
	if !f.Full && f.PageSize > 0 {
		page.HasMore = int64(f.Page*f.PageSize) < count
	}
	return page, nil
}

// gateAliases 给普通词别名做稀有度门控；/正则/ 别名是显式概念规则，不受限。
func (r *postRepo) gateAliases(ctx context.Context, aliases []string) []string {
	if len(aliases) == 0 {
		return nil
	}
	total, err := r.coll.CountDocuments(ctx, bson.M{})
	if err != nil {
		return aliases // 统计失败不阻塞搜索，放行全部
	}
	kept := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		if _, isRegex := textutil.ParseAlias(alias); isRegex {
			kept = append(kept, alias)
			continue
		}
		re := bson.Regex{Pattern: regexp.QuoteMeta(strings.ToLower(alias)), Options: "i"}
		n, err := r.coll.CountDocuments(ctx, bson.M{"$or": bson.A{
			bson.M{"title": re}, bson.M{"mdContent": re},
		}})
		if err != nil || (total > 0 && n*4 > total) {
			continue // 统计失败或超过 25% 都丢弃（宁缺勿滥）
		}
		kept = append(kept, alias)
	}
	return kept
}

// loadRows 按过滤条件加载候选文档。
func (r *postRepo) loadRows(ctx context.Context, q bson.M) ([]postRow, error) {
	cursor, err := r.coll.Find(ctx, q, options.Find().SetProjection(postRowProjection))
	if err != nil {
		return nil, wrapMongo(err)
	}
	rows := []postRow{}
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, wrapMongo(err)
	}
	return rows, nil
}

// rowText 取一篇文档的可检索文本（searchText 优先，缺失则现算）。
func rowText(row postRow) string {
	if row.SearchText != "" {
		return row.SearchText
	}
	return textutil.BuildSearchText(row.Title, row.MdContent)
}

// exactHits：精确层。所有关键词都命中（中文子串/英文整词）才算 tier 2；
// 只有别名命中的算 tier 1（别名不是完整子串命中，排模糊档）。
func (r *postRepo) exactHits(ctx context.Context, f store.PostFilter, noKeyword store.PostFilter, kws []string, loc *time.Location) ([]keywordHit, error) {
	q, err := buildPostQuery(noKeyword, loc)
	if err != nil {
		return nil, err
	}
	if len(kws) > 0 {
		re := bson.Regex{Pattern: regexp.QuoteMeta(kws[0]), Options: "i"}
		if f.TitleOnly {
			q["title"] = re
		} else {
			q["$or"] = bson.A{bson.M{"title": re}, bson.M{"mdContent": re}}
		}
	}
	rows, err := r.loadRows(ctx, q)
	if err != nil {
		return nil, err
	}
	lowered := make([]string, len(kws))
	for i, kw := range kws {
		lowered[i] = strings.ToLower(kw)
	}

	matched := make([]keywordHit, 0, len(rows))
	for _, row := range rows {
		text := rowText(row)
		target := text
		if f.TitleOnly {
			target = strings.ToLower(row.Title)
		}
		ok := true
		for _, kw := range lowered {
			if !textutil.ContainsTerm(target, kw) {
				ok = false
				break
			}
		}
		snippet, weight, tier := lowered[0], 4, 2
		if !ok {
			snippet, weight, tier = "", 0, 1
			for _, alias := range f.Aliases {
				if m, w, sn := textutil.AliasMatch(text, alias); m {
					if w > 3 {
						w = 3
					}
					snippet, weight, tier = sn, w, 1
					break
				}
			}
			if snippet == "" {
				continue
			}
		}
		p := row.Post
		p.Normalize()
		matched = append(matched, keywordHit{post: p, text: text, weight: weight, tier: tier, snippet: snippet})
	}
	return matched, nil
}

// fuzzyHits：模糊层。中文 n-gram / 英文拼写容错 / 别名兜底，统一 tier 1。
func (r *postRepo) fuzzyHits(ctx context.Context, f store.PostFilter, noKeyword store.PostFilter, loc *time.Location) ([]keywordHit, error) {
	pattern := textutil.FuzzyRegexPattern(f.Keyword)
	if pattern == "" {
		return nil, nil
	}
	q, err := buildPostQuery(noKeyword, loc)
	if err != nil {
		return nil, err
	}
	re := bson.Regex{Pattern: pattern, Options: "i"}
	q["$or"] = bson.A{bson.M{"title": re}, bson.M{"mdContent": re}}
	rows, err := r.loadRows(ctx, q)
	if err != nil {
		return nil, err
	}
	texts := make([]string, 0, len(rows))
	for _, row := range rows {
		texts = append(texts, rowText(row))
	}
	total, err := r.coll.CountDocuments(ctx, bson.M{})
	if err != nil {
		return nil, wrapMongo(err)
	}
	stats := textutil.BuildStats(texts, f.Keyword)
	if int64(stats.N) < total {
		stats.N = int(total)
	}

	var hits []keywordHit
	for i, row := range rows {
		m := textutil.ScoreKeywordStats(texts[i], f.Keyword, stats)
		if !m.Matched {
			for _, alias := range f.Aliases {
				if ok, w, sn := textutil.AliasMatch(texts[i], alias); ok {
					if w > 3 {
						w = 3
					}
					m = textutil.KeywordMatch{Matched: true, Weight: w, Snippet: sn}
					break
				}
			}
		}
		if !m.Matched {
			continue
		}
		p := row.Post
		p.Normalize()
		hits = append(hits, keywordHit{post: p, text: texts[i], weight: m.Weight, tier: 1, snippet: m.Snippet})
	}
	return hits, nil
}

// keywordHit 把「文章 + 可检索文本 + 层级」绑在一起。
// 曾经是 matched[] 与 contexts[] 两个平行切片，排序只排了前者导致 context 错位
// （2026-10-28 线上故障：北京游记挂出重庆地铁的正文）。
type keywordHit struct {
	post    model.Post
	text    string
	weight  int // 同层内排序权重
	tier    int // 2 = 精确命中；1 = 模糊/别名
	snippet string
}

// buildKeywordItems 排序（层级 → 权重 → 时间）、分页，并把 context 绑回各自文章。
func buildKeywordItems(hits []keywordHit, f store.PostFilter, keyword string) []model.Post {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].tier != hits[j].tier {
			return hits[i].tier > hits[j].tier
		}
		if hits[i].weight != hits[j].weight {
			return hits[i].weight > hits[j].weight
		}
		if f.OrderAsc {
			return hits[i].post.PublishTime < hits[j].post.PublishTime
		}
		return hits[i].post.PublishTime > hits[j].post.PublishTime
	})

	start, end := 0, len(hits)
	if !f.Full {
		start = (f.Page - 1) * f.PageSize
		if start > len(hits) {
			start = len(hits)
		}
		end = start + f.PageSize
		if end > len(hits) {
			end = len(hits)
		}
	}
	items := make([]model.Post, 0, end-start)
	for i := start; i < end; i++ {
		hit := hits[i]
		p := hit.post
		snippet := hit.snippet
		if snippet == "" {
			snippet = keyword
		}
		if c := textutil.SearchContext(hit.text, snippet, contextRadius); c != "" {
			p.Context = &c
		}
		p.MatchTier = hit.tier
		if !f.WithContent {
			p.MdContent = ""
		}
		truncateListPlain(&p)
		items = append(items, p)
	}
	return items
}

func (r *postRepo) BySlug(ctx context.Context, slug string) (*model.Post, error) {
	var p model.Post
	err := r.coll.FindOne(ctx, bson.M{"slug": slug}).Decode(&p)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, store.ErrNotFound
		}
		return nil, wrapMongo(err)
	}
	p.Normalize()
	return &p, nil
}

func (r *postRepo) Slugs(ctx context.Context) ([]string, error) {
	// 只投影 slug 一列，83 条也就几 KB；按 publishTime 倒序保证输出稳定（与列表一致）
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

func (r *postRepo) Neighbours(ctx context.Context, slug string) (*model.PostBrief, *model.PostBrief, error) {
	// 先拿自己的 publishTime 做锚点，再各查一篇：两次点查走 publishTime 索引，
	// 比拉全列表到内存里找快得多
	self := struct {
		PublishTime int64 `bson:"publishTime"`
	}{}
	err := r.coll.FindOne(ctx, bson.M{"slug": slug},
		options.FindOne().SetProjection(bson.M{"publishTime": 1})).Decode(&self)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil, store.ErrNotFound
		}
		return nil, nil, wrapMongo(err)
	}

	fetch := func(filter bson.M, sortDir int) (*model.PostBrief, error) {
		var b model.PostBrief
		opts := options.FindOne().
			SetProjection(bson.M{"slug": 1, "title": 1, "bannerImg": 1, "publishTime": 1}).
			SetSort(bson.D{{Key: "publishTime", Value: sortDir}})
		err := r.coll.FindOne(ctx, filter, opts).Decode(&b)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, nil // 边界处没有相邻文章是正常情况，不是错误
			}
			return nil, wrapMongo(err)
		}
		return &b, nil
	}
	// newer：比自己新的里面最旧的一篇；older：比自己旧的里面最新的一篇
	newer, err := fetch(bson.M{"publishTime": bson.M{"$gt": self.PublishTime}}, 1)
	if err != nil {
		return nil, nil, err
	}
	older, err := fetch(bson.M{"publishTime": bson.M{"$lt": self.PublishTime}}, -1)
	if err != nil {
		return nil, nil, err
	}
	return newer, older, nil
}

func (r *postRepo) Related(ctx context.Context, slug string, limit int) ([]model.PostBrief, error) {
	// 无标签返回空切片（旧接口返回 404 是行为 bug，契约已修正）
	self, err := r.BySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	if len(self.Tags) == 0 {
		return []model.PostBrief{}, nil
	}
	projection := bson.M{"slug": 1, "title": 1, "bannerImg": 1, "publishTime": 1}
	cursor, err := r.coll.Find(ctx,
		bson.M{"slug": bson.M{"$ne": slug}, "tags": bson.M{"$in": self.Tags}},
		options.Find().SetProjection(projection).SetLimit(int64(limit)))
	if err != nil {
		return nil, wrapMongo(err)
	}
	out := []model.PostBrief{}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, wrapMongo(err)
	}
	return out, nil
}

func (r *postRepo) TotalWordCount(ctx context.Context) (int64, error) {
	// 聚合在数据库侧求和，不把 wordCount 拉回来
	pipeline := mongo.Pipeline{
		{{Key: "$group", Value: bson.M{
			"_id": nil,
			// wordCount 可空：缺失/null 的文档按 0 处理，不能让整个求和变 null
			"total": bson.M{"$sum": bson.M{"$ifNull": []any{"$wordCount", 0}}},
		}}},
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

func (r *postRepo) Create(ctx context.Context, p model.Post) error {
	if r.ro {
		return store.ErrReadOnly
	}
	doc := bson.M{
		"slug": p.Slug, "title": p.Title, "category": p.Category,
		"tags": orEmptySlice(p.Tags), "bannerImg": p.BannerImg,
		"description": nullableString(p.Description), "coverFit": nullableString(p.CoverFit),
		"publishTime": p.PublishTime, "lastUpdatedTime": p.LastUpdatedTime,
		"wordCount":    nullableInt(p.WordCount),
		"plainContent": p.PlainContent, "mdContent": p.MdContent,
	}
	// searchText 为空串时不写该字段——bson.M 不吃 omitempty，必须手动判断；
	// 空字段会让 $exists:false 的 fallback 判定失效，全文搜索直接漏结果
	if p.SearchText != "" {
		doc["searchText"] = p.SearchText
	}
	if _, err := r.coll.InsertOne(ctx, doc); err != nil {
		// 重复 slug = unique index 拦截，转成哨兵；绝不先 Count 再 Insert（竞态）
		if mongo.IsDuplicateKeyError(err) {
			return store.ErrDuplicate
		}
		return wrapMongo(err)
	}
	return nil
}

func (r *postRepo) UpdateMeta(ctx context.Context, slug string, m model.PostMeta) error {
	if r.ro {
		return store.ErrReadOnly
	}
	set := bson.M{}
	if m.Title != nil {
		set["title"] = *m.Title
	}
	if m.Category != nil {
		set["category"] = *m.Category
	}
	if m.Tags != nil {
		set["tags"] = orEmptySlice(m.Tags)
	}
	if m.BannerImg != nil {
		set["bannerImg"] = *m.BannerImg
	}
	if m.Description != nil {
		set["description"] = nullableString(m.Description)
	}
	if m.CoverFit != nil {
		set["coverFit"] = nullableString(m.CoverFit)
	}
	if m.PublishTime != nil {
		set["publishTime"] = *m.PublishTime
	}
	if m.LastUpdatedTime != nil {
		set["lastUpdatedTime"] = *m.LastUpdatedTime
	}
	res, err := r.coll.UpdateOne(ctx, bson.M{"slug": slug}, bson.M{"$set": set})
	if err != nil {
		return wrapMongo(err)
	}
	if res.MatchedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (r *postRepo) UpdateContent(ctx context.Context, slug string, c model.PostContent) error {
	if r.ro {
		return store.ErrReadOnly
	}
	// 派生字段（wordCount/plainContent/searchText）由 service 层算好一起传进来，
	// 仓储不做文本处理——保持这层「只管存取」。
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

func (r *postRepo) Delete(ctx context.Context, slug string) (bool, error) {
	if r.ro {
		return false, store.ErrReadOnly
	}
	// 幂等删除：不存在的 slug 也算成功
	res, err := r.coll.DeleteOne(ctx, bson.M{"slug": slug})
	if err != nil {
		return false, wrapMongo(err)
	}
	return res.DeletedCount > 0, nil
}

// SitemapEntries 实现 store.SitemapSource：只投影 slug 与 lastUpdatedTime，
// 83 篇约几百字节；lastUpdatedTime 缺失时退回 publishTime（存量数据可能有缺）。
func (r *postRepo) SitemapEntries(ctx context.Context) ([]store.SitemapEntry, error) {
	cursor, err := r.coll.Find(ctx, bson.M{},
		options.Find().SetProjection(bson.M{"slug": 1, "lastUpdatedTime": 1, "publishTime": 1}).
			SetSort(bson.D{{Key: "publishTime", Value: -1}}))
	if err != nil {
		return nil, wrapMongo(err)
	}
	rows := []struct {
		Slug            string `bson:"slug"`
		LastUpdatedTime int64  `bson:"lastUpdatedTime"`
		PublishTime     int64  `bson:"publishTime"`
	}{}
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, wrapMongo(err)
	}
	out := make([]store.SitemapEntry, 0, len(rows))
	for _, row := range rows {
		ts := row.LastUpdatedTime
		if ts == 0 {
			ts = row.PublishTime
		}
		out = append(out, store.SitemapEntry{Slug: row.Slug, LastUpdatedTime: ts})
	}
	return out, nil
}

// SearchTexts 实现 store.SearchTextReader（backfill 用）。
func (r *postRepo) SearchTexts(ctx context.Context) (map[string]string, error) {
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

// ---------------- 分类 / 标签 / 归档（同集合） ----------------

func (r *postRepo) Categories(ctx context.Context) ([]model.CategoryCount, error) {
	pipeline := mongo.Pipeline{
		{{Key: "$group", Value: bson.M{"_id": "$category", "count": bson.M{"$sum": 1}}}},
		{{Key: "$sort", Value: bson.M{"_id": 1}}},
	}
	cursor, err := r.coll.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, wrapMongo(err)
	}
	rows := []struct {
		Name  string `bson:"_id"`
		Count int    `bson:"count"`
	}{}
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, wrapMongo(err)
	}
	out := make([]model.CategoryCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, model.CategoryCount{Name: row.Name, Count: row.Count})
	}
	return out, nil
}

func (r *postRepo) Tags(ctx context.Context) ([]model.TagCount, error) {
	// 标签是数组：$unwind 展开后按元素计数
	pipeline := mongo.Pipeline{
		{{Key: "$unwind", Value: "$tags"}},
		{{Key: "$group", Value: bson.M{"_id": "$tags", "count": bson.M{"$sum": 1}}}},
		{{Key: "$sort", Value: bson.M{"_id": 1}}},
	}
	cursor, err := r.coll.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, wrapMongo(err)
	}
	rows := []struct {
		Name  string `bson:"_id"`
		Count int    `bson:"count"`
	}{}
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, wrapMongo(err)
	}
	out := make([]model.TagCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, model.TagCount{Name: row.Name, Count: row.Count})
	}
	return out, nil
}

func (r *postRepo) ArchiveBuckets(ctx context.Context, loc *time.Location) ([]model.ArchiveBucket, error) {
	// 只取 publishTime 一列（83 篇 ≈ 几百字节），在 Go 里按 loc 分桶。
	// ⚠️ 不要改回 MongoDB 的 $toDate + $year/$month：$toDate 是 UTC，
	// 和「本地哪个月」的语义不一致——这正是旧后端归档时区的 bug 根源。
	cursor, err := r.coll.Find(ctx, bson.M{}, options.Find().SetProjection(bson.M{"publishTime": 1}))
	if err != nil {
		return nil, wrapMongo(err)
	}
	docs := []struct {
		PublishTime int64 `bson:"publishTime"`
	}{}
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, wrapMongo(err)
	}
	counts := map[[2]int]int{}
	for _, d := range docs {
		t := time.Unix(d.PublishTime, 0).In(loc)
		counts[[2]int{t.Year(), int(t.Month())}]++
	}
	out := make([]model.ArchiveBucket, 0, len(counts))
	for k, c := range counts {
		out = append(out, model.ArchiveBucket{Year: k[0], Month: k[1], Count: c})
	}
	// 倒序：最新的年月排最前（2026-12 在 2026-01 之前）
	sort.Slice(out, func(i, j int) bool {
		if out[i].Year != out[j].Year {
			return out[i].Year > out[j].Year
		}
		return out[i].Month > out[j].Month
	})
	return out, nil
}

// ---------------- 小工具 ----------------

// wrapMongo 把驱动错误翻译成 store 哨兵；service 层只认哨兵。
// 已经是哨兵的错误原样透传（幂等），避免同一错误被二次包装出两层「数据库操作失败」。
func wrapMongo(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrDuplicate), errors.Is(err, store.ErrReadOnly):
		return err
	case mongo.IsDuplicateKeyError(err):
		return store.ErrDuplicate
	case errors.Is(err, mongo.ErrNoDocuments):
		return store.ErrNotFound
	default:
		return fmt.Errorf("数据库操作失败：%w", err)
	}
}

func orEmptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nullableString(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func nullableInt(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}
