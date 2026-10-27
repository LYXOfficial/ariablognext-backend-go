package mongo

import (
	"regexp"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// regexpEscape 把用户输入转成字面正则。所有 $regex 查询都必须经过它——
// 未转义的元字符会把「搜索关键字」变成「任意正则注入面」。
func regexpEscape(s string) string {
	return regexp.QuoteMeta(s)
}

// primitiveFromHex 把 ObjectID 字符串转回驱动类型；非法字符串按「不存在」处理。
func primitiveFromHex(id string) (bson.ObjectID, error) {
	return bson.ObjectIDFromHex(id)
}

// postMetaSet 把 PostMeta 的非空字段转成 $set 文档（posts / drafts 共用）。
func postMetaSet(m model.PostMeta) bson.M {
	set := bson.M{}
	if m.Title != nil {
		set["title"] = *m.Title
	}
	if m.Category != nil {
		set["category"] = *m.Category
	}
	if m.Tags != nil {
		set["tags"] = orEmptySlice(m.Tags)
	}
	if m.BannerImg != nil {
		set["bannerImg"] = *m.BannerImg
	}
	if m.Description != nil {
		set["description"] = nullableString(m.Description)
	}
	if m.CoverFit != nil {
		set["coverFit"] = nullableString(m.CoverFit)
	}
	if m.PublishTime != nil {
		set["publishTime"] = *m.PublishTime
	}
	if m.LastUpdatedTime != nil {
		set["lastUpdatedTime"] = *m.LastUpdatedTime
	}
	return set
}
