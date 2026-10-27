// cmd/backfill-searchtext 给 Posts / Drafts 补 searchText（幂等）。
//
// 默认 **dry-run**：只统计「缺失或与重建结果不一致」的条数并打印样例；
// 必须显式 --apply 才写库（TASKS P4 的硬要求）。
//
// ⚠️ 只允许对 *_dev / *_test 库执行：云端（生产）库绝不可写是用户明令，
// 程序里强制校验库名。生产库不需要回填也能搜索——查询侧有
// 「searchText 缺失时退回 title+plainContent 匹配」的 fallback（见 mongo/keywordMatch）。
//
//	go run ./cmd/backfill-searchtext            # dry-run
//	go run ./cmd/backfill-searchtext --apply    # 真正写库（仅 dev/test）
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"strings"
	"time"

	_ "github.com/LYXOfficial/ariablognext-backend-go/internal/store/mongo"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/model"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/textutil"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	apply := flag.Bool("apply", false, "真正写库（默认只统计）")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("配置加载失败", "err", err)
		os.Exit(1)
	}
	// 安全闸：云端库绝不写，只允许 dev/test
	if !strings.HasSuffix(cfg.Database.Name, "_dev") && !strings.HasSuffix(cfg.Database.Name, "_test") {
		slog.Error("backfill 只能对 *_dev / *_test 库执行（生产库有查询 fallback，无需回填）",
			"db", cfg.Database.Name)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	st, err := store.Open(ctx, cfg)
	if err != nil {
		slog.Error("存储初始化失败", "err", err)
		os.Exit(1)
	}
	defer st.Close(context.Background())

	pending, planned, err := backfill(ctx, st, *apply)
	if err != nil {
		slog.Error("回填失败", "err", err)
		os.Exit(1)
	}
	mode := "dry-run"
	if *apply {
		mode = "apply"
	}
	slog.Info("完成", "mode", mode, "需要回填", pending, "已写入", planned)
}

// backfill 处理 Posts 与 Drafts，返回（需要回填条数, 实际写入条数）。
func backfill(ctx context.Context, st store.Store, apply bool) (int, int, error) {
	postsPending, postsWritten, err := backfillPosts(ctx, st, apply)
	if err != nil {
		return 0, 0, err
	}
	draftsPending, draftsWritten, err := backfillDrafts(ctx, st, apply)
	if err != nil {
		return 0, 0, err
	}
	return postsPending + draftsPending, postsWritten + draftsWritten, nil
}

func backfillPosts(ctx context.Context, st store.Store, apply bool) (int, int, error) {
	reader, ok := st.Posts().(store.SearchTextReader)
	if !ok {
		return 0, 0, nil
	}
	existing, err := reader.SearchTexts(ctx)
	if err != nil {
		return 0, 0, err
	}
	// Full 拉全量（83 篇规模；仓储不拦 300 上限，那是 service 的业务规则）
	page, err := st.Posts().Page(ctx, store.PostFilter{Full: true, WithContent: true}, time.UTC)
	if err != nil {
		return 0, 0, err
	}
	pending, written := 0, 0
	for _, p := range page.Items {
		want := textutil.BuildSearchText(p.Title, p.MdContent)
		if existing[p.Slug] == want {
			continue // 幂等：已经一致的不重复写
		}
		pending++
		if !apply {
			if pending <= 5 {
				slog.Info("将回填（样例）", "slug", p.Slug, "旧长度", len(existing[p.Slug]), "新长度", len(want))
			}
			continue
		}
		plain := textutil.CleanMarkdown(p.MdContent)
		if err := st.Posts().UpdateContent(ctx, p.Slug, model.PostContent{
			Markdown:     p.MdContent,
			PlainContent: truncateRunes(plain, 201),
			SearchText:   want,
			WordCount:    textutil.WordCount(plain),
		}); err != nil {
			return pending, written, err
		}
		written++
	}
	return pending, written, nil
}

func backfillDrafts(ctx context.Context, st store.Store, apply bool) (int, int, error) {
	reader, ok := st.Drafts().(store.SearchTextReader)
	if !ok {
		return 0, 0, nil
	}
	existing, err := reader.SearchTexts(ctx)
	if err != nil {
		return 0, 0, err
	}
	page, err := st.Drafts().Page(ctx, store.DraftFilter{Full: true, WithContent: true})
	if err != nil {
		return 0, 0, err
	}
	pending, written := 0, 0
	for _, d := range page.Items {
		// 草稿的 mdContent 可空（库里 4/8 条缺）：空正文就只索引标题
		md := ""
		if d.MdContent != nil {
			md = *d.MdContent
		}
		want := textutil.BuildSearchText(d.Title, md)
		if existing[d.Slug] == want {
			continue
		}
		pending++
		if !apply {
			continue
		}
		plain := textutil.CleanMarkdown(md)
		if err := st.Drafts().UpdateContent(ctx, d.Slug, model.PostContent{
			Markdown:     md,
			PlainContent: truncateRunes(plain, 201),
			SearchText:   want,
			WordCount:    textutil.WordCount(plain),
		}); err != nil {
			return pending, written, err
		}
		written++
	}
	return pending, written, nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
