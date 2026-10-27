package handler

import (
	"strconv"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/app"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/service"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

// loginBody：POST /auth/login。失败统一 403 auth.locked（与密码错误同文案，
// 不暴露锁定状态——契约 §0.3）。
type loginBody struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

func authLogin(a *app.App, svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var body loginBody
		if err := c.Bind(&body); err != nil {
			return nil, err
		}
		if body.User == "" || body.Password == "" {
			return nil, apperr.New(apperr.ValidationMissingField, "请填写用户名和密码", nil)
		}
		token, err := svc.Login.Login(c.Context(), c.IP(), body.User, body.Password)
		if err != nil {
			return nil, err
		}
		return map[string]any{"token": token}, nil
	}
}

// authVerify：GET /auth/verify → {"user": ..., "exp": unix秒}。
// apperrFieldDetail 是 handler 层的字段校验 details 构造（与 service.fieldDetail 同形）。
func apperrFieldDetail(field, msg string) apperr.FieldDetails {
	return apperr.FieldDetails{Fields: []apperr.FieldError{{Field: field, Message: msg}}}
}

func authVerify(a *app.App) app.Handler {
	return func(c *app.Ctx) (any, error) {
		// 走到这里说明中间件已验过签；exp 从 Authorization 头再解一次拿数值
		claims, err := a.JWT().Verify(c.Header("Authorization"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"user": c.User, "exp": claims.Exp}, nil
	}
}

// ---------------- 草稿（全部 Admin） ----------------

func draftsList(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		page, ps, countOnly, err := parseListParams(c, true)
		if err != nil {
			return nil, err
		}
		withContent, err := c.Bool("withContent", false)
		if err != nil {
			return nil, err
		}
		res, err := svc.Drafts.List(c.Context(), store.DraftFilter{
			Page:        page,
			PageSize:    ps.N,
			Full:        ps.Full,
			CountOnly:   countOnly,
			Keyword:     c.Query("keyword"),
			WithContent: withContent,
		})
		if err != nil {
			return nil, err
		}
		if countOnly {
			return app.WithMeta{Data: nil, Meta: app.NewCountMeta(res.Count)}, nil
		}
		return app.WithMeta{
			Data: res.Items,
			Meta: app.NewListMeta(page, ps, res.Count, res.HasMore),
		}, nil
	}
}

func draftsDetail(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		slug, err := c.RequiredQuery("slug")
		if err != nil {
			return nil, err
		}
		return svc.Drafts.Detail(c.Context(), slug)
	}
}

func draftsSlugs(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		return svc.Drafts.Slugs(c.Context())
	}
}

// postBody 与旧 UpdatePostRequestBody 对齐：新建只写元信息。
type postBody struct {
	Slug            string   `json:"slug"`
	Title           string   `json:"title"`
	Category        string   `json:"category"`
	Tags            []string `json:"tags"`
	BannerImg       string   `json:"bannerImg"`
	Description     *string  `json:"description"`
	CoverFit        *string  `json:"coverFit"`
	PublishTime     int64    `json:"publishTime"`
	LastUpdatedTime int64    `json:"lastUpdatedTime"`
}

func (b postBody) toPost() model.Post {
	return model.Post{
		Slug: b.Slug, Title: b.Title, Category: b.Category, Tags: b.Tags,
		BannerImg: b.BannerImg, Description: b.Description, CoverFit: b.CoverFit,
		PublishTime: b.PublishTime, LastUpdatedTime: b.LastUpdatedTime,
	}
}

func (b postBody) toDraft() model.Draft {
	return model.Draft{
		Slug: b.Slug, Title: b.Title, Category: b.Category, Tags: b.Tags,
		BannerImg: b.BannerImg, Description: b.Description, CoverFit: b.CoverFit,
		PublishTime: b.PublishTime, LastUpdatedTime: b.LastUpdatedTime,
	}
}

// (b postBody) toMeta 把非空字段挑进 PostMeta（局部更新语义：没传的字段不动）。
func (b postBody) toMeta(hasField map[string]bool) model.PostMeta {
	m := model.PostMeta{}
	if hasField["title"] {
		m.Title = &b.Title
	}
	if hasField["category"] {
		m.Category = &b.Category
	}
	if hasField["tags"] {
		m.Tags = b.Tags
	}
	if hasField["bannerImg"] {
		m.BannerImg = &b.BannerImg
	}
	if hasField["description"] {
		m.Description = b.Description
	}
	if hasField["coverFit"] {
		m.CoverFit = b.CoverFit
	}
	if hasField["publishTime"] {
		m.PublishTime = &b.PublishTime
	}
	if hasField["lastUpdatedTime"] {
		m.LastUpdatedTime = &b.LastUpdatedTime
	}
	return m
}

func postsCreate(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var b postBody
		if err := c.Bind(&b); err != nil {
			return nil, err
		}
		if b.PublishTime == 0 {
			// 旧后台新建不传时间会存 0；给个兜底当前时间，列表排序不至于沉底
			b.PublishTime = time.Now().Unix()
		}
		if b.LastUpdatedTime == 0 {
			b.LastUpdatedTime = b.PublishTime
		}
		out, err := svc.Posts.CreatePost(c.Context(), b.toPost())
		if err != nil {
			return nil, err
		}
		// 契约 §3.1：create 返回 201
		return app.WithStatus{Status: 201, Body: out}, nil
	}
}

func postsUpdate(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var raw map[string]any
		if err := c.Bind(&raw); err != nil {
			return nil, err
		}
		slug, _ := raw["slug"].(string)
		if slug == "" {
			return nil, apperr.New(apperr.ValidationMissingField, "缺少 slug", apperrFieldDetail("slug", "必填"))
		}
		b := postBodyFromMap(raw)
		if err := svc.Posts.UpdatePostMeta(c.Context(), slug, b.toMeta(presentFields(raw))); err != nil {
			return nil, err
		}
		return map[string]any{"updated": true}, nil
	}
}

func draftsCreate(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var b postBody
		if err := c.Bind(&b); err != nil {
			return nil, err
		}
		if b.PublishTime == 0 {
			b.PublishTime = time.Now().Unix()
		}
		if b.LastUpdatedTime == 0 {
			b.LastUpdatedTime = b.PublishTime
		}
		out, err := svc.Drafts.CreateDraft(c.Context(), b.toDraft())
		if err != nil {
			return nil, err
		}
		return app.WithStatus{Status: 201, Body: out}, nil
	}
}

func draftsUpdate(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var raw map[string]any
		if err := c.Bind(&raw); err != nil {
			return nil, err
		}
		slug, _ := raw["slug"].(string)
		if slug == "" {
			return nil, apperr.New(apperr.ValidationMissingField, "缺少 slug", apperrFieldDetail("slug", "必填"))
		}
		b := postBodyFromMap(raw)
		if err := svc.Drafts.UpdateDraftMeta(c.Context(), slug, b.toMeta(presentFields(raw))); err != nil {
			return nil, err
		}
		return map[string]any{"updated": true}, nil
	}
}

// postBodyFromMap / presentFields：局部更新需要知道「哪些键出现了」，
// map 反序列化是唯一能区分「没传」和「零值」的形态。
func postBodyFromMap(raw map[string]any) postBody {
	var b postBody
	b.Slug, _ = raw["slug"].(string)
	b.Title, _ = raw["title"].(string)
	b.Category, _ = raw["category"].(string)
	b.BannerImg, _ = raw["bannerImg"].(string)
	if v, ok := raw["description"].(string); ok {
		b.Description = &v
	}
	if v, ok := raw["coverFit"].(string); ok {
		b.CoverFit = &v
	}
	if v, ok := raw["publishTime"].(float64); ok {
		b.PublishTime = int64(v)
	}
	if v, ok := raw["lastUpdatedTime"].(float64); ok {
		b.LastUpdatedTime = int64(v)
	}
	if tags, ok := raw["tags"].([]any); ok {
		b.Tags = []string{}
		for _, t := range tags {
			if s, ok := t.(string); ok {
				b.Tags = append(b.Tags, s)
			}
		}
	}
	return b
}

func presentFields(raw map[string]any) map[string]bool {
	out := map[string]bool{}
	for k := range raw {
		out[k] = true
	}
	return out
}

// contentBody：PUT /posts/content、PUT /drafts/content。
type contentBody struct {
	Slug     string `json:"slug"`
	Markdown string `json:"markdown"`
}

func postsContent(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var b contentBody
		if err := c.Bind(&b); err != nil {
			return nil, err
		}
		return svc.Posts.UpdatePostContent(c.Context(), b.Slug, b.Markdown)
	}
}

func draftsContent(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var b contentBody
		if err := c.Bind(&b); err != nil {
			return nil, err
		}
		return svc.Drafts.UpdateDraftContent(c.Context(), b.Slug, b.Markdown)
	}
}

func postsDelete(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		slug, err := c.RequiredQuery("slug")
		if err != nil {
			return nil, err
		}
		return svc.Posts.DeletePost(c.Context(), slug)
	}
}

func draftsDelete(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		slug, err := c.RequiredQuery("slug")
		if err != nil {
			return nil, err
		}
		return svc.Drafts.DeleteDraft(c.Context(), slug)
	}
}

// ---------------- 说说写操作 ----------------

func speaksCreate(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var body struct {
			Time    *int64 `json:"time"`
			Content string `json:"content"`
		}
		if err := c.Bind(&body); err != nil {
			return nil, err
		}
		if err := svc.Speaks.CreateSpeak(c.Context(), body.Content, body.Time); err != nil {
			return nil, err
		}
		return map[string]any{"created": true}, nil
	}
}

func speaksUpdate(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var body struct {
			Time    *int64 `json:"time"`
			Content string `json:"content"`
		}
		if err := c.Bind(&body); err != nil {
			return nil, err
		}
		if body.Time == nil {
			return nil, apperr.New(apperr.ValidationMissingField, "缺少 time", apperrFieldDetail("time", "必填"))
		}
		if err := svc.Speaks.UpdateSpeak(c.Context(), *body.Time, body.Content); err != nil {
			return nil, err
		}
		return map[string]any{"updated": true}, nil
	}
}

func speaksDelete(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		raw, err := c.RequiredQuery("time")
		if err != nil {
			return nil, err
		}
		ts, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || ts <= 0 {
			return nil, apperr.InvalidField("time", "必须是正的 unix 秒时间戳")
		}
		if err := svc.Speaks.DeleteSpeak(c.Context(), ts); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true}, nil
	}
}

// ---------------- site / applications ----------------

func siteUpdateTime(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var body struct {
			Time *int64 `json:"time"`
		}
		// body 整体可选：完全不传 body、传 {}、传 {"time":N} 都要能用
		//（旧胶水层回退时会原样转发无 body 的 POST；空 body 不当格式错误）
		if err := c.BindOptional(&body); err != nil {
			return nil, err
		}
		return svc.Sites.SetSiteUpdateTime(c.Context(), body.Time)
	}
}

func applicationsList(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		list, n, err := svc.Links.PendingApplications(c.Context())
		if err != nil {
			return nil, err
		}
		return app.WithMeta{Data: list, Meta: app.NewCountMeta(n)}, nil
	}
}

// decideBody：POST /links/applications/decide。
type decideBody struct {
	ID        string           `json:"id"`
	Decision  string           `json:"decision"`
	Group     string           `json:"group"`
	Overrides *store.LinkPatch `json:"overrides"`
	Reason    string           `json:"reason"`
}

func applicationsDecide(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var body decideBody
		if err := c.Bind(&body); err != nil {
			return nil, err
		}
		if body.ID == "" {
			return nil, apperr.New(apperr.ValidationMissingField, "缺少 id", apperrFieldDetail("id", "必填"))
		}
		return svc.Links.DecideApplication(c.Context(),
			body.ID, body.Decision, body.Group, body.Reason, body.Overrides)
	}
}

// links 写操作（latency/check/screening 属 P3/P4）
type linkBody struct {
	Group       string  `json:"group"`
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	URL         string  `json:"url"`
	Avatar      string  `json:"avatar"`
	Description string  `json:"description"`
	Color       string  `json:"color"`
	Latency     float64 `json:"latency"`
}

func linksCreate(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var b linkBody
		if err := c.Bind(&b); err != nil {
			return nil, err
		}
		if err := svc.Links.AddLink(c.Context(), b.Group, model.Link{
			ID: b.ID, Name: b.Name, URL: b.URL, Avatar: b.Avatar,
			Description: b.Description, Color: b.Color, Latency: b.Latency,
		}); err != nil {
			return nil, err
		}
		return map[string]any{"created": true}, nil
	}
}

func linksUpdate(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var b linkBody
		if err := c.Bind(&b); err != nil {
			return nil, err
		}
		if b.Group == "" || b.ID == "" {
			return nil, apperr.New(apperr.ValidationMissingField, "缺少 group 或 id", nil)
		}
		if err := svc.Links.UpdateLink(c.Context(), b.Group, b.ID, store.LinkPatch{
			Name: b.Name, URL: b.URL, Avatar: b.Avatar,
			Description: b.Description, Color: b.Color,
		}); err != nil {
			return nil, err
		}
		return map[string]any{"updated": true}, nil
	}
}

func linksDelete(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		group, err := c.RequiredQuery("group")
		if err != nil {
			return nil, err
		}
		id, err := c.RequiredQuery("id")
		if err != nil {
			return nil, err
		}
		return svc.Links.DeleteLink(c.Context(), group, id)
	}
}

func linksMove(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var body struct {
			FromGroup string `json:"fromGroup"`
			ToGroup   string `json:"toGroup"`
			ID        string `json:"id"`
		}
		if err := c.Bind(&body); err != nil {
			return nil, err
		}
		if err := svc.Links.MoveLink(c.Context(), body.FromGroup, body.ToGroup, body.ID); err != nil {
			return nil, err
		}
		return map[string]any{"moved": true}, nil
	}
}

func linksGroupsCreate(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Order       int    `json:"order"`
		}
		if err := c.Bind(&body); err != nil {
			return nil, err
		}
		if err := svc.Links.AddGroup(c.Context(), model.LinkGroup{
			Name: body.Name, Description: body.Description, Order: body.Order,
		}); err != nil {
			return nil, err
		}
		return map[string]any{"created": true}, nil
	}
}

func linksGroupsUpdate(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var body struct {
			OldName     string `json:"oldName"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Order       int    `json:"order"`
		}
		if err := c.Bind(&body); err != nil {
			return nil, err
		}
		if err := svc.Links.UpdateGroup(c.Context(), body.OldName, model.LinkGroup{
			Name: body.Name, Description: body.Description, Order: body.Order,
		}); err != nil {
			return nil, err
		}
		return map[string]any{"updated": true}, nil
	}
}

func linksGroupsDelete(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		name, err := c.RequiredQuery("name")
		if err != nil {
			return nil, err
		}
		return svc.Links.DeleteGroup(c.Context(), name)
	}
}
