#!/usr/bin/env bash
# 静态守卫：把「架构约定」变成会失败的命令，而不是靠自觉。
# 任何一条不通过都必须先修代码，不要改这个脚本来绕过。
#
# 用法：bash scripts/checks.sh
# 退出码 0 = 全过；1 = 有违规（会打印文件:行号）。

set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

fail=0
say() { printf '%s\n' "$*"; }
bad() { fail=1; say "  ✗ $*"; }
ok()  { say "  ✓ $*"; }

# 只扫我们自己的代码，跳过 vendor（如果存在）和 .git
SRC_DIRS=(cmd internal)
files() { find "${SRC_DIRS[@]}" -name '*.go' -not -path '*/vendor/*' 2>/dev/null; }

say "== 1. 代码格式 =="
unformatted=$(gofmt -l "${SRC_DIRS[@]}" 2>/dev/null)
if [ -n "$unformatted" ]; then
  bad "以下文件没跑 gofmt："
  printf '%s\n' "$unformatted" | sed 's/^/      /'
else
  ok "gofmt 干净"
fi

say "== 2. 编译与静态检查 =="
if go build ./... 2>&1 | sed 's/^/      /'; then ok "go build 通过"; else bad "go build 失败"; fi
if go vet ./... 2>&1 | sed 's/^/      /'; then ok "go vet 通过"; else bad "go vet 失败"; fi

say "== 3. 禁止直读环境变量（只有 internal/config 可以） =="
hits=$(files | grep -v '^internal/config/' | xargs -r grep -nHE 'os\.(Getenv|LookupEnv|Environ)\(' 2>/dev/null)
if [ -n "$hits" ]; then
  bad "以下位置直接读了环境变量，应改为从 config.Config 取："
  printf '%s\n' "$hits" | sed 's/^/      /'
else
  ok "没有越权读环境变量"
fi

say "== 4. 禁止 handler 直接写响应（必须返回 (any, error)） =="
hits=$(find internal/handler -name '*.go' 2>/dev/null | xargs -r grep -nHE '(c|ctx)\.(JSON|JSONP|Send|SendString|SendStatus|Status|Write|Render)\(' 2>/dev/null)
if [ -n "$hits" ]; then
  bad "handler 里出现了直接写响应的调用："
  printf '%s\n' "$hits" | sed 's/^/      /'
else
  ok "handler 没有绕过统一渲染"
fi
hits=$(find internal/handler -name '*.go' 2>/dev/null | xargs -r grep -nHE '"net/http"' 2>/dev/null | grep -v 'StatusNotFound\|http\.Status' )
if [ -n "$hits" ]; then
  bad "handler 不应 import net/http（状态码请用 apperr）："
  printf '%s\n' "$hits" | sed 's/^/      /'
else
  ok "handler 没有 import net/http"
fi

say "== 5. 禁止 mongo 类型泄漏到适配器之外 =="
hits=$(files | grep -v '^internal/store/mongo/' | grep -v '^internal/store/open.go$' \
  | xargs -r grep -nHE 'go\.mongodb\.org/mongo-driver' 2>/dev/null)
if [ -n "$hits" ]; then
  bad "只有 internal/store/open.go 和 internal/store/mongo/** 能 import mongo 驱动："
  printf '%s\n' "$hits" | sed 's/^/      /'
else
  ok "mongo 类型没有泄漏"
fi

say "== 6. 禁止 bson 出现在 internal/store 顶层包 =="
hits=$(find internal/store -maxdepth 1 -name '*.go' 2>/dev/null | xargs -r grep -nHE 'bson\.|primitive\.' 2>/dev/null)
if [ -n "$hits" ]; then
  bad "internal/store 的接口层出现了 bson 类型："
  printf '%s\n' "$hits" | sed 's/^/      /'
else
  ok "接口层没有 bson"
fi

say "== 7. 禁止用 fmt.Print* 打日志（用 log/slog） =="
hits=$(files | grep -v '_test\.go$' | xargs -r grep -nHE 'fmt\.Print(f|ln)?\(' 2>/dev/null)
if [ -n "$hits" ]; then
  bad "请改用 slog，fmt 只用于拼接字符串："
  printf '%s\n' "$hits" | sed 's/^/      /'
else
  ok "没有 fmt.Print 日志"
fi

say "== 8. 每个包都必须有测试文件 =="
missing=""
for d in $(find internal -type d | sort); do
  if ! ls "$d"/*_test.go >/dev/null 2>&1; then
    # 纯类型定义的包可以豁免，但要在这里显式列出来
    case "$d" in
      internal|internal/model|internal/store|internal/handler|internal/platform) continue ;;
    esac
    missing="$missing $d"
  fi
done
if [ -n "$missing" ]; then
  bad "以下包还没有 *_test.go ：$missing"
else
  ok "包测试文件齐全"
fi

say ""
if [ "$fail" -eq 0 ]; then
  say "全部通过 ✅"
  exit 0
else
  say "存在违规，先修再继续 ❌"
  exit 1
fi
