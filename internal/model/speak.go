package model

// Speak 是说说。time 即 unix 秒，同时是业务主键（旧接口按 time 增删改查）。
type Speak struct {
	Time         int64  `bson:"time" json:"time"`
	Content      string `bson:"content" json:"content"`
	PlainContent string `bson:"plainContent" json:"plainContent"`
}
