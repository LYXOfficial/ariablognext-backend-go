// Package fake 是纯内存的 Store 实现，供 handler / service 单测使用。
// 必须实现全部接口；本阶段还没用到的领域先返回零值/空切片，
// 等对应阶段实现时再补真实逻辑（接口不变，测试也不用改）。
package fake

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/textutil"
)

// Store 持有全部领域数据的内存态。互斥锁保护所有可变字段，
// 测试并发调用时不炸 data race。
type Store struct {
	mu           sync.RWMutex
	posts        []model.Post
	drafts       []model.Draft
	summaries    map[string]summaryEntry
	speaks       []model.Speak
	groups       []model.LinkGroup
	applications []model.LinkApplication
	users        []fakeUser
	siteInfo     map[string]int64
	nextID       int
	closed       bool
}

// fakeUser 带 passwordHex：登录语义（user+hash 匹配）在 fake 里也要真实，
// 否则「错误密码也能登录」的 bug 在单测里发现不了。
type fakeUser struct {
	name        string
	passwordHex string
}

type summaryEntry struct {
	text      string
	refreshed time.Time
}

func New() *Store {
	return &Store{
		summaries: map[string]summaryEntry{},
		siteInfo:  map[string]int64{},
	}
}

// SetPosts 一次性铺测试数据（替换全部）。
func (s *Store) SetPosts(posts []model.Post) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.posts = posts
}

func (s *Store) SetDrafts(drafts []model.Draft) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drafts = drafts
}

func (s *Store) SetSpeaks(speaks []model.Speak) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.speaks = speaks
}

func (s *Store) SetGroups(groups []model.LinkGroup) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groups = groups
}

func (s *Store) SetUsers(users []model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users = nil // 兼容旧签名：清空凭据，用 SetUserCredentials 设置
}

// SetUserCredentials 设置一个「用户名 + sha256(password) 十六进制」凭据。
func (s *Store) SetUserCredentials(name, passwordHex string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users = append(s.users, fakeUser{name: name, passwordHex: passwordHex})
}

func (s *Store) SetSiteInfo(m map[string]int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.siteInfo = m
}

func (s *Store) Ping(ctx context.Context) error  { return nil }
func (s *Store) Close(ctx context.Context) error { return nil }

// ---------------- 文章 ----------------

type postRepo struct{ s *Store }

func (s *Store) Posts() store.PostRepository { return postRepo{s} }

// TaxonomyRepository 与 Posts 同集合（归档/分类/标签都从文章列表算）。
func (s *Store) Taxonomy() store.TaxonomyRepository { return postRepo{s} }

func clonePost(p model.Post) model.Post {
	cp := p
	if p.Tags != nil {
		// 注意用 []string{} 起步：append([]string(nil), 空切片...) 会变回 nil，
		// 空 tags 又会被序列化成 null（前端 .map 直接炸）。
		cp.Tags = append([]string{}, p.Tags...)
	}
	return cp
}

func (r postRepo) Page(ctx context.Context, f store.PostFilter, loc *time.Location) (store.Page[model.Post], error) {
	s := r.s
	s.mu.RLock()
	defer s.mu.RUnlock()

	// count 与列表共用同一套过滤（service 层保证只传一套参数）
	var matched []model.Post
	for _, p := range s.posts {
		if f.Category != "" && p.Category != f.Category {
			continue
		}
		if f.Tag != "" && !containsString(p.Tags, f.Tag) {
			continue
		}
		if f.Year > 0 && !inYearMonth(p.PublishTime, loc, f.Year, f.Month) {
			continue
		}
		if kws := splitKeywords(f.Keyword); len(kws) > 0 {
			// 与 mongo 实现同一语义：空格分隔多关键词、AND、按「剥离 Markdown 后的全文」匹配。
			// 不能用 plainContent（库里截断到 201 字，正文中段会搜不到）；
			// 也不能直接匹配 mdContent（会命中 <Folding> 这类结构标记）。
			title := strings.ToLower(p.Title)
			text := textutil.BuildSearchText(p.Title, p.MdContent)
			matched := true
			for _, kw := range kws {
				if f.TitleOnly {
					if !strings.Contains(title, kw) {
						matched = false
						break
					}
				} else if !strings.Contains(title, kw) && !strings.Contains(text, kw) {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
		}
		matched = append(matched, p)
	}

	count := int64(len(matched))
	if f.CountOnly {
		// CountOnly 必须不产出列表主体（Items 为 nil），调用方靠它省流量
		return store.Page[model.Post]{Count: count}, nil
	}

	sort.SliceStable(matched, func(i, j int) bool {
		if f.OrderAsc {
			return matched[i].PublishTime < matched[j].PublishTime
		}
		return matched[i].PublishTime > matched[j].PublishTime
	})

	items := []model.Post{}
	start, end := 0, len(matched)
	if !f.Full {
		start = (f.Page - 1) * f.PageSize
		if start > len(matched) {
			start = len(matched)
		}
		end = start + f.PageSize
		if end > len(matched) {
			end = len(matched)
		}
	}
	for _, p := range matched[start:end] {
		if !f.WithContent {
			p.MdContent = ""
		}
		p.Normalize()
		items = append(items, clonePost(p))
	}
	return store.Page[model.Post]{
		Items:   items,
		Count:   count,
		HasMore: !f.Full && int64(f.Page*f.PageSize) < count,
	}, nil
}

func (r postRepo) BySlug(ctx context.Context, slug string) (*model.Post, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, p := range r.s.posts {
		if p.Slug == slug {
			cp := clonePost(p)
			cp.Normalize()
			return &cp, nil
		}
	}
	return nil, store.ErrNotFound
}

func (r postRepo) Slugs(ctx context.Context) ([]string, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	out := make([]string, 0, len(r.s.posts))
	for _, p := range r.s.posts {
		out = append(out, p.Slug)
	}
	return out, nil
}

func (r postRepo) Neighbours(ctx context.Context, slug string) (*model.PostBrief, *model.PostBrief, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var self *model.Post
	for i := range r.s.posts {
		if r.s.posts[i].Slug == slug {
			self = &r.s.posts[i]
			break
		}
	}
	if self == nil {
		return nil, nil, store.ErrNotFound
	}
	var newer, older *model.PostBrief
	for i := range r.s.posts {
		p := &r.s.posts[i]
		if p.Slug == slug {
			continue
		}
		brief := model.PostBrief{Slug: p.Slug, Title: p.Title, BannerImg: p.BannerImg, PublishTime: p.PublishTime}
		if p.PublishTime > self.PublishTime && (newer == nil || p.PublishTime < newer.PublishTime) {
			b := brief
			newer = &b
		}
		if p.PublishTime < self.PublishTime && (older == nil || p.PublishTime > older.PublishTime) {
			b := brief
			older = &b
		}
	}
	return newer, older, nil
}

func (r postRepo) Related(ctx context.Context, slug string, limit int) ([]model.PostBrief, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	out := []model.PostBrief{}
	var self *model.Post
	for i := range r.s.posts {
		if r.s.posts[i].Slug == slug {
			self = &r.s.posts[i]
		}
	}
	if self == nil || len(self.Tags) == 0 {
		return out, nil
	}
	for i := range r.s.posts {
		if len(out) >= limit {
			break
		}
		p := &r.s.posts[i]
		if p.Slug == slug {
			continue
		}
		for _, tag := range p.Tags {
			if containsString(self.Tags, tag) {
				out = append(out, model.PostBrief{Slug: p.Slug, Title: p.Title, BannerImg: p.BannerImg, PublishTime: p.PublishTime})
				break
			}
		}
	}
	return out, nil
}

func (r postRepo) TotalWordCount(ctx context.Context) (int64, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var total int64
	for _, p := range r.s.posts {
		if p.WordCount != nil {
			total += int64(*p.WordCount)
		}
	}
	return total, nil
}

func (r postRepo) Create(ctx context.Context, p model.Post) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, exist := range r.s.posts {
		if exist.Slug == p.Slug {
			return store.ErrDuplicate
		}
	}
	r.s.posts = append(r.s.posts, clonePost(p))
	return nil
}

func (r postRepo) UpdateMeta(ctx context.Context, slug string, m model.PostMeta) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i := range r.s.posts {
		if r.s.posts[i].Slug != slug {
			continue
		}
		p := &r.s.posts[i]
		if m.Title != nil {
			p.Title = *m.Title
		}
		if m.Category != nil {
			p.Category = *m.Category
		}
		if m.Tags != nil {
			p.Tags = append([]string(nil), m.Tags...)
		}
		if m.BannerImg != nil {
			p.BannerImg = *m.BannerImg
		}
		if m.Description != nil {
			p.Description = m.Description
		}
		if m.CoverFit != nil {
			p.CoverFit = m.CoverFit
		}
		if m.PublishTime != nil {
			p.PublishTime = *m.PublishTime
		}
		if m.LastUpdatedTime != nil {
			p.LastUpdatedTime = *m.LastUpdatedTime
		}
		return nil
	}
	return store.ErrNotFound
}

func (r postRepo) UpdateContent(ctx context.Context, slug string, c model.PostContent) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i := range r.s.posts {
		if r.s.posts[i].Slug != slug {
			continue
		}
		// 与 mongo 实现同构：正文明细 + 派生字段一起写，fake 少写一个就会漏测
		p := &r.s.posts[i]
		p.MdContent = c.Markdown
		p.PlainContent = c.PlainContent
		p.SearchText = c.SearchText
		wc := c.WordCount
		p.WordCount = &wc
		return nil
	}
	return store.ErrNotFound
}

func (r postRepo) Delete(ctx context.Context, slug string) (bool, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i := range r.s.posts {
		if r.s.posts[i].Slug == slug {
			r.s.posts = append(r.s.posts[:i], r.s.posts[i+1:]...)
			return true, nil
		}
	}
	// 幂等：不存在也算成功，返回 false 让上层决定文案
	return false, nil
}

// SitemapEntries 实现 store.SitemapSource（与 mongo 同语义）。
func (r postRepo) SitemapEntries(ctx context.Context) ([]store.SitemapEntry, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	out := make([]store.SitemapEntry, 0, len(r.s.posts))
	for _, p := range r.s.posts {
		ts := p.LastUpdatedTime
		if ts == 0 {
			ts = p.PublishTime
		}
		out = append(out, store.SitemapEntry{Slug: p.Slug, LastUpdatedTime: ts})
	}
	return out, nil
}

// ---------------- 分类 / 标签 / 归档 ----------------

func (r postRepo) Categories(ctx context.Context) ([]model.CategoryCount, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	counts := map[string]int{}
	for _, p := range r.s.posts {
		counts[p.Category]++
	}
	out := make([]model.CategoryCount, 0, len(counts))
	for name, c := range counts {
		out = append(out, model.CategoryCount{Name: name, Count: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (r postRepo) Tags(ctx context.Context) ([]model.TagCount, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	counts := map[string]int{}
	for _, p := range r.s.posts {
		for _, tag := range p.Tags {
			counts[tag]++
		}
	}
	out := make([]model.TagCount, 0, len(counts))
	for name, c := range counts {
		out = append(out, model.TagCount{Name: name, Count: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (r postRepo) ArchiveBuckets(ctx context.Context, loc *time.Location) ([]model.ArchiveBucket, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	counts := map[[2]int]int{}
	for _, p := range r.s.posts {
		t := time.Unix(p.PublishTime, 0).In(loc)
		counts[[2]int{t.Year(), int(t.Month())}]++
	}
	out := make([]model.ArchiveBucket, 0, len(counts))
	for k, c := range counts {
		out = append(out, model.ArchiveBucket{Year: k[0], Month: k[1], Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Year != out[j].Year {
			return out[i].Year > out[j].Year
		}
		return out[i].Month > out[j].Month
	})
	return out, nil
}

// ---------------- 草稿 ----------------

type draftRepo struct{ s *Store }

func (s *Store) Drafts() store.DraftRepository { return draftRepo{s} }

func (r draftRepo) Page(ctx context.Context, f store.DraftFilter) (store.Page[model.Draft], error) {
	s := r.s
	s.mu.RLock()
	defer s.mu.RUnlock()
	var matched []model.Draft
	for _, d := range s.drafts {
		if f.Keyword != "" && !strings.Contains(strings.ToLower(d.Title), strings.ToLower(f.Keyword)) {
			continue
		}
		matched = append(matched, d)
	}
	count := int64(len(matched))
	if f.CountOnly {
		return store.Page[model.Draft]{Count: count}, nil
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if f.OrderAsc {
			return matched[i].PublishTime < matched[j].PublishTime
		}
		return matched[i].PublishTime > matched[j].PublishTime
	})
	items := []model.Draft{}
	start, end := 0, len(matched)
	if !f.Full {
		start = (f.Page - 1) * f.PageSize
		if start > len(matched) {
			start = len(matched)
		}
		end = start + f.PageSize
		if end > len(matched) {
			end = len(matched)
		}
	}
	for _, d := range matched[start:end] {
		if !f.WithContent {
			d.MdContent = nil
		}
		d.Normalize()
		items = append(items, d)
	}
	return store.Page[model.Draft]{Items: items, Count: count, HasMore: !f.Full && int64(f.Page*f.PageSize) < count}, nil
}

func (r draftRepo) BySlug(ctx context.Context, slug string) (*model.Draft, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for i := range r.s.drafts {
		if r.s.drafts[i].Slug == slug {
			d := r.s.drafts[i]
			d.Normalize()
			return &d, nil
		}
	}
	return nil, store.ErrNotFound
}

func (r draftRepo) Slugs(ctx context.Context) ([]string, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	out := make([]string, 0, len(r.s.drafts))
	for _, d := range r.s.drafts {
		out = append(out, d.Slug)
	}
	return out, nil
}

func (r draftRepo) Count(ctx context.Context) (int64, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	return int64(len(r.s.drafts)), nil
}

func (r draftRepo) Create(ctx context.Context, d model.Draft) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, exist := range r.s.drafts {
		if exist.Slug == d.Slug {
			return store.ErrDuplicate
		}
	}
	r.s.drafts = append(r.s.drafts, d)
	return nil
}

func (r draftRepo) UpdateMeta(ctx context.Context, slug string, m model.PostMeta) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i := range r.s.drafts {
		if r.s.drafts[i].Slug != slug {
			continue
		}
		d := &r.s.drafts[i]
		if m.Title != nil {
			d.Title = *m.Title
		}
		if m.Category != nil {
			d.Category = *m.Category
		}
		if m.Tags != nil {
			d.Tags = append([]string(nil), m.Tags...)
		}
		if m.BannerImg != nil {
			d.BannerImg = *m.BannerImg
		}
		if m.Description != nil {
			d.Description = m.Description
		}
		if m.CoverFit != nil {
			d.CoverFit = m.CoverFit
		}
		if m.PublishTime != nil {
			d.PublishTime = *m.PublishTime
		}
		if m.LastUpdatedTime != nil {
			d.LastUpdatedTime = *m.LastUpdatedTime
		}
		return nil
	}
	return store.ErrNotFound
}

func (r draftRepo) UpdateContent(ctx context.Context, slug string, c model.PostContent) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i := range r.s.drafts {
		if r.s.drafts[i].Slug != slug {
			continue
		}
		md, plain := c.Markdown, c.PlainContent
		wc := c.WordCount
		d := &r.s.drafts[i]
		d.MdContent = &md
		d.PlainContent = &plain
		d.WordCount = &wc
		return nil
	}
	return store.ErrNotFound
}

func (r draftRepo) Delete(ctx context.Context, slug string) (bool, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i := range r.s.drafts {
		if r.s.drafts[i].Slug == slug {
			r.s.drafts = append(r.s.drafts[:i], r.s.drafts[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

// ---------------- 摘要缓存 ----------------

type summaryRepo struct{ s *Store }

func (s *Store) Summaries() store.SummaryRepository { return summaryRepo{s} }

func (r summaryRepo) Get(ctx context.Context, slug string) (string, time.Time, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	e, ok := r.s.summaries[slug]
	if !ok {
		return "", time.Time{}, store.ErrNotFound
	}
	return e.text, e.refreshed, nil
}

func (r summaryRepo) Upsert(ctx context.Context, slug, summary string, at time.Time) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.summaries[slug] = summaryEntry{text: summary, refreshed: at}
	return nil
}

// ---------------- 说说 ----------------

type speakRepo struct{ s *Store }

func (s *Store) Speaks() store.SpeakRepository { return speakRepo{s} }

func (r speakRepo) Page(ctx context.Context, f store.ListFilter) (store.Page[model.Speak], error) {
	s := r.s
	s.mu.RLock()
	defer s.mu.RUnlock()
	matched := append([]model.Speak{}, s.speaks...)
	// 按 time 倒序（最新在前）
	sort.SliceStable(matched, func(i, j int) bool { return matched[i].Time > matched[j].Time })
	count := int64(len(matched))
	if f.CountOnly {
		return store.Page[model.Speak]{Count: count}, nil
	}
	items := []model.Speak{}
	start, end := 0, len(matched)
	if f.PageSize > 0 {
		start = (f.Page - 1) * f.PageSize
		if start > len(matched) {
			start = len(matched)
		}
		end = start + f.PageSize
		if end > len(matched) {
			end = len(matched)
		}
	}
	items = append(items, matched[start:end]...)
	return store.Page[model.Speak]{Items: items, Count: count, HasMore: f.PageSize > 0 && int64(f.Page*f.PageSize) < count}, nil
}

func (r speakRepo) Count(ctx context.Context) (int64, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	return int64(len(r.s.speaks)), nil
}

func (r speakRepo) Create(ctx context.Context, sp model.Speak) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, exist := range r.s.speaks {
		if exist.Time == sp.Time {
			return store.ErrDuplicate
		}
	}
	r.s.speaks = append(r.s.speaks, sp)
	return nil
}

func (r speakRepo) Update(ctx context.Context, time int64, content, plainContent string) (bool, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i := range r.s.speaks {
		if r.s.speaks[i].Time == time {
			r.s.speaks[i].Content = content
			r.s.speaks[i].PlainContent = plainContent
			return true, nil
		}
	}
	return false, nil
}

func (r speakRepo) Delete(ctx context.Context, time int64) (bool, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i := range r.s.speaks {
		if r.s.speaks[i].Time == time {
			r.s.speaks = append(r.s.speaks[:i], r.s.speaks[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

// ---------------- 友链 ----------------

type linkRepo struct{ s *Store }

func (s *Store) Links() store.LinkRepository { return linkRepo{s} }

func (r linkRepo) Groups(ctx context.Context) ([]model.LinkGroup, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	out := make([]model.LinkGroup, 0, len(r.s.groups))
	for _, g := range r.s.groups {
		out = append(out, cloneGroup(g))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out, nil
}

func (r linkRepo) GroupByName(ctx context.Context, name string) (*model.LinkGroup, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for i := range r.s.groups {
		if r.s.groups[i].Name == name {
			g := cloneGroup(r.s.groups[i])
			return &g, nil
		}
	}
	return nil, store.ErrNotFound
}

func (r linkRepo) LinkCount(ctx context.Context) (int64, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var total int64
	for _, g := range r.s.groups {
		total += int64(len(g.Links))
	}
	return total, nil
}

func (r linkRepo) FindByURL(ctx context.Context, url string) (*store.LinkRef, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	want := strings.TrimRight(url, "/")
	for _, g := range r.s.groups {
		for _, l := range g.Links {
			// 结尾斜杠归一化后再比，兼容「https://x.com」和「https://x.com/」两种收录写法
			if strings.TrimRight(l.URL, "/") == want {
				return &store.LinkRef{Group: g.Name, Link: l}, nil
			}
		}
	}
	return nil, store.ErrNotFound
}

func (r linkRepo) AddLink(ctx context.Context, group string, l model.Link) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i := range r.s.groups {
		if r.s.groups[i].Name != group {
			continue
		}
		for _, exist := range r.s.groups[i].Links {
			if exist.ID == l.ID || strings.TrimRight(exist.URL, "/") == strings.TrimRight(l.URL, "/") {
				return store.ErrDuplicate
			}
		}
		r.s.groups[i].Links = append(r.s.groups[i].Links, l)
		return nil
	}
	return store.ErrNotFound
}

func (r linkRepo) UpdateLink(ctx context.Context, group, id string, patch store.LinkPatch) (bool, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for gi := range r.s.groups {
		if r.s.groups[gi].Name != group {
			continue
		}
		for li := range r.s.groups[gi].Links {
			if r.s.groups[gi].Links[li].ID != id {
				continue
			}
			l := &r.s.groups[gi].Links[li]
			if patch.Name != "" {
				l.Name = patch.Name
			}
			if patch.URL != "" {
				l.URL = patch.URL
			}
			if patch.Avatar != "" {
				l.Avatar = patch.Avatar
			}
			if patch.Description != "" {
				l.Description = patch.Description
			}
			if patch.Color != "" {
				l.Color = patch.Color
			}
			return true, nil
		}
	}
	return false, nil
}

func (r linkRepo) MoveLink(ctx context.Context, fromGroup, toGroup, id string) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var moved *model.Link
	for gi := range r.s.groups {
		if r.s.groups[gi].Name != fromGroup {
			continue
		}
		for li := range r.s.groups[gi].Links {
			if r.s.groups[gi].Links[li].ID == id {
				l := r.s.groups[gi].Links[li]
				moved = &l
				r.s.groups[gi].Links = append(r.s.groups[gi].Links[:li], r.s.groups[gi].Links[li+1:]...)
				break
			}
		}
		break
	}
	if moved == nil {
		return store.ErrNotFound
	}
	// push 后 pull：如果目标组不存在，把链接塞回去，保证失败不丢数据
	for gi := range r.s.groups {
		if r.s.groups[gi].Name == toGroup {
			r.s.groups[gi].Links = append(r.s.groups[gi].Links, *moved)
			return nil
		}
	}
	for gi := range r.s.groups {
		if r.s.groups[gi].Name == fromGroup {
			r.s.groups[gi].Links = append(r.s.groups[gi].Links, *moved)
			break
		}
	}
	return store.ErrNotFound
}

func (r linkRepo) DeleteLink(ctx context.Context, group, id string) (bool, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for gi := range r.s.groups {
		if r.s.groups[gi].Name != group {
			continue
		}
		for li := range r.s.groups[gi].Links {
			if r.s.groups[gi].Links[li].ID == id {
				r.s.groups[gi].Links = append(r.s.groups[gi].Links[:li], r.s.groups[gi].Links[li+1:]...)
				return true, nil
			}
		}
	}
	return false, nil
}

func (r linkRepo) AddGroup(ctx context.Context, g model.LinkGroup) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, exist := range r.s.groups {
		if exist.Name == g.Name {
			return store.ErrDuplicate
		}
	}
	g.Links = []model.Link{}
	r.s.groups = append(r.s.groups, g)
	return nil
}

func (r linkRepo) UpdateGroup(ctx context.Context, oldName string, g model.LinkGroup) (bool, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, exist := range r.s.groups {
		if exist.Name == g.Name && oldName != g.Name {
			return false, store.ErrDuplicate
		}
	}
	for i := range r.s.groups {
		if r.s.groups[i].Name == oldName {
			links := r.s.groups[i].Links
			r.s.groups[i].Name = g.Name
			r.s.groups[i].Description = g.Description
			r.s.groups[i].Order = g.Order
			r.s.groups[i].Links = links
			return true, nil
		}
	}
	return false, nil
}

func (r linkRepo) DeleteGroup(ctx context.Context, name string) (bool, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i := range r.s.groups {
		if r.s.groups[i].Name == name {
			r.s.groups = append(r.s.groups[:i], r.s.groups[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func (r linkRepo) SetLatency(ctx context.Context, updates []store.LatencyUpdate) (int64, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var n int64
	for _, u := range updates {
		for gi := range r.s.groups {
			for li := range r.s.groups[gi].Links {
				if r.s.groups[gi].Links[li].ID == u.ID {
					r.s.groups[gi].Links[li].Latency = u.Latency
					n++
				}
			}
		}
	}
	return n, nil
}

// ---------------- 友链申请 ----------------

type applicationRepo struct{ s *Store }

func (s *Store) Applications() store.ApplicationRepository { return applicationRepo{s} }

func (r applicationRepo) Create(ctx context.Context, a model.LinkApplication) (string, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.nextID++
	id := "fake-" + itoa(r.s.nextID)
	a.ID = id
	r.s.applications = append(r.s.applications, a)
	return id, nil
}

func (r applicationRepo) ByID(ctx context.Context, id string) (*model.LinkApplication, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for i := range r.s.applications {
		if r.s.applications[i].ID == id {
			a := r.s.applications[i]
			return &a, nil
		}
	}
	return nil, store.ErrNotFound
}

func (r applicationRepo) CountPending(ctx context.Context) (int64, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var n int64
	for _, a := range r.s.applications {
		if a.Status == model.ApplicationStatusPending {
			n++
		}
	}
	return n, nil
}

func (r applicationRepo) ListPending(ctx context.Context, limit int) ([]model.LinkApplication, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	out := []model.LinkApplication{}
	for _, a := range r.s.applications {
		if a.Status == model.ApplicationStatusPending {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r applicationRepo) FindIdentical(ctx context.Context, ident store.ApplicationIdentity) (*model.LinkApplication, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for i := range r.s.applications {
		a := &r.s.applications[i]
		if a.Status != model.ApplicationStatusPending {
			continue
		}
		targetID := ""
		if a.TargetID != nil {
			targetID = *a.TargetID
		}
		if a.Type == ident.Type && a.Name == ident.Name && a.Link == ident.Link &&
			a.Avatar == ident.Avatar && a.Descr == ident.Descr && a.Color == ident.Color &&
			strings.ToLower(a.Email) == strings.ToLower(ident.Email) && targetID == ident.TargetID {
			cp := *a
			return &cp, nil
		}
	}
	return nil, nil
}

func (r applicationRepo) FindByContact(ctx context.Context, email, name string, limit int) ([]model.LinkApplication, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	out := []model.LinkApplication{}
	for _, a := range r.s.applications {
		if email != "" && strings.EqualFold(a.Email, email) {
			out = append(out, a)
			continue
		}
		if name != "" && strings.EqualFold(a.Name, name) {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r applicationRepo) SetScreening(ctx context.Context, id string, sc model.AIScreening) (bool, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i := range r.s.applications {
		if r.s.applications[i].ID == id && r.s.applications[i].Status == model.ApplicationStatusPending {
			v := sc
			r.s.applications[i].AIScreening = &v
			return true, nil
		}
	}
	return false, nil
}

func (r applicationRepo) Decide(ctx context.Context, id, status, reason string, at time.Time) (bool, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i := range r.s.applications {
		if r.s.applications[i].ID == id && r.s.applications[i].Status == model.ApplicationStatusPending {
			r.s.applications[i].Status = status
			r.s.applications[i].Reason = reason
			atCopy := at
			r.s.applications[i].DecidedAt = &atCopy
			return true, nil
		}
	}
	return false, nil
}

func (r applicationRepo) DeleteDecidedByEmail(ctx context.Context, email string) (int64, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var n int64
	kept := r.s.applications[:0]
	for _, a := range r.s.applications {
		if strings.EqualFold(a.Email, email) && a.Status != model.ApplicationStatusPending {
			n++
			continue
		}
		kept = append(kept, a)
	}
	r.s.applications = kept
	return n, nil
}

func (r applicationRepo) PurgeDecidedBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var n int64
	kept := r.s.applications[:0]
	for _, a := range r.s.applications {
		// 只清「已出结果且超期」的；pending 不动
		if a.Status != model.ApplicationStatusPending && a.CreatedAt.Before(cutoff) {
			n++
			continue
		}
		kept = append(kept, a)
	}
	r.s.applications = kept
	return n, nil
}

// ---------------- 用户 / 站点信息 ----------------

type userRepo struct{ s *Store }

func (s *Store) Users() store.UserRepository { return userRepo{s} }

func (r userRepo) ByCredentials(ctx context.Context, user, passwordHex string) (*model.User, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for i := range r.s.users {
		// user + hash 双条件匹配（与 mongo 实现一致），密码错误就是找不到
		if r.s.users[i].name == user && r.s.users[i].passwordHex == passwordHex {
			return &model.User{User: r.s.users[i].name}, nil
		}
	}
	return nil, store.ErrNotFound
}

type siteRepo struct{ s *Store }

func (s *Store) Sites() store.SiteRepository { return siteRepo{s} }

func (r siteRepo) GetInt(ctx context.Context, key string) (int64, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	v, ok := r.s.siteInfo[key]
	if !ok {
		return 0, store.ErrNotFound
	}
	return v, nil
}

func (r siteRepo) SetNow(ctx context.Context, key string) (int64, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	now := time.Now().Unix()
	r.s.siteInfo[key] = now
	return now, nil
}

// UpsertUser 实现 store.UserWriter（种子工具用）。
func (s *Store) UpsertUser(ctx context.Context, user, passwordHex string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.users {
		if s.users[i].name == user {
			s.users[i].passwordHex = passwordHex
			return nil
		}
	}
	s.users = append(s.users, fakeUser{name: user, passwordHex: passwordHex})
	return nil
}

func (r siteRepo) SetInt(ctx context.Context, key string, value int64) (int64, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.siteInfo[key] = value
	return value, nil
}

// ---------------- 小工具 ----------------

// splitKeywords 与 mongo 侧 SplitKeywords 同一语义（空白分隔、去空项）。
// fake 不 import mongo 包，这里复制一份小逻辑并靠测试对齐。
func splitKeywords(raw string) []string {
	fields := strings.Fields(raw)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func inYearMonth(unix int64, loc *time.Location, year, month int) bool {
	t := time.Unix(unix, 0).In(loc)
	if t.Year() != year {
		return false
	}
	if month > 0 && int(t.Month()) != month {
		return false
	}
	return true
}

func cloneGroup(g model.LinkGroup) model.LinkGroup {
	cp := g
	cp.Links = append([]model.Link{}, g.Links...)
	return cp
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
