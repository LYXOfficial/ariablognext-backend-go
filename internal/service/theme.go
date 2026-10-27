package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // 注册解码器：side-effect import，按契约支持的格式
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	_ "golang.org/x/image/bmp"  // 旧后端 PIL 支持 bmp，解码器对齐
	_ "golang.org/x/image/webp" // 旧后端 PIL 支持 webp（友链头像里有 webp）

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/platform"
)

// themeFetchUA 是取色下载的 UA（部分图床 WAF 拒绝空 UA）。
const themeFetchUA = "Mozilla/5.0 (compatible; AriablogNext/1.0; +https://blog.yaria.top)"

// maxImageBytes 是取色下载的体积上限（与旧后端一致）。
const maxImageBytes = 512 * 1024

// maxImagePixels 是解码内存上限（约 4000×4000），防解压炸弹（与旧后端一致）。
const maxImagePixels = 16_000_000

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
	body, err := s.fetchImageBytes(ctx, u)
	if err != nil {
		return "", err
	}
	img, err := decodeImageBounded(body)
	if err != nil {
		return "", err
	}
	return extractThemeColor(boxResize64(img)), nil
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

// decodeImageBounded 先读图片头校验像素总数再完整解码：
// 512KB 的 PNG 可能解压出上亿像素，不设上限会被解压炸弹打爆内存（与旧 413 行为一致）。
func decodeImageBounded(body []byte) (image.Image, error) {
	cfgImg, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return nil, apperr.New(apperr.ImageUnsupportedType,
			"头像不是支持的图片格式（jpeg/png/gif/webp），无法取色", nil)
	}
	if cfgImg.Width*cfgImg.Height > maxImagePixels {
		return nil, apperr.New(apperr.ImageTooLarge, "头像图片尺寸过大，无法取色", nil)
	}
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, apperr.New(apperr.ImageUnsupportedType,
			"头像图片解析失败，无法取色", apperr.CauseDetails{Cause: err.Error()})
	}
	return img, nil
}

// fetchImageBytes 下载头像原始字节。响应体用 LimitedReader 截断在 512KB，
// 防止「图片实际是 4GB 的恶意文件」把内存打爆；解码与像素上限在调用侧。
func (s *ThemeService) fetchImageBytes(ctx context.Context, u *url.URL) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, apperr.New(apperr.ImageUnreachable, "头像请求构造失败", apperr.CauseDetails{Cause: err.Error()})
	}
	req.Header.Set("User-Agent", themeFetchUA)
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
			"头像下载失败（对方返回 HTTP "+strconv.Itoa(resp.StatusCode)+"，可检查链接）", nil)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes))
	if err != nil {
		return nil, apperr.New(apperr.ImageUnreachable, "头像下载中断", apperr.CauseDetails{Cause: err.Error()})
	}
	if len(body) >= maxImageBytes {
		return nil, apperr.New(apperr.ImageTooLarge, "头像图片过大（超过 512KB），无法取色", nil)
	}
	return body, nil
}

// ---------- 取色算法：逐行复刻旧后端 get/flink/main.py ----------

// jsRound 对齐 JS 的 Math.round：.5 一律向上（Python 内置 round 是银行家舍入，
// 分桶键和均值取整会因此得到不同结果，不能直接用 math.Round）。
func jsRound(x float64) float64 { return math.Floor(x + 0.5) }

type themeHSL struct{ h, s, l float64 }

// themeRGBToHSL 是旧 _rgb_to_hsl 的逐行移植。
func themeRGBToHSL(r, g, b int) themeHSL {
	rn, gn, bn := float64(r)/255, float64(g)/255, float64(b)/255
	mx, mn := max(rn, gn, bn), min(rn, gn, bn)
	l := (mx + mn) / 2
	if mx == mn {
		return themeHSL{0, 0, l}
	}
	d := mx - mn
	var s float64
	if l > 0.5 {
		s = d / (2 - mx - mn)
	} else {
		s = d / (mx + mn)
	}
	var h float64
	switch mx {
	case rn:
		add := 0.0
		if gn < bn {
			add = 6
		}
		h = ((gn-bn)/d + add) * 60
	case gn:
		h = ((bn-rn)/d + 2) * 60
	default:
		h = ((rn-gn)/d + 4) * 60
	}
	return themeHSL{h, s, l}
}

// themeHSLToRGB 是旧 _hsl_to_rgb 的逐行移植（含 js_round 与 0-255 截断）。
func themeHSLToRGB(x themeHSL) (int, int, int) {
	c := (1 - math.Abs(2*x.l-1)) * x.s
	hp := math.Mod(math.Mod(x.h, 360)+360, 360) / 60
	second := c * (1 - math.Abs(math.Mod(hp, 2)-1))
	var r, g, b float64
	switch {
	case hp < 1:
		r, g, b = c, second, 0
	case hp < 2:
		r, g, b = second, c, 0
	case hp < 3:
		r, g, b = 0, c, second
	case hp < 4:
		r, g, b = 0, second, c
	case hp < 5:
		r, g, b = second, 0, c
	default:
		r, g, b = c, 0, second
	}
	m := x.l - c/2
	clamp255 := func(v float64) int { return min(max(int(jsRound(v*255+m*255)), 0), 255) }
	return clamp255(r), clamp255(g), clamp255(b)
}

// normalizeForCard 把颜色压到适合白字卡片的明度/饱和度区间（旧 _normalize_for_card）。
func normalizeForCard(x themeHSL) themeHSL {
	if x.l > 0.58 {
		x.l = 0.58
	}
	if x.l < 0.28 {
		x.l = 0.28 + (x.l-0.28)*0.3
	}
	if x.s < 0.22 {
		x.s = 0.22
	}
	if x.s > 0.75 {
		x.s = 0.75
	}
	return x
}

func themeToHex(r, g, b int) string {
	return fmt.Sprintf("#%02x%02x%02x", min(max(r, 0), 255), min(max(g, 0), 255), min(max(b, 0), 255))
}

// boxResize64 把图缩到 64×64：每个目标像素 = 源矩形内 RGBA 各通道（非预乘，
// 与 PIL getdata 口径一致）的算术平均。与旧后端 PIL BOX 的实现在边界像素上
// 可能有 ±1 通道差（视觉无感知，用户确认可接受），主色结论一致。
func boxResize64(img image.Image) []color.NRGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	const n = 64
	out := make([]color.NRGBA, 0, n*n)
	for j := 0; j < n; j++ {
		y0 := int(float64(j) * float64(h) / n)
		y1 := max(int(float64(j+1)*float64(h)/n), y0+1)
		for i := 0; i < n; i++ {
			x0 := int(float64(i) * float64(w) / n)
			x1 := max(int(float64(i+1)*float64(w)/n), x0+1)
			var rs, gs, bs, as, cnt float64
			for y := y0; y < y1 && y < b.Max.Y; y++ {
				for x := x0; x < x1 && x < b.Max.X; x++ {
					c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
					rs += float64(c.R)
					gs += float64(c.G)
					bs += float64(c.B)
					as += float64(c.A)
					cnt++
				}
			}
			if cnt == 0 {
				continue
			}
			out = append(out, color.NRGBA{
				R: uint8(rs/cnt + 0.5),
				G: uint8(gs/cnt + 0.5),
				B: uint8(bs/cnt + 0.5),
				A: uint8(as/cnt + 0.5),
			})
		}
	}
	return out
}

// extractThemeColor 是旧 _extract_color_from_pixels 的逐行移植：
// 跳过透明/极浅/极深像素，按「色相/16 - 明度×8」分桶、彩度加权（1+s×3），
// 取最重的桶做平均色；并列取先插入的桶。全部被跳过时退化成
// 不透明像素平均色过 normalize；完全没有像素时返回空串。
func extractThemeColor(pixels []color.NRGBA) string {
	type bucket struct {
		weight  float64
		r, g, b float64
		n       float64
	}
	var order []string
	buckets := map[string]*bucket{}
	fbR, fbG, fbB, n := 0.0, 0.0, 0.0, 0.0
	for _, px := range pixels {
		if px.A < 128 {
			continue
		}
		fbR += float64(px.R)
		fbG += float64(px.G)
		fbB += float64(px.B)
		n++
		x := themeRGBToHSL(int(px.R), int(px.G), int(px.B))
		if x.l > 0.92 || x.l < 0.06 {
			continue
		}
		key := fmt.Sprintf("%d-%d", int(jsRound(x.h/16)), int(jsRound(x.l*8)))
		weight := 1 + x.s*3
		if cur, ok := buckets[key]; ok {
			cur.weight += weight
			cur.r += float64(px.R)
			cur.g += float64(px.G)
			cur.b += float64(px.B)
			cur.n++
		} else {
			order = append(order, key)
			buckets[key] = &bucket{weight: weight, r: float64(px.R), g: float64(px.G), b: float64(px.B), n: 1}
		}
	}
	if len(buckets) == 0 {
		if n == 0 {
			return ""
		}
		avg := themeRGBToHSL(int(jsRound(fbR/n)), int(jsRound(fbG/n)), int(jsRound(fbB/n)))
		r, g, b := themeHSLToRGB(normalizeForCard(avg))
		return themeToHex(r, g, b)
	}
	// 并列取先插入的桶（Python dict 保插入序 + max 取第一个，用 order 复刻）
	var best *bucket
	for _, key := range order {
		if best == nil || buckets[key].weight > best.weight {
			best = buckets[key]
		}
	}
	avg := themeRGBToHSL(int(jsRound(best.r/best.n)), int(jsRound(best.g/best.n)), int(jsRound(best.b/best.n)))
	r, g, b := themeHSLToRGB(normalizeForCard(avg))
	return themeToHex(r, g, b)
}
