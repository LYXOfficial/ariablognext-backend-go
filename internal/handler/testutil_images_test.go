package handler

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/textproto"
	"testing"
)

// multipartBodyT 构造 multipart 请求体（文件字段 + 文本字段）。
func multipartBodyT(t *testing.T, fileField, filename string, fileData []byte, textField, textValue string) (*bytes.Buffer, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="`+fileField+`"; filename="`+filename+`"`)
	part, err := w.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(fileData); err != nil {
		t.Fatal(err)
	}
	if textField != "" {
		if err := w.WriteField(textField, textValue); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf, w.FormDataContentType()
}

// pngFixtureT 生成纯色 PNG。
func pngFixtureT(size int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, color.RGBA{R: 0xef, G: 0x85, B: 0x9d, A: 255})
		}
	}
	buf := &bytes.Buffer{}
	_ = png.Encode(buf, img)
	return buf.Bytes()
}
