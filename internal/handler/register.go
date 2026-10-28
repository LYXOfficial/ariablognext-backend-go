package handler

import (
	"github.com/LYXOfficial/ariablognext-backend-go/internal/app"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/service"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

// Register 列出当前阶段的全部路由。
// 路由清单与 docs/api-draft.md §2 对齐；P1 覆盖全部公开只读接口。
func Register(a *app.App, cfg *config.Config, st store.Store, svc *service.Set, images *ImageStore) {
	routes := []app.Route{
		// 骨架
		{Method: "GET", Path: "/health", Auth: app.Public, Handler: health(st)},
		{Method: "GET", Path: "/status", Auth: app.Public, Handler: status(cfg, svc, func() int { return len(a.Routes()) })},
		// posts（读）
		{Method: "GET", Path: "/posts/list", Auth: app.Public, Handler: postsList(cfg, svc)},
		{Method: "GET", Path: "/posts/detail", Auth: app.Public, Handler: postsDetail(svc)},
		{Method: "GET", Path: "/posts/statistics", Auth: app.Public, Handler: postsStatistics(svc)},
		{Method: "GET", Path: "/posts/slugs", Auth: app.Public, Handler: postsSlugs(svc)},
		{Method: "GET", Path: "/posts/navigation", Auth: app.Public, Handler: postsNavigation(svc)},
		{Method: "GET", Path: "/posts/related", Auth: app.Public, Handler: postsRelated(svc)},
		{Method: "GET", Path: "/posts/summary", Auth: app.Public, Handler: postsSummaryGet(svc)},
		{Method: "GET", Path: "/posts/semantic", Auth: app.Public, Handler: postsSemantic(cfg, svc)},
		// 分类 / 标签 / 归档
		{Method: "GET", Path: "/tags/list", Auth: app.Public, Handler: tagsList(svc)},
		{Method: "GET", Path: "/categories/list", Auth: app.Public, Handler: categoriesList(svc)},
		{Method: "GET", Path: "/archives/list", Auth: app.Public, Handler: archivesList(svc)},
		// links（读）
		{Method: "GET", Path: "/links/list", Auth: app.Public, Handler: linksList(svc)},
		{Method: "GET", Path: "/links/theme-color", Auth: app.Public, Handler: linksThemeColor(svc)},
		{Method: "GET", Path: "/links/applications/status", Auth: app.Public, Handler: applicationsStatus(svc)},
		// speaks / site
		{Method: "GET", Path: "/speaks/list", Auth: app.Public, Handler: speaksList(svc)},
		{Method: "GET", Path: "/site/info", Auth: app.Public, Handler: siteInfo(svc)},
		// auth（P2）
		{Method: "POST", Path: "/auth/login", Auth: app.Public, Handler: authLogin(a, svc)},
		{Method: "GET", Path: "/auth/verify", Auth: app.Admin, Handler: authVerify(a)},
		// drafts（P2，全部 Admin）
		{Method: "GET", Path: "/drafts/list", Auth: app.Admin, Handler: draftsList(svc)},
		{Method: "GET", Path: "/drafts/detail", Auth: app.Admin, Handler: draftsDetail(svc)},
		{Method: "GET", Path: "/drafts/slugs", Auth: app.Admin, Handler: draftsSlugs(svc)},
		{Method: "POST", Path: "/drafts/create", Auth: app.Admin, Handler: draftsCreate(svc)},
		{Method: "PUT", Path: "/drafts/update", Auth: app.Admin, Handler: draftsUpdate(svc)},
		{Method: "PUT", Path: "/drafts/content", Auth: app.Admin, Handler: draftsContent(svc)},
		{Method: "DELETE", Path: "/drafts/delete", Auth: app.Admin, Handler: draftsDelete(svc)},
		// posts 写接口（P2）
		{Method: "POST", Path: "/posts/create", Auth: app.Admin, Handler: postsCreate(svc)},
		{Method: "PUT", Path: "/posts/update", Auth: app.Admin, Handler: postsUpdate(svc)},
		{Method: "PUT", Path: "/posts/content", Auth: app.Admin, Handler: postsContent(svc)},
		{Method: "DELETE", Path: "/posts/delete", Auth: app.Admin, Handler: postsDelete(svc)},
		{Method: "POST", Path: "/posts/summary", Auth: app.Admin, Handler: postsSummaryRefresh(svc)},
		// links 写接口（P2；latency=P4 / check=P3）
		{Method: "POST", Path: "/links/create", Auth: app.Admin, Handler: linksCreate(svc)},
		{Method: "PUT", Path: "/links/update", Auth: app.Admin, Handler: linksUpdate(svc)},
		{Method: "DELETE", Path: "/links/delete", Auth: app.Admin, Handler: linksDelete(svc)},
		{Method: "POST", Path: "/links/move", Auth: app.Admin, Handler: linksMove(svc)},
		{Method: "POST", Path: "/links/groups/create", Auth: app.Admin, Handler: linksGroupsCreate(svc)},
		{Method: "PUT", Path: "/links/groups/update", Auth: app.Admin, Handler: linksGroupsUpdate(svc)},
		{Method: "DELETE", Path: "/links/groups/delete", Auth: app.Admin, Handler: linksGroupsDelete(svc)},
		// applications 后台接口（P2；apply=P3 / screening=P3）
		{Method: "GET", Path: "/links/applications/list", Auth: app.Admin, Handler: applicationsList(svc)},
		{Method: "POST", Path: "/links/applications/decide", Auth: app.Admin, Handler: applicationsDecide(svc)},
		// speaks 写接口（P2）
		{Method: "POST", Path: "/speaks/create", Auth: app.Admin, Handler: speaksCreate(svc)},
		{Method: "PUT", Path: "/speaks/update", Auth: app.Admin, Handler: speaksUpdate(svc)},
		{Method: "DELETE", Path: "/speaks/delete", Auth: app.Admin, Handler: speaksDelete(svc)},
		// site 写接口（P2）：latestUpdateTime 语义见 service.SiteService 注释
		{Method: "POST", Path: "/site/update-time", Auth: app.Admin, Handler: siteUpdateTime(svc)},
		// 友链申请（P3）：公开 apply（限流 5/30m 已登记）+ 内部 screening 回写
		{Method: "POST", Path: "/links/applications/apply", Auth: app.Public, Handler: applicationsApply(svc)},
		{Method: "POST", Path: "/links/applications/screening", Auth: app.Internal, Handler: applicationsScreening(svc)},
		// 图床（P3）
		{Method: "GET", Path: "/images/list", Auth: app.Admin, Handler: images.List()},
		{Method: "POST", Path: "/images/upload", Auth: app.Admin, Handler: images.Upload()},
		{Method: "PUT", Path: "/images/reupload", Auth: app.Admin, Handler: images.Reupload()},
		{Method: "POST", Path: "/images/import", Auth: app.Admin, Handler: images.Import()},
		{Method: "DELETE", Path: "/images/delete", Auth: app.Admin, Handler: images.Delete()},
		{Method: "POST", Path: "/links/check", Auth: app.Admin, Handler: linksCheck(cfg)},
		// 收尾（P4）
		{Method: "GET", Path: "/sitemap", Auth: app.Public, Handler: sitemap(st, cfg)},
		{Method: "POST", Path: "/maintenance/export", Auth: app.Internal, Handler: maintenanceExport(st, cfg)},
		{Method: "POST", Path: "/links/latency", Auth: app.Internal, Handler: linksLatency(svc)},
	}
	for _, r := range routes {
		a.Add(r)
	}
}

// parseListParams 解析 list 类接口共用的分页参数。
// 返回 page / pageSize / countOnly；校验失败直接返回 apperr。
func parseListParams(c *app.Ctx, allowFull bool) (int, app.PageSize, bool, error) {
	page, err := c.Int("page", 1, 1, 1_000_000)
	if err != nil {
		return 0, app.PageSize{}, false, err
	}
	ps, err := c.PageSize()
	if err != nil {
		return 0, app.PageSize{}, false, err
	}
	// full 只对 posts/drafts/speaks 开放：其它资源传 full 直接拒绝（契约 §0.4），
	// 避免把图片目录这类大集合一次拉爆
	if ps.Full && !allowFull {
		return 0, app.PageSize{}, false, apperr.InvalidField("pageSize", "该资源不支持 full，请使用 1–100 的分页")
	}
	countOnly, err := c.Bool("countOnly", false)
	if err != nil {
		return 0, app.PageSize{}, false, err
	}
	return page, ps, countOnly, nil
}
