package service

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // 注册解码器：side-effect import，按契约支持的格式
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/platform"
)

// ThemeService 是友链头像取色：下载头像 → 缩到 64×64 分桶 → 取主色。
// 安全约束（契约 §3.4）：仅 http(s)、禁私网/环回（SSRF）、8 秒总时限、
// 最多 3 次重定向、512KB 体积上限。
type ThemeService struct {
	client *http.Client
	// allowPrivate 只给测试用：httptest 服务在 127.0.0.1 上，
	// 生产构造函数必须保持 false（SSRF 闸门）。
	allowPrivate bool
}

func NewThemeService() *ThemeService {
	// 8s 总时限是契约值；重定向限 3 跳、每一跳都过内网校验，都由 NewSafeHTTPClient 负责
	return &ThemeService{client: platform.NewSafeHTTPClient(false, 8*time.Second)}
}

// ThemeColor 返回头像主色（#rrggbb 小写）。
func (s *ThemeService) ThemeColor(ctx context.Context, rawURL string) (string, error) {
	u, err := s.validateExternalURL(rawURL)
	if err != nil {
		return "", err
	}
	img, err := s.fetchImage(ctx, u)
	if err != nil {
		return "", err
	}
	return dominantColor(img), nil
}

// validateExternalURL 是 SSRF 第一道闸：协议白名单 + 拒绝解析到私网/环回的 host。
// 私网拦截不能省：云元数据（169.254.169.254）和内网服务都在这个范围里。
func (s *ThemeService) validateExternalURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, apperr.InvalidField("url", "头像链接无法解析")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, apperr.InvalidField("url", "头像必须以 http:// 或 https:// 开头")
	}
	if u.Hostname() == "" {
		return nil, apperr.InvalidField("url", "头像链接缺少主机名")
	}
	// 内网拦截在 dial 阶段做（能覆盖重定向与 DNS rebinding，见 platform/safedial.go）；
	// 这里只做协议与主机名的快速校验。
	return u, nil
}

// fetchImage 下载并解码头像。响应体用 LimitedReader 截断在 512KB，
// 防止「图片实际是 4GB 的恶意文件」把内存打爆。
func (s *ThemeService) fetchImage(ctx context.Context, u *url.URL) (image.Image, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, apperr.New(apperr.ImageUnreachable, "头像请求构造失败", apperr.CauseDetails{Cause: err.Error()})
	}
	req.Header.Set("User-Agent", "AriaBlogNext-theme-color/1.0")
	resp, err := s.client.Do(req)
	if err != nil {
		// 内网/重定向到内网在 dial 阶段被拦（safedial.go），按参数错误返回
		if errors.Is(err, platform.ErrPrivateAddress) {
			return nil, apperr.InvalidField("url", "头像地址指向内网，已拒绝")
		}
		return nil, apperr.New(apperr.ImageUnreachable, "头像下载失败（对方站点无响应或超时），可稍后重试",
			apperr.CauseDetails{Cause: err.Error()})
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apperr.New(apperr.ImageUnreachable,
			"头像下载失败（对方返回 "+strconv.Itoa(resp.StatusCode)+"），可检查链接或稍后重试", nil)
	}
	img, _, err := image.Decode(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		// 到这一步还解不出来 = 不是图片（或是不支持的格式）
		return nil, apperr.New(apperr.ImageUnsupportedType, "头像不是支持的图片格式（jpeg/png/gif）", nil)
	}
	return img, nil
}

// dominantColor 64×64 分桶取主色：每通道量化到 4bit（16 桶），
// 取样本最多的桶的平均色。人眼对「最大色块」的感知比精确均值好得多。
func dominantColor(img image.Image) string {
	b := img.Bounds()
	// 缩到 64×64 网格采样：大图也不用逐像素扫
	const samples = 64
	type bucketKey [3]uint8
	// sums 存指针：数组值从 map 取出来不可寻址，直接改会编译错误
	counts := map[bucketKey]int{}
	sums := map[bucketKey]*[3]int64{}

	for sy := 0; sy < samples; sy++ {
		for sx := 0; sx < samples; sx++ {
			px := b.Min.X + (sx*(b.Dx()-1))/(samples-1)
			py := b.Min.Y + (sy*(b.Dy()-1))/(samples-1)
			c := color.NRGBAModel.Convert(img.At(px, py)).(color.NRGBA)
			if c.A < 128 { // 透明像素不参与统计
				continue
			}
			key := bucketKey{uint8(c.R >> 4), uint8(c.G >> 4), uint8(c.B >> 4)}
			counts[key]++
			if sums[key] == nil {
				sums[key] = &[3]int64{}
			}
			sums[key][0] += int64(c.R)
			sums[key][1] += int64(c.G)
			sums[key][2] += int64(c.B)
		}
	}
	if len(counts) == 0 {
		return "#888888" // 全透明：给中性灰，别让前端取色崩掉
	}
	keys := make([]bucketKey, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return counts[keys[i]] > counts[keys[j]] })
	best := keys[0]
	n := int64(counts[best])
	s := sums[best]
	return fmt.Sprintf("#%02x%02x%02x", s[0]/n, s[1]/n, s[2]/n)
}
