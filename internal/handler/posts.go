package handler

import (
	"strconv"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/app"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/service"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

// wrapStore 把存储层哨兵转成对外错误（写接口阶段仍在用的兜底路径）。
// 读路径的 ErrNotFound 已由 service 按领域转成 post.not_found 等语义码。
func wrapStore(err error, fallback string) error {
	switch err {
	case nil:
		return nil
	case store.ErrNotFound:
		// 不该直接到这里：调用方应先按领域转。兜底成内部错误防止误报 404
		return apperr.New(apperr.InternalError, fallback, nil)
	case store.ErrDuplicate:
		return apperr.New(apperr.InternalError, fallback, nil)
	case store.ErrReadOnly:
		return apperr.New(apperr.StorageReadOnly,
			"服务正以只读模式运行（生产只读验证），写操作不可用", nil)
	default:
		return apperr.Wrap(apperr.InternalError, fallback, err)
	}
}

// postsList：GET /posts/list。参数绑定在这里，业务（full 上限、标题优先）在 service。
func postsList(cfg *config.Config, svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		page, ps, countOnly, err := parseListParams(c, true)
		if err != nil {
			return nil, err
		}
		category := c.Query("category")
		tag := c.Query("tag")
		year, err := c.Int("year", 0, 0, 9999)
		if err != nil {
			return nil, err
		}
		month, err := c.Int("month", 0, 0, 12)
		if err != nil {
			return nil, err
		}
		// 给 month 必须给 year：单独的「几月」跨年歧义，直接拒绝
		if month > 0 && year == 0 {
			return nil, apperr.InvalidField("month", "使用 month 时必须同时提供 year")
		}
		keyword := c.Query("keyword")
		if len([]rune(keyword)) > cfg.Search.MaxKeywordLength {
			return nil, apperr.InvalidField("keyword",
				"关键词最长 "+strconv.Itoa(cfg.Search.MaxKeywordLength)+" 字")
		}
		titleOnly, err := c.Bool("titleOnly", false)
		if err != nil {
			return nil, err
		}
		withContent, err := c.Bool("withContent", false)
		if err != nil {
			return nil, err
		}
		order := c.QueryDefault("order", "desc")
		if order != "asc" && order != "desc" {
			return nil, apperr.InvalidField("order", "只接受 asc 或 desc")
		}

		res, err := svc.Posts.List(c.Context(), store.PostFilter{
			Page:        page,
			PageSize:    ps.N,
			Full:        ps.Full,
			CountOnly:   countOnly,
			Category:    category,
			Tag:         tag,
			Year:        year,
			Month:       month,
			Keyword:     keyword,
			TitleOnly:   titleOnly,
			WithContent: withContent,
			OrderAsc:    order == "asc",
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

// postsDetail：GET /posts/detail?slug=。返回完整 Post（含 mdContent，后台编辑器也用它）。
func postsDetail(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		slug, err := c.RequiredQuery("slug")
		if err != nil {
			return nil, err
		}
		return svc.Posts.Detail(c.Context(), slug)
	}
}

// postsStatistics：GET /posts/statistics → {"totalWordCount": N}。
func postsStatistics(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		return svc.Posts.Statistics(c.Context())
	}
}

// postsSlugs：GET /posts/slugs → 全部 slug，不分页。
func postsSlugs(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		return svc.Posts.Slugs(c.Context())
	}
}

// postsNavigation：GET /posts/navigation?slug= → {newer, older}。
func postsNavigation(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		slug, err := c.RequiredQuery("slug")
		if err != nil {
			return nil, err
		}
		return svc.Posts.Navigation(c.Context(), slug)
	}
}

// postsRelated：GET /posts/related?slug=&limit=。
func postsRelated(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		slug, err := c.RequiredQuery("slug")
		if err != nil {
			return nil, err
		}
		limit, err := c.Int("limit", 6, 1, 20)
		if err != nil {
			return nil, err
		}
		return svc.Posts.Related(c.Context(), slug, limit)
	}
}

// postsSummaryGet：GET /posts/summary?slug=（缓存优先；miss 时生成一次并落库，
// 与旧 get/post/postSummary 一致；ai.summary 未启用时报 upstream.failed）。
func postsSummaryGet(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		slug, err := c.RequiredQuery("slug")
		if err != nil {
			return nil, err
		}
		// generate=false：只读缓存、绝不触发上游（后台预览用）。
		// 默认 false→true 的语义不变：不传时仍是「缓存优先，miss 生成一次」。
		generate, err := c.Bool("generate", true)
		if err != nil {
			return nil, err
		}
		if !generate {
			return svc.Posts.SummaryCached(c.Context(), slug)
		}
		return svc.Posts.SummaryGet(c.Context(), slug)
	}
}

// postsSummaryRefresh：POST /posts/summary 强制重生成（P3 接 SiliconFlow；
// 未配置时 upstream.failed——语义与 GET 未命中一致）。
func postsSummaryRefresh(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var body struct {
			Slug string `json:"slug"`
		}
		if err := c.Bind(&body); err != nil {
			return nil, err
		}
		if body.Slug == "" {
			return nil, apperr.New(apperr.ValidationMissingField, "缺少 slug",
				apperr.FieldDetails{Fields: []apperr.FieldError{{Field: "slug", Message: "必填"}}})
		}
		// 强制重生成（管理员）：旧 update/post/refreshSummary 的语义
		return svc.Posts.SummaryGenerate(c.Context(), body.Slug)
	}
}

// postsSemanticStub：GET /posts/semantic 固定 501（契约预留位，避免以后改契约）。
func postsSemanticStub(c *app.Ctx) (any, error) {
	return nil, apperr.New(apperr.SearchNotImplemented, "语义搜索还没做，先用关键词搜索", nil)
}
