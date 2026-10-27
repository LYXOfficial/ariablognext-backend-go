//go:build integration

package config

import "os"

// TestDatabase 读集成测试用的数据库连接参数。
//
// 为什么放在这里：架构守卫规定 `os.Getenv` 只能出现在 internal/config
// （配置必须从 config.yml 走）。集成测试又确实需要「测试库在哪」这种运行时参数，
// 于是用一个带 `integration` build tag 的文件承接——普通构建与 `go test ./...`
// 都不会编译它，只有 `go test -tags=integration` 时存在。
//
// 返回 ok=false 表示没有配置测试库，调用方应当 Skip 而不是失败。
func TestDatabase() (uri, name string, ok bool) {
	uri = os.Getenv("MONGODB_URI_TEST")
	if uri == "" {
		return "", "", false
	}
	name = os.Getenv("TEST_DB_NAME")
	if name == "" {
		name = "AriaBlogNext_test"
	}
	return uri, name, true
}
