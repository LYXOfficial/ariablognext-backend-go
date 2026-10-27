package handler

import (
	"bytes"
	"encoding/xml"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/app"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

// sitemapURL 是 <url> 一项。字段顺序按旧后端（loc/lastmod/changefreq/priority）。
type sitemapURL struct {
	Loc        string `xml:"loc"`
	Lastmod    string `xml:"lastmod"`
	Changefreq string `xml:"changefreq"`
	Priority   string `xml:"priority"`
}

type urlset struct {
	XMLName xml.Name     `xml:"urlset"`
	Xmlns   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

// sitemap：GET /sitemap 返回 XML（全服务唯一的非 JSON 响应，给爬虫的文档不是 API）。
// lastmod 固定上海时区（design-notes §5），不依赖容器 TZ。
func sitemap(st store.Store, cfg *config.Config) app.Handler {
	return func(c *app.Ctx) (any, error) {
		slugs, err := st.Posts().Slugs(c.Context())
		if err != nil {
			return nil, err
		}
		loc := cfg.Location()
		now := time.Now().In(loc).Format("2006-01-02")
		set := urlset{Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9"}

		// 文章页 lastmod 用每篇的 lastUpdatedTime（与旧后端一致，爬虫据此判断是否重抓）。
		// 存储实现不支持 SitemapSource 时退回页面级 lastmod（今天）。
		entries := make([]store.SitemapEntry, 0, len(slugs))
		if src, ok := st.Posts().(store.SitemapSource); ok {
			got, err := src.SitemapEntries(c.Context())
			if err != nil {
				return nil, err
			}
			entries = got
		}
		if len(entries) == 0 {
			for _, slug := range slugs {
				entries = append(entries, store.SitemapEntry{Slug: slug})
			}
		}
		for _, e := range entries {
			lastmod := now
			if e.LastUpdatedTime > 0 {
				// 库里的时间戳是 unix 秒，按配置时区格式化（上海，不依赖容器 TZ）
				lastmod = time.Unix(e.LastUpdatedTime, 0).In(loc).Format("2006-01-02")
			}
			set.URLs = append(set.URLs, sitemapURL{
				Loc:        cfg.Site.URL + "/posts/" + e.Slug,
				Lastmod:    lastmod,
				Changefreq: "daily",
				Priority:   "0.8",
			})
		}
		for _, p := range []struct {
			path     string
			priority string
		}{
			{"", "1.0"}, {"/speaks", "0.6"}, {"/links", "0.8"}, {"/about", "0.8"},
		} {
			set.URLs = append(set.URLs, sitemapURL{
				Loc: cfg.Site.URL + p.path, Lastmod: now, Changefreq: "daily", Priority: p.priority,
			})
		}
		buf := &bytes.Buffer{}
		buf.WriteString(xml.Header)
		enc := xml.NewEncoder(buf)
		if err := enc.Encode(set); err != nil {
			return nil, apperr.Wrap(apperr.InternalError, "生成 sitemap 失败", err)
		}
		return app.Raw{ContentType: "application/xml; charset=utf-8", Body: buf.Bytes()}, nil
	}
}
