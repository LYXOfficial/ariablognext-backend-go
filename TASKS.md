# 施工清单 · ariablognext-backend-go

> 你（下一个模型）要把这份清单**从上到下**做完。不要跳阶段，不要一次改十个文件。
> 每个阶段末尾都有「验收命令」，跑不过就不要进入下一阶段。

---

## 0. 你在做什么

用 **Go + Fiber v3 + MongoDB** 重写个人博客后端，部署在 **Vercel**（Go Framework Preset）。
前端（blog / dashboard）这一阶段**完全不改**，由另一个独立项目 `ariablognext-backend-glue`
做旧接口的兼容翻译。

本仓库只实现**新接口**，旧路径一条都不要有。

必读（按顺序）：

| 文件 | 作用 |
|---|---|
| `docs/api-draft.md` | **接口契约**。55 条路由、参数、返回、错误码。这是唯一事实来源 |
| `docs/store-interfaces.md` | 存储层接口。签名照抄 |
| `docs/design-notes.md` | 为什么这么设计（配置、时区、并发、抽象层）|
| `docs/testing.md` | 测试方案。每层测试写什么用例 |
| `config.yml` | 唯一的配置入口 |

---

## 1. 十条禁令

**违反任何一条都会被要求重做。**

1. **不要改 `docs/api-draft.md` 里的契约。** 觉得有问题就停下来在回复里说明，不要自己改字段名/路径/错误码。
2. **不要用 `os.Getenv`**，除非在 `internal/config` 里。所有配置从 `config.Config` 取。
3. **handler 里不许写响应。** 签名固定 `func(*app.Ctx) (any, error)`，不许出现 `c.JSON`、`c.Status`、`net/http`。
4. **`internal/store` 顶层（接口层）不许出现 `bson`、`primitive`、`mongo-driver`。** 只有 `internal/store/mongo/**` 和 `internal/store/open.go` 能 import 驱动。
5. **不要用全局变量传依赖。** 全部在 `cmd/server/main.go` 里显式串起来。
6. **不要连生产库做写操作。** 只读验证可以（`MONGODB_URI` 指向线上），但任何 `insert/update/delete` 只能打在测试库上。
7. **不要 `git commit` / `git push`。** 用户自己提交。
8. **不要引入清单外的依赖。** 见 §3。
9. **不要跳过测试。** `scripts/checks.sh` 和 `go test ./...` 必须常绿。
10. **卡住超过 20 分钟就停下来问。** 把「我试了什么、报什么错、卡在哪」写清楚，不要瞎猜着改契约。

---

## 2. 命令速查

```bash
# 依赖
go mod tidy

# 本地跑（监听 127.0.0.1:2333；Vercel 上用 $PORT）
go run ./cmd/server

# 静态守卫（架构约束）
bash scripts/checks.sh

# 单元测试
go test ./...

# 单测 + 覆盖率
go test ./... -coverprofile=coverage.out && go tool cover -func=coverage.out | tail -1

# 集成测试（需要先起测试库，见 docs/testing.md §4.1）
go test -tags=integration ./...

# 冒烟（服务要跑着）
python3 tests/smoke.py
```

> **环境文件（三个都不进 git）**
>
> | 文件 | 用途 |
> |---|---|
> | `.env.dev` | 日常开发：本机 mongod + `AriaBlogNext_dev` + 一次性随机 SECRET + 外部服务全空 |
> | `.env.production` | 生产值留档，**本地开发绝对不要用** |
> | `config.local.yml` | 本地配置变体：`media`/`mail`/`flink.screening`/`ai.summary` 全部 `enabled: false` |
>
> `cmd/server` 必须按 **`.env.dev` → `.env`** 的顺序找第一个存在的文件加载（用 `godotenv.Load`），
> 然后 `config.Load()` 从 `.env.dev` 里的 `CONFIG_FILE=./config.local.yml` 取配置路径。
> **线上不设 `CONFIG_FILE`**，走 `config.yml` 全开的版本。
>
> ⚠️ `.env.production` 里的 `MONGODB_URI` 指向**线上库**。任何时候都不要把它的
> `DB_NAME` 从 `AriaBlogNext` 换成别的来「顺手本地跑一下」——要连线上库只许只读。
>
> 两种「缺」要分清：变量**没定义** = 启动失败；变量**定义为空** = 允许，由 `enabled` 开关决定。

**每次改完至少跑 `bash scripts/checks.sh && go test ./...`。**

---

## 3. 依赖（照这个版本，不要自己升级）

```
github.com/gofiber/fiber/v3            v3.6.1
go.mongodb.org/mongo-driver/v2         v2.9.2
github.com/golang-jwt/jwt/v5           v5.3.1
gopkg.in/yaml.v3                       v3.0.1
github.com/joho/godotenv               latest    # 只在 cmd/server 里加载 .env
github.com/aws/aws-sdk-go-v2/...       latest    # 图床（P3 阶段才加）
github.com/wneessen/go-mail            latest    # SMTP 隐式 TLS（P3 阶段才加）
```

⚠️ **`go.mod` 里 `go` 指令不能低于 `1.26.0`**，因为 fiber v3.6.1 要求 1.26，mongo v2.9.2 要求 1.25。
如果 Vercel 报 Go 版本不支持，**不要改到 1.24**，而是把 fiber 降到 v3.5.x 再 `go mod tidy`，
并在回复里说明。

---

## 4. 阶段 P0 · 骨架能跑

**目标**：`go run ./cmd/server` 起来，`GET /health` 和 `GET /posts/list` 能返回正确信封。

### 4.1 要创建的文件

| 文件 | 内容 | 关键签名 |
|---|---|---|
| `internal/config/config.go` | 读 `config.yml`（`go:embed`）、替换 `{{ env.X }}`、校验、脱敏。**同时负责加载 dotenv**：先看 `ENV_FILE`（显式指定），否则依次试 `.env.dev`、`.env`；之后再读 `CONFIG_FILE` | `func Load() (*Config, error)`、`func MustLoad() *Config`、`func (c *Config) Location() *time.Location`、`func (c *Config) Redacted() map[string]any` |
| `internal/config/config_test.go` | 见 `docs/testing.md §2.1` | |
| `internal/config/duration.go` | `type Duration time.Duration` + `UnmarshalYAML`（支持 `8s`/`120s`/`5m`）| |
| `internal/apperr/codes.go` | `type Code string` + 全部错误码常量 + `var httpStatus map[Code]int` | 错误码表逐条对齐 `api-draft.md §0.3` |
| `internal/apperr/error.go` | 错误类型 | `func New(code Code, msg string, details any) *Error`、`func Wrap(code Code, msg string, err error) *Error`、`func (e *Error) Error() string`、`func (e *Error) Unwrap() error` |
| `internal/apperr/error_test.go` | 见 `testing.md §2.2` | |
| `internal/model/post.go` | `Post`、`PostBrief`、`PostMeta`、`PostContent`、`CategoryCount`、`TagCount`、`ArchiveBucket` | 字段名 = 数据库字段名，驼峰 |
| `internal/store/errors.go` | `ErrNotFound`、`ErrDuplicate` | |
| `internal/store/filter.go` | `Page[T]`、`PostFilter`、`DraftFilter`、`ListFilter` 等 | 见 `store-interfaces.md §1` |
| `internal/store/store.go` | 全部接口 | 见 `store-interfaces.md` |
| `internal/store/open.go` | 按 `cfg.Database.Type` 选择实现 | `func Open(ctx context.Context, cfg *config.Config) (Store, error)` |
| `internal/store/fake/store.go` | 内存实现（先把所有方法都写出来，未用到的返回零值）| |
| `internal/store/mongo/store.go` | 连接、`Ping`、`Close`、`ensureIndexes`；**每个写方法第一行 `if r.ro { return store.ErrReadOnly }`**（`r.ro` 来自 `cfg.Database.ReadOnly`）| |
| `internal/store/mongo/post.go` | `Posts()` 的最小实现：`Page`、`BySlug`、`Slugs` | |
| `internal/app/meta.go` | 分页元信息 | `type PageSize struct{ N int; Full bool }`、`type Meta struct{…}`、`func NewListMeta(...)`、`func NewCountMeta(...)` |
| `internal/app/ctx.go` | handler 的上下文（**只有读能力**）| `func (c *Ctx) Query(name string) string`、`QueryDefault(name, def)`、`RequiredQuery(name) (string, error)`、`Int(name string, def, min, max int) (int, error)`、`Bool(name string, def bool) (bool, error)`、`PageSize() (PageSize, error)`、`Bind(dst any) error`、`Context() context.Context`、`IP() string` |
| `internal/app/app.go` | 组装 fiber、中间件、路由注册、`Test()` 辅助 | `func New(cfg *config.Config, st store.Store) *App`、`func (a *App) Fiber() *fiber.App`、`func (a *App) Test(req *http.Request) (*http.Response, error)`、`func (a *App) Routes() []Route` |
| `internal/app/render.go` | **唯一的响应出口** | `func (a *App) Render(...)`，成功/失败两种信封结构体分开写 |
| `internal/app/routes.go` | `type Route struct{ Method, Path string; Auth Auth; Handler Handler }`、`func (a *App) Add(r Route)` | |
| `internal/app/middleware/*.go` | `recover.go`、`requestid.go`、`cors.go`、`logger.go`、`timeout.go` | |
| `internal/handler/register.go` | **一个文件列出全部 55 条路由** | `func Register(a *app.App, cfg *config.Config, st store.Store, svc *service.Set)` |
| `internal/handler/misc.go` | `GET /health` | |
| `internal/handler/posts.go` | 先只写 `list` / `detail` | |
| `cmd/server/main.go` | 入口：加载配置 → 开存储 → 建 app → 注册路由 → 监听 `$PORT`（默认 2333）| |

### 4.2 关键实现要求

**统一信封**（`internal/app/render.go`）——两个结构体，不要用一个带 omitempty 的：

```go
type successBody struct {
    OK        bool   `json:"ok"`
    RequestID string `json:"requestId"`
    Data      any    `json:"data"`
    Meta      *Meta  `json:"meta,omitempty"`
}

type errorBody struct {
    OK        bool      `json:"ok"`
    RequestID string    `json:"requestId"`
    Error     errorItem `json:"error"`
}

type errorItem struct {
    Code    string `json:"code"`
    Message string `json:"message"`
    Details any    `json:"details"`   // 没有就显式写 null，不要 omitempty
}
```

**错误处理**：`Render` 收到 `error` 时判断是不是 `*apperr.Error`：

- 是 → 用它的 code / message / details / HTTP 状态
- 不是 → `internal.error` + `details = {"cause": err.Error()}` + 500

**分页**（`internal/app/meta.go`）：

```go
// PageSize.Value() 返回 int 或字符串 "full"
meta := NewListMeta(page, ps, count, hasMore)  // {page, pageSize, count, hasMore}
meta := NewCountMeta(count)                     // 只有 count
```

`pageSize` 是 `any` 类型，值可能是 `16`（数字）或 `"full"`（字符串），
测试里要断言这两种形态。

**health 接口**要真的 ping 数据库：

```json
{"ok":true,"requestId":"…","data":{"status":"ok","db":"ok","version":"<build sha 或 dev>","time":1761400000}}
```

DB ping 失败 → 503 + `internal.error`。

**mongo 连接**（注意 v2 和 v1 的 API 不一样，别写错）：

```go
opts := options.Client().ApplyURI(cfg.Database.URI).
    SetMaxPoolSize(uint64(cfg.Database.Pool.Max)).
    SetMinPoolSize(uint64(cfg.Database.Pool.Min)).
    SetMaxConnIdleTime(cfg.Database.Pool.IdleTimeout.Std())

client, err := mongo.Connect(opts)          // ⚠️ v2 不接收 ctx！
if err != nil { return nil, err }
if err := client.Ping(ctx, nil); err != nil { return nil, err }   // Ping(ctx, *readpref.ReadPref)
```

**查询结果用 `cursor.All(ctx, &out)`**，不要 `for cursor.Next()`。

**毫秒/秒**：库里是 unix **秒**，`int64`。不要用 `time.Time` 存。

### 4.3 P0 验收

```bash
go mod tidy
bash scripts/checks.sh          # 必须全绿（此时允许「包测试文件齐全」这条暂时豁免，见脚本注释）
go test ./...
go run ./cmd/server &
curl -s localhost:2333/health | head -c 300
curl -s 'localhost:2333/posts/list?pageSize=2' | head -c 400
python3 tests/smoke.py          # [1] 里 posts 相关的几条应该过，其他会挂——这是预期的
```

- `GET /get/post/postsInfo` 必须返回 **404**（证明旧路径没被偷偷保留）

---

## 5. 阶段 P1 · 只读域全量（27 条公开 GET）

**目标**：`python3 tests/smoke.py` 的 `[1] [2] [4] [5] [6]` 全过。

### 5.1 新增文件

| 文件 | 内容 |
|---|---|
| `internal/model/{draft,speak,link,application,user,site,page}.go` | 其余领域模型 |
| `internal/service/post.go` | 列表/详情/归档/分类/标签的业务编排；分页上限、`full` 的 300 条上限在这里拦 |
| `internal/service/search.go` | 关键词搜索：转义、长度限制、标题优先、context 片段 |
| `internal/textutil/{markdown,searchtext,wordcount}.go` | 剥 Markdown、生成 `searchText`、中英字数 |
| `internal/service/link.go` | 友链列表/计数/取色 |
| `internal/service/theme.go` | 头像取色：SSRF 校验、8s 总时限、3 次重定向、512KB、64×64 色彩分桶 |
| `internal/handler/{taxonomy,links,speaks,site,misc,applications}.go` | 对应路由 |
| `internal/store/mongo/{draft,speak,link,application,site,summary,taxonomy}.go` | 对应仓储 |
| `internal/store/fake/*.go` | 同步补全 fake 实现 |

### 5.2 最容易写错的三处

**① 归档分桶（时区）**

```go
// 只取 publishTime 一列，在 Go 里按 loc 分组
// ❌ 不要在 MongoDB 里用 $toDate + $year/$month：那是 UTC，和「哪个月」的本地语义不一致
```

**② 搜索**

```go
// 投影只取这几个字段，不要把 mdContent 拉出来
projection := bson.M{"slug": 1, "title": 1, "publishTime": 1, "bannerImg": 1, "searchText": 1}
keyword := regexp.QuoteMeta(strings.TrimSpace(f.Keyword))   // 必须转义
if len(f.Keyword) > cfg.Search.MaxKeywordLength { 报 validation.invalid_field }
```

标题命中优先：先按 `title` 查一次、再按 `searchText` 查一次，合并去重（标题命中的在前），
或者查一次然后在 Go 里给标题命中的加权。两种都行，但**要有测试证明顺序对**。

**③ 分类/标签列表的排序**

旧代码排序字段写错成不存在的 `date`（等于没排序）。新实现**必须**按 `publishTime` 倒序。

### 5.3 P1 验收

```bash
bash scripts/checks.sh && go test ./... && python3 tests/smoke.py
```

---

## 6. 阶段 P2 · 鉴权与写接口

**目标**：带真实 JWT 能调通全部后台接口。

### 6.1 新增文件

| 文件 | 内容 |
|---|---|
| `internal/auth/jwt.go` | HS256 签发/校验；claims 用 `sub`/`iat`/`exp` |
| `internal/service/auth.go` | 登录：sha256(password) 比对、失败计数、锁定 |
| `internal/handler/auth.go` | `/auth/login`、`/auth/verify` |
| `internal/app/middleware/auth.go` | 三种级别：Public / Admin（Bearer）/ Internal（`X-Internal-Token`）|
| `internal/handler/{drafts}.go` | 草稿 7 条 |
| `internal/service/{draft,speak,site}.go` | |
| `internal/store/mongo/user.go` | `ByCredentials` |
| `internal/app/middleware/ratelimit.go` | 全局 + 按路由的限流 |
| `internal/app/middleware/bodylimit.go` | 默认 / markdown / 图片三档 |

### 6.2 注意

- **密码算法不变**：`sha256(password)` 的小写 hex，与库里现有数据一致。不要加盐、不要换 bcrypt。
- JWT 不要求兼容旧 token（旧 token 可以失效），但 claims 结构保持一致，方便以后排查。
- 写接口的并发安全靠 **unique index + `mongo.IsDuplicateKeyError`**：

```go
err := coll.InsertOne(ctx, doc)
if mongo.IsDuplicateKeyError(err) {
    return apperr.New(apperr.PostSlugConflict, "这个 slug 已经有文章了", nil)
}
```
**不要**先 `CountDocuments` 再 `InsertOne`（那是竞态，旧代码就是这么错的）。

### 6.3 P2 验收

```bash
# 起服务后
TOKEN=$(curl -s -X POST localhost:2333/auth/login -H 'Content-Type: application/json' \
  -d '{"user":"…","password":"…"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["token"])')
python3 tests/smoke.py --token "$TOKEN"
```

---

## 7. 阶段 P3 · 图床与友链申请

### 7.1 新增文件

| 文件 | 内容 |
|---|---|
| `internal/platform/s3.go` | R2/S3：上传、覆盖、删除、列目录。**所有调用放在 goroutine + 超时里**，因为 aws-sdk 是阻塞的 |
| `internal/platform/smtp.go` | 隐式 TLS（465）与 STARTTLS（587）两种 |
| `internal/platform/github.go` | `repository_dispatch` 触发初筛 / 触发链路检查 |
| `internal/platform/llm.go` | SiliconFlow 摘要生成（60s 超时，错误带 `upstream` 字段）|
| `internal/validate/flink.go` | 申请校验：XSS 正则、URL、邮箱、颜色白名单 |
| `internal/service/application.go` | 申请全流程：校验 → 限流 → 队列容量 → 判重 → 入库 → 触发初筛 → 发邮件 |
| `internal/handler/images.go` | 5 条图片接口 |
| `internal/handler/applications.go` | 补全 5 条申请接口 |
| `internal/store/mongo/{application}.go` | 补全 |

### 7.2 业务规则（照 `flink/main.py` 的语义，不要自己发挥）

| 规则 | 值 |
|---|---|
| 待审队列上限 | 20 |
| 同 IP 限流 | 5 次 / 30 分钟 |
| 已出结果保留 | 14 天（提交时顺带清理）|
| 判重 | 归一化后**逐字段完全相同**才算重复：trim、URL 去尾斜杠、颜色缺省 `#888888`、邮箱小写 |
| 修改申请内容无变化 | 400 `application.no_change` |
| 修改申请目标不存在 | 404 `link.not_found` |

### 7.3 P3 验收

本地 `.env` 没有 `S3_*`，所以图床接口应该返回 **503 + `upstream.failed`**（明确说「图床未配置」），
而不是 panic 或 500。邮件同理：`mail.enabled=true` 但密钥不全时，**启动就失败**
（由配置校验负责，不是运行时才报错）。

---

## 8. 阶段 P4 · 收尾

| 文件 | 内容 |
|---|---|
| `internal/handler/misc.go` 补 `POST /maintenance/export` | 走 `store.RawExporter`，没有实现时返回 501 |
| `internal/handler/semantic.go` | `GET /posts/semantic` 固定 501 + `search.not_implemented` |
| `cmd/backfill-searchtext/main.go` | 给所有 Posts/Drafts 补 `searchText`；幂等；支持 `--dry-run` |
| `cmd/build-embeddings/main.go` | 现在只打印「未配置，跳过」并退出 0 |
| `internal/store/mongo/index.go` | 幂等建索引：`Posts.slug`(unique)、`Posts.publishTime`、`Drafts.slug`(unique)、`PostSummary.slug`(unique)、`Speaks.time`、`webSiteInfo.key`(unique)、`Users.user`(unique) |
| `testdata/golden/*.json` | 契约快照，见 `docs/testing.md §5` |
| `tests/README.md` | 怎么跑冒烟/集成测试 |

**`backfill-searchtext` 不许默认写入**：必须显式 `--apply` 才写，默认 `--dry-run` 只打印会改多少条。

### 8.1 生产只读验证（必做的最后一步）

```bash
ENV_FILE=.env.production CONFIG_FILE=./config.prod-readonly.yml go run ./cmd/server
python3 tests/compare.py        # 与线上旧后端逐字段比对
```

- `config.prod-readonly.yml` 已存在：`database.readOnly: true`，四个外部子系统全关
- 每个写方法开头的 `store.ErrReadOnly` 判断是**硬闸门**，不许省
- `compare.py` 输出里 `✗` 必须清零；`⚠` 是刻意修正（归档时区、标签排序）
- 详见 `docs/testing.md §4.5`

---

## 9. 每个阶段的通用验收

```bash
bash scripts/checks.sh          # 必须 0 退出
go test ./...                   # 必须全过
go vet ./...                    # 无输出
gofmt -l cmd internal           # 无输出
```

到 P1 之后再加：

```bash
python3 tests/smoke.py
```

---

## 10. 遇到这些情况，停下来问（不要猜）

1. 契约里某个字段的语义你不确定（比如 `meta.count` 到底算不算过滤）
2. 某个旧行为的细节在 `docs/` 里没写清（比如邮件模板文案）
3. 依赖下载失败 / Vercel 构建报错，重试两次仍不行
4. 你发现契约本身有矛盾
5. 需要访问生产数据库做写操作

提问格式：**「我在做 X，预期 Y，实际 Z，我试过 A/B，怀疑是 C」**。

---

## 11. 交付前检查表

- [ ] `bash scripts/checks.sh` 全绿
- [ ] `go test ./...` 全过，覆盖率达标（`docs/testing.md §6`）
- [ ] `go mod tidy` 之后 `go.sum` 没有意外变更
- [ ] 55 条路由全部注册，路径与 `docs/api-draft.md §2` 逐条核对过
- [ ] 旧后端路径（`/get/*`、`/update/*`、`/access/*`、`/flink/*`）全部 404
- [ ] `GET /posts/content` 是 404（这个接口已废弃）
- [ ] 所有错误响应都有 `error.details` 键（哪怕是 `null`）
- [ ] 删除类接口返回 200 + `{"deleted":true}`，不是 204
- [ ] 配置缺 `{{ env.X }}` 时启动直接失败，错误信息里有变量名
- [ ] 仓库里没有任何真实密钥（`git grep -nE 'mongodb\+srv://|sk-[A-Za-z0-9]{20}'` 应为空）
- [ ] 没有 `git commit`（留给用户）
