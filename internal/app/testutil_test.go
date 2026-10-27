package app

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/textproto"
	"testing"
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// multipartBody 构造一个 multipart 请求体：一个文件字段 + 一个文本字段。
func multipartBody(t *testing.T, fileField, filename string, fileData []byte, textField, textValue string) (*bytes.Buffer, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="`+fileField+`"; filename="`+filename+`"`)
	h.Set("Content-Type", "image/png")
	part, err := w.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(fileData); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField(textField, textValue); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf, w.FormDataContentType()
}
