package middleware

import (
	"log/slog"
	"runtime/debug"
)

// Safe 执行 fn，把 panic 转成 error 返回（onPanic 负责造错误对象）。
// 进程不退出，栈只进日志——对外的信封里永远不该出现栈信息。
func Safe[T any](fn func() (T, error), onPanic func(r any) error) (result T, err error) {
	defer func() {
		if r := recover(); r != nil {
			// debug.Stack 必须在 recover 的 deferred 函数里取，离开 runtime 才拿得到
			slog.Error("panic recovered", "panic", r, "stack", string(debug.Stack()))
			var zero T
			result, err = zero, onPanic(r)
		}
	}()
	return fn()
}
