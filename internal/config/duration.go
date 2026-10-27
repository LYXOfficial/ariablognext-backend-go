package config

import "time"

// Duration 是 yaml 里的时长字段（"8s" / "120s" / "5m"）。
// 单独建类型而不是直接用 time.Duration，是为了让配置文件里能写字符串形式，
// 也允许纯数字（按秒解释），避免配置里出现 8000000000 这种没法读的裸纳秒。
type Duration time.Duration

func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var raw any
	if err := unmarshal(&raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case int:
		*d = Duration(time.Duration(v) * time.Second)
		return nil
	case int64:
		*d = Duration(time.Duration(v) * time.Second)
		return nil
	case float64:
		*d = Duration(time.Duration(v * float64(time.Second)))
		return nil
	case string:
		parsed, err := time.ParseDuration(v)
		if err != nil {
			return err
		}
		*d = Duration(parsed)
		return nil
	default:
		return errUnsupportedDuration(v)
	}
}

// Std 还原成标准库的 time.Duration。
func (d Duration) Std() time.Duration { return time.Duration(d) }
