package mongo

// embedRepo：PostEmbeddings 集合仓储。向量是附属数据，写路径也受只读硬闸门管。

import (
	"context"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type embedRepo struct {
	coll *mongo.Collection
	ro   bool
}

var _ store.EmbeddingRepository = (*embedRepo)(nil)

func (s *Store) Embeddings() store.EmbeddingRepository {
	return &embedRepo{coll: s.db.Collection("PostEmbeddings"), ro: s.ro}
}

// Upsert 以 slug（_id）为键整体替换。文档里不出现 ObjectID，不受
// ObjectIDAsHexString 选项影响；slug 是 24 位 hex 字符串恰好也兼容。
func (r *embedRepo) Upsert(ctx context.Context, doc store.EmbeddingDoc) error {
	if r.ro {
		return store.ErrReadOnly
	}
	_, err := r.coll.ReplaceOne(ctx, bson.M{"_id": doc.Slug}, doc, options.Replace().SetUpsert(true))
	return err
}

func (r *embedRepo) DeleteBySlug(ctx context.Context, slug string) (bool, error) {
	if r.ro {
		return false, store.ErrReadOnly
	}
	res, err := r.coll.DeleteOne(ctx, bson.M{"_id": slug})
	if err != nil {
		return false, err
	}
	return res.DeletedCount > 0, nil
}

func (r *embedRepo) ListAll(ctx context.Context) ([]store.EmbeddingDoc, error) {
	cursor, err := r.coll.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	var docs []store.EmbeddingDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	if docs == nil {
		docs = []store.EmbeddingDoc{}
	}
	return docs, nil
}
