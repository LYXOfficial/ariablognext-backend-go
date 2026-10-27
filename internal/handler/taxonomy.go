package handler

import (
	"github.com/LYXOfficial/ariablognext-backend-go/internal/app"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/service"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

// taxonomy：分类 / 标签 / 归档三件套。
// meta.count 是「去重后的种类数」，每项的 count 是该标签/分类下的文章数（契约 §3.3）。
// countOnly=true 时只返回种类数，data 为 null。

func categoriesList(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		countOnly, err := c.Bool("countOnly", false)
		if err != nil {
			return nil, err
		}
		list, err := svc.Posts.Categories(c.Context())
		if err != nil {
			return nil, err
		}
		if countOnly {
			return app.WithMeta{Data: nil, Meta: app.NewCountMeta(int64(len(list)))}, nil
		}
		// 契约 §3.3：非 countOnly 也要带 meta.count（= 去重后的分类数）
		return app.WithMeta{Data: list, Meta: app.NewCountMeta(int64(len(list)))}, nil
	}
}

func tagsList(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		countOnly, err := c.Bool("countOnly", false)
		if err != nil {
			return nil, err
		}
		list, err := svc.Posts.Tags(c.Context())
		if err != nil {
			return nil, err
		}
		if countOnly {
			return app.WithMeta{Data: nil, Meta: app.NewCountMeta(int64(len(list)))}, nil
		}
		// 契约 §3.3：非 countOnly 也要带 meta.count（= 去重后的标签数）
		return app.WithMeta{Data: list, Meta: app.NewCountMeta(int64(len(list)))}, nil
	}
}

// archivesList：GET /archives/list → [{year, month, count}]，倒序（上海时区）。
func archivesList(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		countOnly, err := c.Bool("countOnly", false)
		if err != nil {
			return nil, err
		}
		buckets, err := svc.Posts.Archives(c.Context())
		if err != nil {
			return nil, err
		}
		if countOnly {
			return app.WithMeta{Data: nil, Meta: app.NewCountMeta(int64(len(buckets)))}, nil
		}
		// 归档同理：meta.count = 月份桶数（契约 §0.4「列表类接口都带 meta」）
		return app.WithMeta{Data: buckets, Meta: app.NewCountMeta(int64(len(buckets)))}, nil
	}
}

// linksList：GET /links/list → 分组（order 升序）+ meta.count=友链条目总数。
func linksList(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		countOnly, err := c.Bool("countOnly", false)
		if err != nil {
			return nil, err
		}
		groups, n, err := svc.Links.List(c.Context())
		if err != nil {
			return nil, err
		}
		if countOnly {
			return app.WithMeta{Data: nil, Meta: app.NewCountMeta(n)}, nil
		}
		return app.WithMeta{Data: groups, Meta: app.NewCountMeta(n)}, nil
	}
}

// linksThemeColor：GET /links/theme-color?url=。
// 取色结果稳定可缓存：Cache-Control 86400（契约值）。
func linksThemeColor(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		rawURL, err := c.RequiredQuery("url")
		if err != nil {
			return nil, err
		}
		color, err := svc.Links.ThemeColor(c.Context(), rawURL)
		if err != nil {
			return nil, err
		}
		return app.WithHeader{
			Data:   map[string]any{"color": color},
			Header: map[string]string{"Cache-Control": "public, max-age=86400"},
		}, nil
	}
}

// applicationsStatus：GET /links/applications/status?email=&name=（公开）。
// 公开的原因：申请人要能自助查询自己的申请进度；只回 Contact 匹配的记录，
// 泄露面 = 「知道邮箱或站名的人」——与旧后端 /flink/apply/status 等价。
// email 和 name 至少给一个，都没有就是「枚举所有申请」，必须拒绝。
func applicationsStatus(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		email := c.Query("email")
		name := c.Query("name")
		if email == "" && name == "" {
			return nil, apperr.New(apperr.ValidationInvalidField,
				"请提供申请时用的邮箱或站点名",
				apperr.FieldDetails{Fields: []apperr.FieldError{
					{Field: "email", Message: "email 与 name 至少提供一个"},
					{Field: "name", Message: "email 与 name 至少提供一个"},
				}})
		}
		list, err := svc.Links.ApplicationsByContact(c.Context(), email, name, 20)
		if err != nil {
			return nil, err
		}
		return list, nil
	}
}

// speaksList：GET /speaks/list。按 time 倒序。
func speaksList(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		page, ps, countOnly, err := parseListParams(c, true)
		if err != nil {
			return nil, err
		}
		res, err := svc.Speaks.List(c.Context(), store.ListFilter{
			Page: page, PageSize: ps.N, CountOnly: countOnly,
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

// siteInfo：GET /site/info → {"latestUpdateTime": N}。
func siteInfo(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		return svc.Sites.Info(c.Context())
	}
}
