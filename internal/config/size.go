package config

import (
	"strconv"
	"strings"
)

// ByteSize 是 yaml 里的体积字段（"1MB" / "512KB"）。
// 请求体上限这种东西写 "4MB" 比写 4194304 可读得多；单位按二进制（1024）解释。
type ByteSize int64

const (
	KB ByteSize = 1024
	MB ByteSize = 1024 * KB
	GB ByteSize = 1024 * MB
)

func (b *ByteSize) UnmarshalYAML(unmarshal func(any) error) error {
	var raw any
	if err := unmarshal(&raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case int:
		*b = ByteSize(v)
		return nil
	case int64:
		*b = ByteSize(v)
		return nil
	case string:
		s := strings.TrimSpace(v)
		// 拆出数字与单位，单位不区分大小写（mb / Mb / MB 都接受）
		i := 0
		for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
			i++
		}
		num, err := strconv.ParseFloat(s[:i], 64)
		if err != nil {
			return errUnsupportedSize(v)
		}
		unit := strings.ToUpper(strings.TrimSpace(s[i:]))
		switch unit {
		case "", "B":
			*b = ByteSize(num)
		case "K", "KB":
			*b = ByteSize(num * float64(KB))
		case "M", "MB":
			*b = ByteSize(num * float64(MB))
		case "G", "GB":
			*b = ByteSize(num * float64(GB))
		default:
			return errUnsupportedSize(v)
		}
		return nil
	default:
		return errUnsupportedSize(v)
	}
}

func (b ByteSize) Int() int     { return int(b) }
func (b ByteSize) Int64() int64 { return int64(b) }
