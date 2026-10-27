package service

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/platform"
)

// theme 服务只测「能脱离外网验证」的部分：URL 校验（SSRF）与分桶取色。
// 真实抓图（对方站点 8s 时限等）由冒烟/生产只读验证覆盖。

func TestValidateExternalURL(t *testing.T) {
	ok := []string{
		"https://img.0v0.my/avatar.png",
		"http://example.com/a.jpg",
	}
	svc := NewThemeService()
	for _, u := range ok {
		if _, err := svc.validateExternalURL(u); err != nil {
			t.Errorf("%s 不该被拒：%v", u, err)
		}
	}
	// 协议/主机名校验（内网拦截在 dial 阶段，见 platform/safedial_test.go）
	bad := []string{
		"ftp://x/y.png",       // 协议白名单
		"javascript:alert(1)", // 协议白名单
		"/relative/path.png",  // 无主机
	}
	for _, u := range bad {
		if _, err := svc.validateExternalURL(u); err == nil {
			t.Errorf("%s 应被拒绝", u)
		}
	}
}

func makeTestPNG(size int, c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, c)
		}
	}
	buf := &bytes.Buffer{}
	_ = png.Encode(buf, img)
	return buf.Bytes()
}

func TestThemeColorFromServer(t *testing.T) {
	// 本地起一个 HTTP 服务当「头像站」，验证下载→解码→取色全链路（旧算法复刻）
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(makeTestPNG(32, color.RGBA{R: 0xF0, G: 0x80, B: 0x7F, A: 255}))
	}))
	defer srv.Close()
	s := allowPrivateThemeService()
	got, err := s.ThemeColor(context.Background(), srv.URL+"/a.png")
	if err != nil {
		t.Fatal(err)
	}
	// 单色图：所有像素进同一桶，平均色 normalize_for_card 后是确定值。
	// #ef859d: l≈0.73>0.58→0.58, s≈0.77>0.75→0.75 → #e44544（手算常量，防回归；取色与旧后端逐头像对比见 P4_DONE）
	if got != "#e44544" {
		t.Errorf("单色图取色（含 normalize_for_card）= %q，应为 #e44544（手算常量，防回归）", got)
	}
}

func TestThemeColorUnreachable(t *testing.T) {
	s := allowPrivateThemeService()
	// 不存在的端口：image.unreachable 而不是 panic / internal.error
	_, err := s.ThemeColor(context.Background(), "http://127.0.0.1:1/a.png")
	if err == nil || !strings.Contains(err.Error(), "image.unreachable") {
		t.Errorf("不可达应报 image.unreachable：%v", err)
	}
}

// 全透明图：按旧算法全部像素 a<128 被跳过、n=0 → 返回空串
// （旧 getThemeColor 会回 200 + data:""，前端落到默认色，不会崩）。
func TestThemeColorTransparentFallback(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	if got := extractThemeColor(boxResize64(img)); got != "" {
		t.Errorf("全透明应回空串（旧行为），得到 %q", got)
	}
	// 半透明混合：a≥128 的像素参与平均（PIL getdata 非预乘口径）
	img2 := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img2.SetNRGBA(x, y, color.NRGBA{R: 0x20, G: 0x10, B: 0x10, A: 255})
		}
	}
	if got := extractThemeColor(boxResize64(img2)); !strings.HasPrefix(got, "#") {
		t.Errorf("普通图应产出 hex：%q", got)
	}
}

// allowPrivateThemeService 是测试专用构造：httptest 服务监听 127.0.0.1，
// 生产构造函数必须保持禁内网（allowPrivate=false）。
func allowPrivateThemeService() *ThemeService {
	s := NewThemeService()
	s.allowPrivate = true
	s.client = platform.NewSafeHTTPClient(true, 8*time.Second)
	return s
}

// theme 服务只测「能脱离外网验证」的部分：URL 校验（SSRF）与分桶取色。
// 真实抓图（对方站点 8s 时限等）由冒烟/生产只读验证覆盖。
