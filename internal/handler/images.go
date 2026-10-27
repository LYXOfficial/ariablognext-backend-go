package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/app"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/platform"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/service"
)

// ImageStore 图床能力由 media 配置开关决定：
// enabled=false 时路由仍注册（前端/胶水始终能访问到稳定路径），但返回 503 upstream.failed
// 「图床未配置」——比 404 更能说明问题（TASKS P3 验收的明确语义，与 design-notes §3.4
// 的「关闭即不注册」取舍冲突时以施工清单验收为准，已记录 docs/pitfalls.md）。
type ImageStore struct {
	cfg *config.Config
	s3  *platform.S3
}

func NewImageStore(cfg *config.Config) *ImageStore {
	s := &ImageStore{cfg: cfg}
	if cfg.Media.Enabled {
		// S3 客户端构造失败也按「未配置」降级，不让启动失败——
		// media.enabled=true 但密钥错这种事，503 的错误信息里能看清楚
		s3c, err := platform.NewS3(context.Background(), cfg.Media.S3)
		if err != nil {
			loggerWarn("图床客户端初始化失败（将以 503 服务）", "err", err)
		} else {
			s.s3 = s3c
		}
	}
	return s
}

// s3OrFail：未配置时给 503 upstream.failed。
func (s *ImageStore) s3OrFail() (*platform.S3, error) {
	if s.s3 == nil {
		return nil, apperr.New(apperr.UpstreamFailed, "图床未配置，请联系站长启用", nil)
	}
	return s.s3, nil
}

var safeKeyRe = regexp.MustCompile(`[^a-zA-Z0-9._/-]`)

// safeKey 清洗对象键：只留字母数字、点、斜杠、横线，防路径穿越。
func safeKey(name string) string {
	return strings.Trim(safeKeyRe.ReplaceAllString(name, "-"), "/")
}

// extFromName / extFromType：推断存储扩展名与 Content-Type。
var allowedExt = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
	".webp": "image/webp", ".gif": "image/gif",
}

func extOf(name string) string {
	return strings.ToLower(filepath.Ext(name))
}

func randomKey(ext string) string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	// 按 日期/随机名 组织：图床目录天然按时间排序
	return time.Now().Format("2006/01/02") + "/" + hex.EncodeToString(buf) + ext
}

// List：GET /images/list（Admin）。
//
// ⚠️ `data.images` 必须是**URL 字符串数组**：旧后端 `/update/image/listImages` 返回的就是
// `{"images": ["https://img.0v0.my/…", …]}`，dashboard 直接把它当 `string[]` 用
// （`key={img}`、`img` 当图片地址）。返回对象数组会让图片墙整片坏掉，
// 而胶水侧是按原样透传 `data` 的（它没有做 object→string 转换）。
// 富信息（key/size/lastModified）另放 `data.items`，给以后的 dashboard 升级用；
// 旧前端忽略未知键，无影响。
func (s *ImageStore) List() app.Handler {
	return func(c *app.Ctx) (any, error) {
		client, err := s.s3OrFail()
		if err != nil {
			return nil, err
		}
		items, err := client.List(c.Context())
		if err != nil {
			return nil, err
		}
		// 按最后修改时间倒序（契约 §3.8「按日期倒序」；旧后端也是排序后返回）
		sort.Slice(items, func(i, j int) bool {
			return items[i].LastModified > items[j].LastModified
		})
		urls := make([]string, 0, len(items))
		for _, it := range items {
			urls = append(urls, it.URL)
		}
		return map[string]any{"images": urls, "items": items}, nil
	}
}

// Upload：POST /images/upload（multipart: file）。
func (s *ImageStore) Upload() app.Handler {
	return func(c *app.Ctx) (any, error) {
		client, err := s.s3OrFail()
		if err != nil {
			return nil, err
		}
		body, filename, err := c.FormFile("file")
		if err != nil {
			return nil, err
		}
		ct := allowedExt[extOf(filename)]
		if ct == "" {
			return nil, apperr.New(apperr.ImageUnsupportedType, "只支持 jpeg/png/webp/gif 图片", nil)
		}
		if len(body) > s.cfg.Media.Upload.MaxBytes.Int() {
			return nil, apperr.New(apperr.ImageTooLarge,
				"图片超过 "+itoaHandler(s.cfg.Media.Upload.MaxBytes.Int()/(1<<20))+"MB 上限，请压缩后重试", nil)
		}
		url, err := client.Upload(c.Context(), randomKey(extOf(filename)), body, ct)
		if err != nil {
			return nil, err
		}
		return map[string]any{"url": url}, nil
	}
}

// Reupload：PUT /images/reupload（multipart: url + file）——用新文件覆盖旧图。
func (s *ImageStore) Reupload() app.Handler {
	return func(c *app.Ctx) (any, error) {
		client, err := s.s3OrFail()
		if err != nil {
			return nil, err
		}
		oldURL := c.FormValue("url")
		if oldURL == "" {
			return nil, apperr.New(apperr.ValidationMissingField, "缺少 url 字段", apperrFieldDetail("url", "必填"))
		}
		key, err := s.keyFromURL(oldURL)
		if err != nil {
			return nil, err
		}
		body, filename, err := c.FormFile("file")
		if err != nil {
			return nil, err
		}
		ct := allowedExt[extOf(filename)]
		if ct == "" {
			return nil, apperr.New(apperr.ImageUnsupportedType, "只支持 jpeg/png/webp/gif 图片", nil)
		}
		url, err := client.Upload(c.Context(), key, body, ct)
		if err != nil {
			return nil, err
		}
		return map[string]any{"url": url}, nil
	}
}

// Import：POST /images/import（body {"url"}）——把外链图转存进图床。
// 下载限 3MB / 20 秒（config.media.import.*），非图片内容会被拒。
func (s *ImageStore) Import() app.Handler {
	return func(c *app.Ctx) (any, error) {
		client, err := s.s3OrFail()
		if err != nil {
			return nil, err
		}
		var body struct {
			URL string `json:"url"`
		}
		if err := c.Bind(&body); err != nil {
			return nil, err
		}
		if body.URL == "" {
			return nil, apperr.New(apperr.ValidationMissingField, "缺少 url", apperrFieldDetail("url", "必填"))
		}
		// 下载（复用 theme 的 SSRF 校验语义：只允许 http(s)，拒绝私网）
		fetched, contentType, err := platform.FetchExternalImage(c.Context(), body.URL,
			s.cfg.Media.Import.MaxBytes.Int(), s.cfg.Media.Import.Timeout.Std())
		if err != nil {
			return nil, err
		}
		ext := extFromContentType(contentType)
		if ext == "" {
			return nil, apperr.New(apperr.ImageUnsupportedType, "对方返回的不是支持的图片格式（jpeg/png/webp/gif）", nil)
		}
		url, err := client.Upload(c.Context(), randomKey(ext), fetched, contentType)
		if err != nil {
			return nil, err
		}
		return map[string]any{"url": url}, nil
	}
}

// Delete：DELETE /images/delete?url=。
func (s *ImageStore) Delete() app.Handler {
	return func(c *app.Ctx) (any, error) {
		client, err := s.s3OrFail()
		if err != nil {
			return nil, err
		}
		rawURL, err := c.RequiredQuery("url")
		if err != nil {
			return nil, err
		}
		key, err := s.keyFromURL(rawURL)
		if err != nil {
			return nil, err
		}
		if err := client.Delete(c.Context(), key); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true}, nil
	}
}

// keyFromURL 从公网 URL 反解对象键：publicURL 前缀必须对得上，
// 对不上就是「不是本图床的图」，拒绝操作（防误删别人域名下的东西）。
func (s *ImageStore) keyFromURL(rawURL string) (string, error) {
	prefix := strings.TrimSuffix(s.cfg.Media.S3.PublicURL, "/") + "/"
	if !strings.HasPrefix(rawURL, prefix) {
		return "", apperr.InvalidField("url", "不是本图床的图片地址，无法操作")
	}
	key := safeKey(strings.TrimPrefix(rawURL, prefix))
	if key == "" {
		return "", apperr.InvalidField("url", "图片地址为空")
	}
	return key, nil
}

func extFromContentType(ct string) string {
	ct = strings.ToLower(strings.TrimSpace(ct))
	switch {
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
		return ".jpg"
	case strings.Contains(ct, "png"):
		return ".png"
	case strings.Contains(ct, "webp"):
		return ".webp"
	case strings.Contains(ct, "gif"):
		return ".gif"
	default:
		return ""
	}
}

func loggerWarn(msg string, args ...any) { slog.Warn(msg, args...) }

func itoaHandler(n int) string {
	if n == 0 {
		return "0"
	}
	buf := []byte{}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}

// ---------------- 友链申请（公开 apply / 内部 screening） ----------------

// applyBody：全部可选（缺失由 validate 给友好 400，与旧后端一致）。
type applyBody struct {
	Name      string `json:"name"`
	Link      string `json:"link"`
	Avatar    string `json:"avatar"`
	Descr     string `json:"descr"`
	Email     string `json:"email"`
	Color     string `json:"color"`
	Type      *int   `json:"type"`
	TargetURL string `json:"targetUrl"`
}

// applicationsApply：POST /links/applications/apply（公开）。
// 公开原因：申请人自助提交；滥用面由三重限流与判重兜住
// （全局 300/min + 本路由 5/30m + 队列容量 20，与旧后端等价）。
func applicationsApply(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var b applyBody
		if err := c.Bind(&b); err != nil {
			return nil, err
		}
		appType := 0
		if b.Type != nil {
			appType = *b.Type
		}
		return svc.Applications.Apply(c.Context(), service.ApplyInput{
			Name: b.Name, Link: b.Link, Avatar: b.Avatar, Descr: b.Descr,
			Email: b.Email, Color: b.Color, Type: appType, TargetURL: b.TargetURL,
			IP: c.IP(),
		})
	}
}

// applicationsScreening：POST /links/applications/screening（内部 token）。
// 调用方是 GitHub Actions 初筛 workflow；截断长度与旧后端一致
// （summary 200 / reason 300 / model 50）。
func applicationsScreening(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var body struct {
			ID       string `json:"id"`
			Verdict  string `json:"verdict"`
			Backlink string `json:"backlink"`
			Summary  string `json:"summary"`
			Reason   string `json:"reason"`
			Model    string `json:"model"`
		}
		if err := c.Bind(&body); err != nil {
			return nil, err
		}
		if body.ID == "" {
			return nil, apperr.New(apperr.ValidationMissingField, "缺少 id", apperrFieldDetail("id", "必填"))
		}
		if err := svc.Applications.Screening(c.Context(), body.ID, body.Verdict,
			truncateStr(body.Backlink, 500), truncateStr(body.Summary, 200),
			truncateStr(body.Reason, 300), truncateStr(body.Model, 50)); err != nil {
			return nil, err
		}
		return map[string]any{"updated": true}, nil
	}
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// linksCheck：POST /links/check（Admin）——手动触发外部 check-flink 仓库的链路检查。
// 失败 → upstream.failed（不静默：这是管理员主动动作，需要明确反馈）。
func linksCheck(cfg *config.Config) app.Handler {
	return func(c *app.Ctx) (any, error) {
		lc := cfg.Flink.LatencyCheck
		if !lc.Enabled {
			return nil, apperr.New(apperr.UpstreamFailed, "链路检查功能未启用", nil)
		}
		ctx, cancel := context.WithTimeout(c.Context(), 8*time.Second)
		defer cancel()
		if err := platform.DispatchWorkflow(ctx, lc.Repo, lc.Token, lc.Workflow, "main"); err != nil {
			return nil, err
		}
		return map[string]any{"dispatched": true}, nil
	}
}
