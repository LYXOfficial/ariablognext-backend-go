# 测试方案（照做即可）

> 目标：**任何人不看代码也能判断这次改动是不是坏的**。
> 五层测试，每一层都能单独跑，任何一层挂了都不许往下走。

```
第 1 层  静态守卫   bash scripts/checks.sh            几十毫秒，不需要数据库
第 2 层  单元测试   go test ./...                     几百毫秒，用 fake 存储
第 3 层  冒烟测试   python3 tests/smoke.py            需要服务跑起来
第 4 层  集成测试   go test -tags=integration ./...   需要本地测试库
第 5 层  生产只读   python3 tests/compare.py          连生产库（只读）对比旧后端
```

---

## 0. 一次性准备

```bash
# 依赖（只装这一次）
go mod tidy

# 本地服务（第 3 层要用）。本机已有 .env.dev 与 config.local.yml（都不进 git）
go run ./cmd/server        # 监听 127.0.0.1:2333

# 换一台机器没有这两个文件时：
#   cp .env.example .env.dev       然后按需填（库名必须是 *_dev，不能碰线上库）
#   cp config.yml config.local.yml 然后把 media/mail/screening/summary 的 enabled 改成 false
```

---

## 1. 静态守卫（`scripts/checks.sh`）

已写好，不需要改。它检查：

| 检查 | 为什么 |
|---|---|
| `gofmt -l` | 格式统一，避免 review 时噪音 |
| `go build` / `go vet` | 编译期问题 |
| 禁止 `os.Getenv` 出现在 `internal/config` 之外 | 配置只能从 `config.yml` 走 |
| 禁止 handler 里 `c.JSON` / `c.Status` / `net/http` | 统一信封是硬约束 |
| 禁止 mongo 类型泄漏到适配器外 | 数据抽象层不被污染 |
| 禁止 `internal/store` 顶层出现 `bson.` | 同上 |
| 禁止 `fmt.Print*` | 日志统一走 `slog` |
| 每个 `internal/` 子包都有 `*_test.go` | 防止「只写实现不写测试」 |

**它挂了就修代码，不要改脚本。**

---

## 2. 单元测试（`go test ./...`，不需要数据库）

### 2.1 `internal/config`

| 用例 | 输入 | 期望 |
|---|---|---|
| 正常解析 | 合法 YAML + 齐全 env | `Load()` 无错，字段值正确 |
| 占位符替换 | `uri: "{{ env.X }}"`，X=`abc` | `cfg.Database.URI == "abc"` |
| 缺 env | 引用 `{{ env.NOT_SET }}` | 报错，且**错误信息里包含变量名** |
| 多个缺失 | 同时缺 2 个 | **一次报全**，不是只报一个 |
| 未知字段 | 多写一个 `foo: 1` | 报错（KnownFields 生效）|
| 功能开关缺密钥 | `media.enabled=true` 但 bucket 为空 | 报错，信息里写明是哪个开关、缺哪个 key |
| 脱敏 | 调 `Redacted()` | 输出里**不含**任何密钥原文，密钥位置是 `***` |
| 时区 | `server.timezone: Asia/Shanghai` | `cfg.Location` 可用，且不是 `time.Local` |

### 2.2 `internal/apperr`

| 用例 | 期望 |
|---|---|
| 每个 Code → HTTP 状态 | 与 `docs/api-draft.md §0.3` 完全一致（表驱动，一个 Code 一行）|
| `Wrap(code, msg, err)` | 保留 code，`Unwrap()` 能取到原错误 |
| 普通 error 进渲染器 | 变成 `internal.error`，`details.cause` 是原错误串 |
| 未知 Code | 兜底 500 + `internal.error`，不能 panic |

### 2.3 `internal/app`（信封渲染）

这是最关键的包，测试必须覆盖：

| 用例 | 期望 |
|---|---|
| 成功 + data | `{"ok":true,"requestId":"…","data":…}`，**没有** `meta` 键 |
| 成功 + 列表 | 有 `meta`，且 `meta.count` 是数字 |
| `countOnly` | `data: null` + `meta` 只有 `count`，**没有** `page/pageSize/hasMore` |
| `pageSize=full` | `meta.pageSize` 是字符串 `"full"` |
| 失败 | `{"ok":false,…,"error":{code,message,details}}`，**没有** `data` 键 |
| `data` 为 nil | 仍然输出 `"data": null`，键不能丢 |
| 空切片 | 输出 `[]` 而不是 `null`（用 `[]T{}` 初始化）|
| `requestId` | 透传到响应里 |
| 404 兜底 | 访问未知路径 → `not_found.route` + 404 |
| panic | 中间件 recover 后返回 `internal.error` + 500，进程不退出 |
| 路由表 | `app.Routes()` 数量 == 55，且路径集合与 `docs/api-draft.md` 一致（写死一份列表来比）|

### 2.4 `internal/textutil`

| 函数 | 用例 |
|---|---|
| `WordCount` | 纯中文、纯英文、中英混排、空串、代码块里的英文 |
| `CleanMarkdown` | 标题/粗体/斜体/行内代码/围栏代码/图片/链接/引用/列表/表格/HTML 标签/分隔线 各一条 |
| `BuildSearchText` | 结果全部小写、不含 Markdown 符号、不含 HTML、**不截断**（长度 > 201）|

> `CleanMarkdown` 的期望值要写死成字符串常量，不要用「再跑一遍同样的正则」来验证。

### 2.5 `internal/validate`（友链申请校验）

照 `flink/main.py` 的规则搬，至少这些用例：

| 输入 | 期望 |
|---|---|
| 正常申请 | 通过 |
| `name` 为空 | 400 + `validation.missing_field` |
| `link` 不带协议 | 400 + `validation.invalid_field` |
| `link` 含 `<script` | 406 语义（本项目用 `validation.invalid_field` + 中文说明）|
| `color` = `red; }` | 400 |
| `color` = `#f08` / `rgb(1,2,3)` / `red` | 通过 |
| `type=1` 缺 `targetUrl` | 400 |
| `type=2` | 400 |
| 名称 51 字 | 400 |

### 2.6 `internal/auth`

| 用例 | 期望 |
|---|---|
| 签发 → 校验 | 通过，`sub` 正确 |
| 过期 token | `auth.invalid_token` |
| 改一个字符 | `auth.invalid_token` |
| 缺 `Bearer ` 前缀 | 也接受裸 token（兼容旧行为），或明确只接受 Bearer——二选一但要有测试 |
| 空 token | `auth.missing_token` |

### 2.7 `internal/handler`（用 fake 存储 + `app.Test`）

**每个 handler 至少四个用例**：正常、缺参、未授权、不存在。

写一个公共辅助函数，避免重复：

```go
func do(t *testing.T, app *App, method, path string, body any, token string) (int, map[string]any)
```

至少覆盖：

| 接口 | 必测 |
|---|---|
| `/posts/list` | 默认返回 data+meta.count；`countOnly` 只给 count；`pageSize=full` 超过 300 条报 400 |
| `/posts/detail` | 正常；`post.not_found` |
| `/posts/create` | 正常 201；`post.slug_conflict` 409 |
| `/posts/update` 缺 slug | 400 |
| `/posts/navigation` | newer/older 方向正确（这是旧接口的老 bug 点，务必用固定数据验证）|
| `/posts/related` | 无标签返回 `[]` 不是 404 |
| `/posts/semantic` | 固定 501 + `search.not_implemented` |
| `/links/applications/apply` | 正常；重复网址 409；队列满 429；限流 429 |
| `/links/applications/decide` | approved 新增；approved 修改；rejected 带理由/不带理由 |
| `/media/upload`（`/images/upload`）| 正常；类型不支持 415；超大 413 |
| `/auth/login` | 正确密码 200 + token；错误密码 403；连续失败触发锁定 |
| `/maintenance/export` | 无内部 token → 401；有 → 200 且 `data.collections` 是对象 |
| 任意路由 | 不带 token 访问后台接口 → 401 `auth.missing_token` |

---

## 3. 冒烟测试（`tests/smoke.py`，需要服务在跑）

已经写好。它只验证**契约形状**，不验证业务：

- 12 条公开只读接口：2xx、有 `ok/requestId/data`、无 `error`
- 5 条参数校验/已删接口：返回预期错误码（其中 `GET /posts/content` 必须是 404，证明接口真的删了）
- 7 条后台接口：不带 token 必须 401 且错误码是 `auth.*`
- `/posts/semantic` 必须 501
- 三条**旧后端路径**必须 404（证明新服务没有偷偷保留旧路径）
- CORS 预检必须有 `Access-Control-Allow-Origin`

```bash
go run ./cmd/server &
python3 tests/smoke.py
python3 tests/smoke.py --token "$(你的JWT)"   # 顺带验证鉴权链路
```

---

## 4. 集成测试（`go test -tags=integration ./...`）

### 4.1 测试库

**绝不连生产库。** 两个方案，优先第一个：

```bash
# 方案 A：本机 mongod（推荐）
mongod --dbpath /tmp/aria-mongo --port 27017 --fork --logpath /tmp/aria-mongo.log
export MONGODB_URI_TEST=mongodb://127.0.0.1:27017
export TEST_DB_NAME=AriaBlogNext_test
```

```bash
# 方案 B：本机没有 mongod / docker 时，用同一 Atlas 集群但**不同库名**
export MONGODB_URI_TEST="$MONGODB_URI"       # 与生产同一个集群
export TEST_DB_NAME=AriaBlogNext_test        # 库名不同，物理隔离
```

没有设置 `MONGODB_URI_TEST` 时，集成测试必须 **`t.Skip`** 而不是失败
（这样 `go test ./...` 在任何机器上都能跑）。

### 4.2 用例要求

每个仓储方法一个测试，且必须**自己造数据、跑完清理**：

```go
func TestMain(m *testing.M) {
    // 建连接 → 用随机库名或 TEST_DB_NAME → 跑测试 → db.Drop() → 退出
}
```

必须覆盖的「实现细节容易错」的点：

| 点 | 期望 |
|---|---|
| `PostFilter.Year/Month` | 传 `Asia/Shanghai`，跨月边界的两篇文章归属正确（这是旧后端的时区 bug 点）|
| `ArchiveBuckets` | 用上海时区分组，结果倒序 |
| `Page` 的 `Count` | 是全量过滤后的数量，不是本页长度 |
| `CountOnly` | 返回的 `Items` 为 nil，且**不执行** find（可以用一个会失败的投影来验证）|
| `Full` | 超过 300 条时报 `validation`（在 service 层拦，仓储不管）|
| `Create` 重复 slug | 返回 `store.ErrDuplicate` |
| `MoveLink` | 源组减少、目标组增加、总数不变；目标组不存在时自动创建 |
| `MoveLink` 失败回滚 | 用故意失败的条件验证源组数据没丢 |
| `FindByURL` | `https://x.com` 与 `https://x.com/` 都能命中 |
| `SetLatency` | 只更新指定 id，返回受影响条数 |
| `Application.TTL` | `PurgeDecidedBefore` 只删已出结果且超期的，pending 不删 |
| 索引 | 启动后 `Posts.slug` 有 unique 索引（`ensureIndexes` 幂等，跑两次不报错）|

---

## 4.5 生产只读验证（第 5 层，最接近真实）

mock 和造的数据永远测不出存量数据的坑：`plainContent` 被截断到 201 字、
`FLinks.order` 缺字段、`coverFit` 是 null、83 篇里 1 篇还残留 `cachedHtml`。
所以必须有一条**连生产库但绝对写不进去**的通路。

### 4.5.1 只读是怎么强制的

不是靠约定，是代码里每个写方法的第一行：

```go
func (r *postRepo) Create(ctx context.Context, p model.Post) error {
    if r.ro { return store.ErrReadOnly }   // r.ro 来自 database.readOnly
    ...
}
```

`config.prod-readonly.yml` 把 `database.readOnly` 设成 `true`，
service 层拿到 `store.ErrReadOnly` 后统一变成 `503 storage.read_only`。
**就算脚本误调了写接口，也写不进去。**

### 4.5.2 怎么跑

```bash
# 终端 A：用生产变量文件 + 只读配置起服务
ENV_FILE=.env.production CONFIG_FILE=./config.prod-readonly.yml go run ./cmd/server

# 终端 B：和线上旧后端逐字段对比
python3 tests/compare.py
```

`ENV_FILE` 是**引导变量**（和 `CONFIG_FILE` 一样），只在 `internal/config` 里读一次。
这样生产值永远待在 `.env.production`，不会被写进 `.env.dev`。

### 4.5.3 结果怎么读

| 记号 | 含义 |
|---|---|
| ✓ | 一致 |
| ⚠ | **刻意的语义修正**，允许不同：归档分桶从 UTC 改成 `Asia/Shanghai`；标签/分类从「按不存在的 `date` 排序」改成按 `publishTime` |
| ✗ | 真实差异，必须查清才能切换 |

`compare.py` 里有 FAIL 就说明新实现和线上行为不一致，**不要切流**。

### 4.5.4 铁律

- 只读验证**只能用 `config.prod-readonly.yml`**，不许为了「改个数据看看」去改 `readOnly`
- 不许把 `.env.production` 复制成 `.env` 来跑——那就绕过了所有开关
- 要验证「写接口真的被拦住」，用 `go test` 里对 `store.ErrReadOnly` 的单元测试，
  不要拿生产库试

---

## 5. 契约快照（防字段漂移）

`testdata/golden/` 下每个接口一个 JSON 文件，内容是**固定的假存储数据**渲染出的完整响应。

```bash
go test ./internal/handler -run TestGolden -update   # 首次生成/确实要改契约时
go test ./internal/handler -run TestGolden           # 平时跑，必须逐字节一致
```

规则：

1. golden 文件里**不要有会变的值**（时间戳用固定常量、requestId 固定写成 `"test"`）
2. 只有明确决定改契约时才 `-update`，并在 commit message 里写清改了哪个字段
3. `-update` 之后要人工 diff 一遍，确认没有意外字段消失

---

## 6. 覆盖率

```bash
go test ./... -coverprofile=coverage.out
go tool cover -func=coverage.out | tail -1
```

要求（不追求 100%，但低于这个数说明测试是凑数的）：

| 包 | 最低 |
|---|---|
| `internal/app` | 90% |
| `internal/handler` | 80% |
| `internal/service` | 80% |
| `internal/textutil`, `internal/validate` | 90% |
| `internal/store/mongo` | 只算集成测试，不看数字 |

---

## 7. 常见误区（会浪费时间的那种）

- ❌ 直接 `t.Skip` 掉难写的用例 → 那等于没写
- ❌ 用 `map[string]any` 断言整个响应体 → 字段改名照样过；要断言**具体字段**
- ❌ 测试之间共享数据 → 用 `t.Cleanup` 或每个测试独立库/集合
- ❌ 用 `time.Now()` 断言时间字段 → 用 `time.Equal` 或注入固定时钟
- ❌ 连生产库跑集成测试 → 见 §4.1
- ❌ 改 golden 文件来让测试通过 → 只有在**确实改了契约**时才允许
