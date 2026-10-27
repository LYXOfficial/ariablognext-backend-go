package config

import "fmt"

// errUnsupportedDuration / errUnsupportedSize 统一走这里，
// 报错里带上「该写成什么样子」，配置写错的人能直接照着改。
func errUnsupportedDuration(v any) error {
	return fmt.Errorf("无法识别的时长写法 %q（应写成 8s / 120s / 5m 这类）", v)
}

func errUnsupportedSize(v any) error {
	return fmt.Errorf("无法识别的体积写法 %q（应写成 1MB / 512KB / 4MB 这类）", v)
}
