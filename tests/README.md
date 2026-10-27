# tests/ 说明（怎么跑冒烟 / 集成 / 生产只读）

四层测试的完整方案见 `docs/testing.md`，这里只写**操作步骤**与前置条件。

## 0. 前置：本地 mongod

```bash
# 项目约定用官方 tarball 起本地实例（/tmp 下，重启机器后需要重新 fork）
mkdir -p /tmp/aria-mongo/db
/tmp/aria-mongo/mongodb-linux-x86_64-ubuntu2404-8.0.14/bin/mongod \
  --dbpath /tmp/aria-mongo/db --port 27017 --fork \
  --logpath /tmp/aria-mongo/mongod.log --bind_ip 127.0.0.1
```

没有 mongod 时：`go test ./...` 仍然全过（集成测试会 skip），但冒烟测试与 `go run ./cmd/server` 起不来。

## 1. 准备数据 + 起服务

```bash
go run ./cmd/seeddev          # 灌固定种子（幂等；只允许 *_dev / *_test 库）
go run ./cmd/server           # 127.0.0.1:2333；自动用 .env.dev + config.local.yml
```

种子账号：`seedadmin` / `seed-admin-password`（拿 JWT 用）：

```bash
TOKEN=$(curl -s -X POST localhost:2333/auth/login -H 'Content-Type: application/json' \
  -d '{"user":"seedadmin","password":"seed-admin-password"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["token"])')
```

## 2. 冒烟（`smoke.py`）

只验契约形状（信封、状态码、错误码），不验业务。

```bash
python3 tests/smoke.py                     # 公开接口 + 鉴权 + stub + 旧路径 404 + CORS
python3 tests/smoke.py --token "$TOKEN"    # 顺带验证鉴权链路
python3 tests/smoke.py --base https://...  # 打别的地址
```

## 3. 集成测试（`-tags=integration`）

需要真实 mongod；用独立测试库，跑完自清理。

```bash
export MONGODB_URI_TEST=mongodb://127.0.0.1:27017
export TEST_DB_NAME=AriaBlogNext_test
go test -tags=integration ./...
```

未设置 `MONGODB_URI_TEST` 时集成测试会 skip（`go test ./...` 在任何机器都能跑）。

## 4. 生产只读对比（`compare.py`）

连**线上库但只能读**，与线上旧后端逐字段比对。这是切换前的最后一关。

```bash
# 终端 A：只读实例（readOnly=true 是代码里的硬闸门，写接口一律 503）
ENV_FILE=.env.production CONFIG_FILE=./config.prod-readonly.yml go run ./cmd/server

# 终端 B：逐字段比对
python3 tests/compare.py
```

结果记号：`✓` 一致 / `⚠` 刻意修正（归档时区、标签排序）/ `✗` 真实差异（必须清零）。

> **铁律**：云端（生产）库绝不可写。`compare.py` 只调只读接口；
> 种子/回填等写工具会强制校验库名带 `_dev` / `_test` 后缀。

## 5. 其它验证命令

```bash
bash scripts/checks.sh                            # 架构守卫
go test ./... -coverprofile=coverage.out && go tool cover -func=coverage.out | tail -1
go test ./internal/handler -run TestGolden        # 契约快照（改契约才用 -update）
go gofmt -l cmd internal                          # 应为空
python3 tests/smoke.py                            # 冒烟
```

## 6. 跨服务回放（`glue_replay.py`，需要 glue 实例在跑）

验证「旧契约 → glue → 新后端」整条链路（不只是各自单测）。glue 用假上游的 golden 测不到
「真实新后端」的响应细节，这个脚本补的就是这一段。

```bash
# 1) 起新后端（dev）
ENV_FILE=.env.dev go run ./cmd/server            # :2333

# 2) 起 glue（另一个仓库），上游指向 2333，端口错开避免冲突
#    ⚠️ 对方仓库可能在改：先 go build 新鲜产物再起，别复用旧进程
#    （踩过：旧二进制让「generate=false」看起来没生效，白排查半天）
cd ../ariablognext-backend-glue
go build -o /tmp/glue-fresh ./cmd/server
PORT=2335 UPSTREAM=http://127.0.0.1:2333 /tmp/glue-fresh

# 3) 回放（默认打 :2335，可传第一个参数换地址）
cd ../ariablognext-backend-go
python3 tests/glue_replay.py
```

覆盖：20 条只读旧接口的**旧格式形状**（`message/count/totalCount/time` 等）、
写接口全链路（文章增改删、说说增改删、友链建组/加链/改/移/删、申请提交与查询）、
`type=full` 带 `mdContent`、`plainContent` 截断 ≤201、`postNavigation` 的 previous/next、
图床未配置时不死 500。

注意：
- 脚本用纳秒时间戳做唯一后缀，可重复跑；**每次会留下一条 pending 申请**（dev 库，
  申请没有删除接口，属正常残留；需要清就 decide 掉让它进保留期）。
- 同一 IP 的限流（login 10/10min、apply 5/30min）是**进程内**计数：连跑多次会被 429，
  重启 dev 后端即可重置。脚本把 apply 的 429 视为「限流生效」的正向结果。
- 旧契约里 `moveFlinkGroup` 的 body 是 **snake_case**（`from_group/to_group/link_id`），
  不是新接口的 camelCase——回放时必须用旧字段名，否则会被 glue 原样转发导致 400。
