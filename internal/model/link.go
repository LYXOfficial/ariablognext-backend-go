package model

// LinkGroup 是友链分组；Links 里的条目就是单条友链。
// order 缺失时按 0 处理（存量数据有缺），排序由数据库 sort 完成。
type LinkGroup struct {
	Name        string `bson:"name" json:"name"`
	Description string `bson:"description" json:"description"`
	Order       int    `bson:"order" json:"order"`
	Links       []Link `bson:"links" json:"links"`
}

type Link struct {
	ID          string  `bson:"id" json:"id"`
	Name        string  `bson:"name" json:"name"`
	URL         string  `bson:"url" json:"url"`
	Avatar      string  `bson:"avatar" json:"avatar"`
	Description string  `bson:"description" json:"description"`
	Color       string  `bson:"color" json:"color"`
	Latency     float64 `bson:"latency" json:"latency"`
}
