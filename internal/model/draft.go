package model

// Draft 与 Post 结构对称，但 mdContent / plainContent / wordCount 可空——
// 后台新建草稿可以只写标题不写正文（存量数据 4/8 条就是缺这几个字段）。
// 不要用空串/0 伪造「有正文」，前端依赖 null 区分。
type Draft struct {
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
	PlainContent    *string  `bson:"plainContent" json:"plainContent"`
	MdContent       *string  `bson:"mdContent" json:"mdContent"`
}

// Normalize 同 Post.Normalize：drafts 的 tags 也不能是 null。
func (d *Draft) Normalize() {
	if d.Tags == nil {
		d.Tags = []string{}
	}
}
