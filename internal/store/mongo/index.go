package mongo

import (
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// 索引构造的两个小助手，避免 ensureIndexes 里出现成片的样板代码。
// bson 类型只在本包内使用，不许泄漏到 store 接口层。

// uniqueIndex：唯一索引。slug 去重、并发写安全全靠它 + IsDuplicateKeyError。
func uniqueIndex(field string) mongo.IndexModel {
	return mongo.IndexModel{
		Keys:    bson.D{{Key: field, Value: 1}},
		Options: options.Index().SetUnique(true),
	}
}

// normalIndex：普通（可指定方向）索引。
func normalIndex(field string, direction int) mongo.IndexModel {
	return mongo.IndexModel{
		Keys: bson.D{{Key: field, Value: direction}},
	}
}
