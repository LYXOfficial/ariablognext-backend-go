// Package mongo 是 store.Store 的生产实现。
// bson / mongo-driver 只允许出现在这个包（和 store/open.go）里；
// 所有驱动错误必须转成 store 的哨兵（ErrNotFound / ErrDuplicate）再往上抛。
package mongo

import (
	"context"
	"fmt"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Store 持有驱动客户端与各集合仓储。实例由 main 显式构造并传递，
// 不做包级单例——旧后端 db.py 的全局单例在这里变成一个显式值。
type Store struct {
	client *mongo.Client
	db     *mongo.Database
	ro     bool
}

// 编译期断言：mongo.Store 同时满足 Store 与可选的 RawExporter 能力。
var (
	_ store.Store       = (*Store)(nil)
	_ store.RawExporter = (*Store)(nil)
)

// Open 建立连接并 Ping 一次。连接失败立刻报错——带着坏连接启动只会把问题推迟到第一个请求。
func open(ctx context.Context, cfg *config.Config) (*Store, error) {
	// 池子参数与 design-notes §4 一致：Vercel 单实例并发有限，不用开大
	opts := options.Client().ApplyURI(cfg.Database.URI).
		SetMaxPoolSize(uint64(cfg.Database.Pool.Max)).
		SetMinPoolSize(uint64(cfg.Database.Pool.Min)).
		SetMaxConnIdleTime(cfg.Database.Pool.IdleTimeout.Std()).
		// ObjectID 一律解码成 24 位 hex 字符串：接口层的 ID 是 string（store-interfaces §0），
		// 没有这个选项，解 _id 到 model.LinkApplication.ID 会直接报错。
		SetBSONOptions(&options.BSONOptions{ObjectIDAsHexString: true})

	// ⚠️ mongo-driver v2 的 Connect 不接收 ctx：连接是惰性的，
	// 真正等连接的是下面的 Ping（拿调用方的 ctx 做超时控制）。
	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("数据库 Ping 失败（检查 MONGODB_URI 与网络）：%w", err)
	}

	s := &Store{
		client: client,
		db:     client.Database(cfg.Database.Name),
		ro:     cfg.Database.ReadOnly,
	}
	if err := s.ensureIndexes(ctx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	return s, nil
}

func (s *Store) Ping(ctx context.Context) error {
	return s.client.Ping(ctx, nil)
}

func (s *Store) Close(ctx context.Context) error {
	return s.client.Disconnect(ctx)
}

func (s *Store) Posts() store.PostRepository {
	return &postRepo{coll: s.db.Collection("Posts"), ro: s.ro}
}

// Taxonomy 与 Posts 同集合，由 postRepo 顺带实现（见 store-interfaces.md §4）。
func (s *Store) Taxonomy() store.TaxonomyRepository {
	return s.Posts().(store.TaxonomyRepository)
}

func (s *Store) Drafts() store.DraftRepository {
	return &draftRepo{coll: s.db.Collection("Drafts"), ro: s.ro}
}

func (s *Store) Summaries() store.SummaryRepository {
	return &summaryRepo{coll: s.db.Collection("PostSummary"), ro: s.ro}
}

func (s *Store) Speaks() store.SpeakRepository {
	return &speakRepo{coll: s.db.Collection("Speaks"), ro: s.ro}
}

func (s *Store) Links() store.LinkRepository {
	return &linkRepo{coll: s.db.Collection("FLinks"), ro: s.ro}
}

func (s *Store) Applications() store.ApplicationRepository {
	return &applicationRepo{coll: s.db.Collection("FLinkApplications"), ro: s.ro}
}

func (s *Store) Users() store.UserRepository {
	return &userRepo{coll: s.db.Collection("Users")}
}

func (s *Store) Sites() store.SiteRepository {
	return &siteRepo{coll: s.db.Collection("webSiteInfo"), ro: s.ro}
}

// UpsertUser 实现 store.UserWriter（种子工具专用；业务接口不暴露用户写入）。
func (s *Store) UpsertUser(ctx context.Context, user, passwordHex string) error {
	if s.ro {
		return store.ErrReadOnly
	}
	return (&userRepo{coll: s.db.Collection("Users")}).UpsertUser(ctx, user, passwordHex)
}

// ensureIndexes 幂等建索引。unique 索引是写竞态安全的根基：
// Create 靠重复键错误返回 ErrDuplicate，没有索引那个保护就不存在。
// 索引已存在时 CreateIndex 是无害的（跳过）。
func (s *Store) ensureIndexes(ctx context.Context) error {
	indexes := []struct {
		coll  *mongo.Collection
		model mongo.IndexModel
	}{
		{coll: s.db.Collection("Posts"), model: uniqueIndex("slug")},
		{coll: s.db.Collection("Posts"), model: normalIndex("publishTime", 1)},
		{coll: s.db.Collection("Drafts"), model: uniqueIndex("slug")},
		{coll: s.db.Collection("PostSummary"), model: uniqueIndex("slug")},
		{coll: s.db.Collection("Speaks"), model: normalIndex("time", -1)},
		{coll: s.db.Collection("webSiteInfo"), model: uniqueIndex("key")},
		{coll: s.db.Collection("Users"), model: uniqueIndex("user")},
	}
	// 建索引有网络往返，给固定 30s——驱动默认超时对启动路径已经够用
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for _, i := range indexes {
		if _, err := i.coll.Indexes().CreateOne(ctx, i.model); err != nil {
			return fmt.Errorf("建索引失败（集合 %s）：%w", i.coll.Name(), err)
		}
	}
	return nil
}
