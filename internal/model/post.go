// Package model 定义领域模型。
// 字段名与数据库字段名保持一致（bson/json tag 都用原名驼峰），
// 这样胶水层不需要字段映射表，排查数据问题时也少一层翻译。
// 时间一律 unix 秒 int64；可空字段用指针，不用零值伪造（库里 Drafts 有 4/8 条缺正文）。
package model

// Post 是文章的完整形态。mdContent 仅 detail / withContent=true 时才该被填充，
// 列表查询必须显式投影排除它（平均 8.5KB、最长 140KB，拉进列表就是流量灾难）。
type Post struct {
	Slug            string   `bson:"slug" json:"slug"`
	Title           string   `bson:"title" json:"title"`
	Category        string   `bson:"category" json:"category"`
	Tags            []string `bson:"tags" json:"tags"`
	BannerImg       string   `bson:"bannerImg" json:"bannerImg"`
	Description     *string  `bson:"description" json:"description"`
	CoverFit        *string  `bson:"coverFit" json:"coverFit"`
	PublishTime     int64    `bson:"publishTime" json:"publishTime"`
	LastUpdatedTime int64    `bson:"lastUpdatedTime" json:"lastUpdatedTime"`
	WordCount       *int     `bson:"wordCount" json:"wordCount"`
	PlainContent    string   `bson:"plainContent" json:"plainContent"`
	// Context 仅搜索命中时由仓储填充（从 searchText 定位取片段），
	// 非搜索查询必须为 nil；bson:"-" 因为它不落库
	Context *string `bson:"-" json:"context,omitempty"`
	// mdContent 带 omitempty：列表走投影时键必须整段消失（mongo 投影行为），
	// fake 实现靠置空串 + omitempty 达成同一形状；detail 场景正文非空键照常出现
	MdContent string `bson:"mdContent" json:"mdContent,omitempty"`
	// MatchTier 是本次检索的匹配层级：2 = 精确命中，1 = 模糊/别名命。0 = 未标记。
	// 只在搜索路径由存储层填充，不落库、不对外输出（service 用它分层排序）。
	MatchTier int `bson:"-" json:"-"`
	// SearchText 是搜索索引字段：写入时由服务端维护，对外 JSON 永不输出（json:"-"）。
	// 放在模型上是因为 Create 的写入载体就是 Post；查询走投影不进模型。
	SearchText string `bson:"searchText,omitempty" json:"-"`
}

// Normalize 把可能为 nil 的切片换成空切片：
// JSON 里 `"tags":null` 会让前端 `.map` 直接炸（AGENTS.md 明令返回 []T{}）。
// 由各存储适配器在解码后调用，保证对外形态稳定。
func (p *Post) Normalize() {
	if p.Tags == nil {
		p.Tags = []string{}
	}
}

// PostBrief 是列表/导航/相关文章场景的瘦身版，不含正文。
type PostBrief struct {
	Slug        string  `bson:"slug" json:"slug"`
	Title       string  `bson:"title" json:"title"`
	BannerImg   string  `bson:"bannerImg" json:"bannerImg"`
	PublishTime int64   `bson:"publishTime" json:"publishTime"`
	Context     *string `bson:"-" json:"context,omitempty"`
}

// Brief 抽出瘦身版。Context 原样带过去（搜索命中时由仓储填充）。
func (p *Post) Brief() PostBrief {
	return PostBrief{
		Slug: p.Slug, Title: p.Title, BannerImg: p.BannerImg,
		PublishTime: p.PublishTime, Context: p.Context,
	}
}

// PostMeta 是文章元信息里可被后台单独修改的部分。
// 指针字段 = 「没传就不动」，让 PUT /posts/update 做局部更新。
type PostMeta struct {
	Title           *string  `bson:"title,omitempty" json:"title,omitempty"`
	Category        *string  `bson:"category,omitempty" json:"category,omitempty"`
	Tags            []string `bson:"tags,omitempty" json:"tags,omitempty"`
	BannerImg       *string  `bson:"bannerImg,omitempty" json:"bannerImg,omitempty"`
	Description     *string  `bson:"description,omitempty" json:"description,omitempty"`
	CoverFit        *string  `bson:"coverFit,omitempty" json:"coverFit,omitempty"`
	PublishTime     *int64   `bson:"publishTime,omitempty" json:"publishTime,omitempty"`
	LastUpdatedTime *int64   `bson:"lastUpdatedTime,omitempty" json:"lastUpdatedTime,omitempty"`
}

// PostContent 是「只改正文」的载荷。服务端由 markdown 重算
// wordCount / plainContent / searchText 并一起落库，调用方只传 markdown。
type PostContent struct {
	Markdown     string `json:"markdown"`
	PlainContent string `json:"-"`
	SearchText   string `json:"-"`
	WordCount    int    `json:"-"`
}

// CategoryCount / TagCount 的 count 是该分类/标签下的文章数。
type CategoryCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type TagCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// ArchiveBucket 是归档分桶。年月语义由调用方传入的时区决定，
// 分组在 Go 侧完成，绝不在 MongoDB 里用 $toDate（那是 UTC）。
type ArchiveBucket struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Count int `json:"count"`
}
