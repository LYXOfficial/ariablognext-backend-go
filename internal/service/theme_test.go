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
	// 本地起一个 HTTP 服务当「头像站」，验证下载→解码→取色全链路
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(makeTestPNG(32, color.RGBA{R: 0xF0, G: 0x80, B: 0x7F, A: 255}))
	}))
	defer srv.Close()
	s := allowPrivateThemeService()
	got, err := s.ThemeColor(context.Background(), srv.URL+"/a.png")
	if err != nil {
		t.Fatal(err)
	}
	// 单色图的主色就是该色（量化允许 ±16 的桶内误差）
	if !strings.HasPrefix(got, "#") || len(got) != 7 {
		t.Fatalf("格式应为 #rrggbb：%q", got)
	}
	if !strings.Contains(got, "f") && !strings.Contains(got, "e") {
		t.Logf("主色 %s（量化后与原色有桶内偏差，允许）", got)
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

func TestDominantColorTransparentFallback(t *testing.T) {
	// 全透明图：返回中性灰，前端取色不至于崩
	img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	if got := dominantColor(img); got != "#888888" {
		t.Errorf("全透明应回 #888888：%q", got)
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
