package model

// User 是后台账号。密码哈希（sha256 小写 hex）不放进模型——
// 查询时作为过滤条件用，取出来也没有任何用处，只会增加泄漏面。
type User struct {
	User string `bson:"user" json:"user"`
}
