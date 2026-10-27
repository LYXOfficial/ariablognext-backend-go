package platform

import (
	"bytes"
	"io"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

func deref64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func asAppErr(err error, target **apperr.Error) bool {
	if e, ok := err.(*apperr.Error); ok {
		*target = e
		return true
	}
	return false
}
