# 设计要点（草稿 v0.1）

配套 `api-draft.md`。这里说的是「怎么保证规则不被违反」，不是「大家记得遵守」。

---

## 1. 统一返回格式：做成编译期约束

靠 code review 保证格式统一，迟早会漏。三层硬约束：

### 1.1 handler 拿不到 ResponseWriter

```go
// internal/app/handler.go
type Handler func(*Ctx) (any, error)
```

`Ctx` 只暴露**读**能力：

```go
type Ctx struct {
    req     *fiber.Ctx   // 私有
    RequestID string
    User    string
    ctx     context.Context
}
func (c *Ctx) Query(key string, def ...string) string
func (c *Ctx) Int(key string, def, min, max int) (int, error)
func (c *Ctx) Bind(dst any) error
func (c *Ctx) Context() context.Context   // 带超时的请求上下文
```

没有 `JSON()`、没有 `Status()`、没有 `Send()`。**写不出别的格式。**

### 1.2 唯一的渲染点

```go
func (a *App) Render(c *Ctx, data any, err error) error
```

- `err == nil` → `{"ok":true,"requestId":…,"data":…,"meta":…}`
- `err` 是 `*apierr.Error` → 信封 + 对应 HTTP 状态
- `err` 是其它 → `internal.error`，**原始错误串写进 `error.details.cause`** 并同时进日志
- 特殊响应走 `app.Raw{ContentType, Body}`（sitemap XML），仍由同一个渲染器写出

`details` 的取值规则（对象形态固定，见 `api-draft.md §0.2`）：

| 场景 | details |
|---|---|
| 字段校验 | `{"fields":[{"field","message"}]}` |
| 内部错误 | `{"cause":"<error 原文>"}` |
| 上游错误 | `{"cause":"…","upstream":"siliconflow","status":50602}` |

个人项目优先排查效率，所以真实原因**返回给客户端**，只有栈信息不回。这是刻意选择，
换成对外产品时把 `cause` 挪进日志即可（一个配置项开关）。

### 1.3 静态检查 + 注册表测试

- `golangci-lint` 的 `forbidigo` 禁止 `internal/handler/**` 出现 `c.JSON(`、`c.Send(`、`c.Status(`、`http.Error(`
- `forbidigo` 同时禁止 `internal/store/**` 之外的包 import `go.mongodb.org/mongo-driver/**`
- 一条测试遍历路由注册表，断言每个路由的 handler 类型是 `app.Handler`（防止有人绕过包装直接挂 `fiber.Handler`）

---

## 2. 数据抽象层

### 2.1 不做通用查询抽象

现有代码深度依赖 Mongo 语义（位置投影 `{"links.$":1}`、`$push/$pull + upsert`、
`$unwind/$group/$addToSet`、`$regex/$options`）。若接口长成 `Find(filter any)`，
换库等于重写所有 handler，抽象层白做。

**接口按业务能力定义**，返回值是领域模型，参数是过滤器结构体：

```go
type PostRepository interface {
    Count(ctx context.Context) (int64, error)
    List(ctx context.Context, f PostFilter) (Page[Post], error)
    BySlug(ctx context.Context, slug string) (*Post, error)
    Slugs(ctx context.Context) ([]string, error)
    Neighbours(ctx context.Context, slug string) (newer, older *PostBrief, err error)
    Related(ctx context.Context, slug string, limit int) ([]PostBrief, error)
    Create(ctx context.Context, p Post) error
    UpdateMeta(ctx context.Context, slug string, m PostMeta) error
    UpdateContent(ctx context.Context, slug string, c PostContent) error
    Delete(ctx context.Context, slug string) error
}
```

### 2.2 四条硬规则

1. **`store` 包不出现任何 bson / mongo 符号**（`depguard` 兜底）。领域模型只用
   `string/int64/bool/[]string/time.Time`。
2. **复合写下沉为仓储原子方法**。现有「友链移组」是 `push 新组 → pull 旧组 → 失败回滚`
   三步组合，留在 service 里换到 SQL 就无法用事务。接口直接给 `MoveLink(ctx, from, to, id)`。
3. **边界转换只在适配器里做**：`ObjectID ↔ string`、`Date ↔ time.Time`、`null ↔ 可选类型`。
4. **可选能力拆小接口**：`RawExporter`（全库导出）、`FullTextSearcher`（以后接 PG 的 `tsvector`）。
   别的数据库不实现就不注册对应路由。

### 2.3 驱动选择

```go
// internal/config
DBType string // env DB_TYPE=mongo（默认）| postgres | sqlite
```

```go
// internal/store/store.go
func Open(cfg config.Config) (Store, error) {
    switch cfg.DBType {
    case "mongo":    return mongostore.Open(cfg)
    // case "postgres": return pgstore.Open(cfg)
    default:         return nil, fmt.Errorf("不支持的 DB_TYPE %q", cfg.DBType)
    }
}
```

service 与 handler 只依赖 `store.Store` 接口。加一个数据库 = 新增一个适配器包 + 一个 case。

### 2.4 现有库结构结论（已连线上库逐集合检查）

**结论：结构够清晰，可以保留，不做数据迁移。** 只做增量清理：

| 动作 | 内容 | 状态 |
|---|---|---|
| 删集合 | `Keep`（0 文档，历史遗留）| ✅ 已删，备份 `/tmp/aria-keep-backup-20261026-225016.json` |
| 加索引 | `Speaks.time` 倒序 | ✅ 已加（`time_-1`）|
| 加索引 | `FLinkApplications` 的 `status`/`email` | ⏸ 按你的意见不动（只有 33 条）|
| 加索引 | `Users.user` 唯一 | ⏸ 待确认（只有 1 条，加了更安全）|
| 加字段 | `Posts.searchText` / `Drafts.searchText`（见 §2.5）| ⏸ 需一次性回填 83 篇 |
| 删字段 | `Posts.cachedHtml`、`Drafts.cachedHtml` | ⏸ 代码不再读写，字段本身最后再清 |
| 修排序 | `tag`/`category` 列表改按 `publishTime` | 代码层面修，不动数据 |
| 补默认 | `FLinks.order` 缺失按 0 | 代码层面修，不动数据 |

字段层面一致性良好：`slug` 全部存在、`publishTime` 全是 int、`tags` 无空数组、
`Speaks.time` 120/120 全有且无重复。只有 `Drafts` 有 4/8 条缺
`mdContent/plainContent/wordCount`（后台新建草稿不写正文），模型用可空类型即可。

> 澄清一处容易看错的表述：`Speaks` 缺的是 **`time` 索引**，不是 `time` 字段。
> 字段一直有（说说时间就是它），只是按 `time` 排序/删除时走全表扫。现在索引补上了。

### 2.5 搜索为什么要加字段

现状：`searchPosts` 把 83 篇的 `mdContent` **全量**（平均 8.5 KB、最长 140 KB、总计约 700 KB）
拉进内存，再用一条又长又可疑的正则剥 Markdown，然后字符串查找。慢、费内存、费流量，而且：

- `plainContent` 在库里被**截断到 201 字**（70/83 篇正好 201 字符），拿它搜等于只搜开头一段
- 那条剥 Markdown 的正则里含 `$(.*?)$`（与 `^$` 无关的行尾元字符），行为不可预测

改法：

1. 写入时（`create` / `update content`）额外维护 `searchText`：
   标题 + 全文纯文本，转小写、压空白、去 Markdown 标记与 HTML 标签，不截断
2. 查询只投影 `slug/title/publishTime/bannerImg/searchText`，`keyword` 经 `QuoteMeta` 转义
3. 标题命中优先于正文命中（两次查询分别 `title` 精确子串 / `searchText` 子串，然后合并去重）
4. `context` 从 `searchText` 定位取前后 50 字
5. 一次性回填：`go run ./cmd/backfill-searchtext`（幂等，只处理缺失或过期的文档）

83 篇的规模下正则扫描完全够用，所以**不引入全文索引**；但接口保持在 `Searcher` 抽象后面，
以后要换 Atlas Search 或 PG `tsvector` 只换实现。

### 2.6 语义搜索的预留位（占位，不实现）

留这个 stub 的唯一目的：**以后加功能时不用再动一次对外契约**。

```go
// internal/store/store.go
type Searcher interface {                       // 现在就有，关键词搜索
    Search(ctx context.Context, f SearchFilter) (Page[Post], error)
}

type SemanticSearcher interface {               // 预留，Mongo 适配器暂时不实现
    SemanticSearch(ctx context.Context, q SemanticQuery) ([]SemanticHit, error)
}

var ErrNotImplemented = errors.New("semantic search not implemented")
```

- 路由 `GET /posts/semantic?query=&limit=` 现在就注册，固定返回
  `501 search.not_implemented`（文案：「语义搜索还没做，先用关键词搜索」）
- 数据侧预留：`Posts.embedding []float32` + `Posts.embeddingMeta{model, dim, updatedAt}`，
  以及 `cmd/build-embeddings`（现在只打印「未配置，跳过」）
- 配置预留：`EMBEDDING_MODEL`、`EMBEDDING_API`（缺省即功能关闭）
- 真做的时候：`mongostore` 里实现 `SemanticSearch`（Atlas Vector Search 的 `$vectorSearch`），
  关键词搜索保持不变，两者可并存并用 RRF 融合

成本极低（一个接口 + 一个路由 + 三个字段），以后能省一次契约变更。

---

## 3. 配置：一个 `config.yml`，缺就崩

**程序不直接读 `os.Getenv`。** 环境变量只在「解析占位符」这一处出现，
所有配置项——包括限流阈值、body limit、时区、分页上限、功能开关——都写进仓库根目录的
`config.yml`。看一眼文件就知道这个服务被配成了什么样。

### 3.1 形态

```yaml
database:
  type: mongo
  uri: "{{ env.MONGODB_URI }}"     # 敏感值：占位符
  name: "{{ env.DB_NAME }}"

media:
  enabled: true                    # 开关显式化
  s3:
    bucket: "{{ env.S3_BUCKET }}"
```

- 非敏感值直接写在文件里（站点名、限流阈值、超时、CORS、分页上限…）
- 敏感值一律 `{{ env.XXX }}`，**只写占位符，不写真实值**
- `config.yml` 本身可以进 git（它不含任何密钥）

### 3.2 加载流程

```go
//go:embed config.yml
var builtin []byte

func Load() (*Config, error) {
    raw := builtin
    if p := os.Getenv("CONFIG_FILE"); p != "" {   // 唯一允许直读的 env
        b, err := os.ReadFile(p); if err != nil { return nil, … }
        raw = b
    }
    var tree map[string]any
    if err := yaml.Unmarshal(raw, &tree); err != nil { return nil, err }

    tree, missing, err := resolveEnv(tree)        // 收集所有缺失的 env，一次性报全
    if err != nil { return nil, err }

    out, err := yaml.Marshal(tree)
    var cfg Config
    dec := yaml.NewDecoder(bytes.NewReader(out))
    dec.KnownFields(true)                          // 拼错字段名立刻报错
    if err := dec.Decode(&cfg); err != nil { return nil, err }

    return &cfg, cfg.Validate()
}
```

`resolveEnv` 只替换**整串匹配** `{{ env.NAME }}` 的字符串值（不做文本内插），
这样密码里有引号、冒号、换行也不会破坏 YAML 结构。

### 3.3 `go:embed` 而不是运行时读文件

Vercel 的 Go 函数只带编译产物，运行时不一定有 `config.yml`。用 `go:embed` 编进二进制，
本地和线上行为一致；代价是「改配置要重新部署」——在 Vercel 上本来就是这样。

想临时换配置本地调试：`CONFIG_FILE=./config.local.yml go run ./cmd/server`
（`config.local.yml` 进 `.gitignore`）。

### 3.4 校验规则

| 类别 | 规则 | 违反时 |
|---|---|---|
| 必填 | `database.uri` / `database.name` / `auth.jwtSecret` / `site.url` / `site.name` | 启动失败 |
| 占位符 | 引用了 `{{ env.X }}` 但 `X` **未定义** → 按空处理（2026-10-27 语义修订：EMBEDDING_API_KEY 这类预留占位符不再逼着每个环境定义）；必填性由「必填校验 + enabled 开关」保证，校验失败时错误里列出全部未定义名 | 启动失败 / 通过 |
| 依赖 | 某子系统 `enabled: true` 但它的密钥有空值 | 启动失败，报「media.enabled=true 但 S3_BUCKET 为空」|
| 互斥 | `features.semanticSearch=true` 但 `ai.embedding.enabled=false` | 启动失败 |
| 未知 | 出现 `Config` 里没有的字段 | 启动失败（`KnownFields`）|
| 取值范围 | 超时 > 0、`maxPageSize >= defaultPageSize`、`maxFullItems > 0` | 启动失败 |

错误一次性列出**全部**问题，不是报一个改一个。

> `enabled: false` 是**显式关闭**：对应路由根本不注册（404），
> 而不是注册了再返回 503。半开半关的状态不存在。

### 3.5 硬约束

- `forbidigo` 禁止 `internal/config` 以外的包出现 `os.Getenv` / `os.LookupEnv` / `os.Environ`
- 日志里输出配置时走 `cfg.Redacted()`：所有来自占位符的字段打码成 `***`，
  只有非敏感字段原样显示
- 敏感字段在 `Config` 上标注 `secret:"true"` tag，`Redacted()` 据此判断，
  新增配置项时不会忘记

### 3.6 环境变量清单（只出现在 `{{ env.* }}` 里）

| 变量 | 用途 |
|---|---|
| `MONGODB_URI` | 数据库连接串 |
| `DB_NAME` | 库名，默认 `AriaBlogNext` |
| `SECRET` | JWT 签名 + 服务间门牌（`X-Internal-Token`）|
| `S3_ENDPOINT` `S3_ACCESSKEYID` `S3_SECRETACCESSKEY` `S3_REGION` `S3_BUCKET` `S3_PUBLIC_URL` | 图床（R2 用 `us-east-1`）|
| `SMTP_HOST` `SMTP_PORT` `SMTP_SECURE` `SMTP_USER` `SMTP_PASS` `SMTP_FROM` | 邮件 |
| `GH_TOKEN` `GITHUB_REPO` | 友链初筛 / 链路检查 |
| `SF_TOKEN` | 摘要生成 |
| `FLINK_ADMIN_EMAIL` | 申请人提醒抄送 |
| `EMBEDDING_API_KEY` | 语义搜索（暂时空着）|
| `CONFIG_FILE` | 唯一直读的 env 之一，指定配置文件路径 |
| `ENV_FILE` | 唯一直读的 env 之一，指定 dotenv 文件（默认 `.env.dev` → `.env`）；生产只读验证时用 `ENV_FILE=.env.production` |
| `PORT` | 由 `server.listen` 为空时在 `internal/config` 内部读取，Vercel 注入 |

`.env` 只给本地用（`godotenv` 在 `cmd/` 里加载），Vercel 走 Environment Variables。

---

## 4. 并发与数据库访问

| 关注点 | 做法 |
|---|---|
| 连接池 | 单例 `*mongo.Client`，`MaxPoolSize=20`、`MinPoolSize=0`、`MaxConnIdleTime=5m`（Vercel 单实例并发有限，池子不用开大）|
| 请求超时 | 默认 `server.requestTimeout`（8s）派生 `context.WithTimeout`，超时映射 `504 timeout.exceeded`；`timeoutOverrides` 给导出（120s）、摘要（90s）、图床转存（30s）单独放宽——否则扫全库和等模型必被 8 秒砍掉 |
| 取消传播 | 客户端断开 → `fiber.Ctx.Context()` 取消 → mongo 操作自动中断，不空转 |
| 投影 | 列表一律显式投影，禁 `mdContent`；禁用 `to_list(length=None)` 拉全表 |
| 计数 + 列表 | 一次请求返回 `data + meta.count`；`count` 与 `find` 用 `errgroup` 并行；`countOnly=true` 时**完全跳过 find** |
| 写竞态 | 依赖 unique index + `IsDuplicateKeyError`，不做「先查后写」 |
| 冷启动 | 首次请求才建连接，`sync.Once` 保证只建一次 |

---

## 5. 时区

- 库里全存 unix 秒，本身无歧义
- **所有「年/月」划分固定用 `Asia/Shanghai`**：`import _ "time/tzdata"` + `time.LoadLocation`，
  不依赖容器 `TZ`（现在还依赖，等于靠环境）
- **不在 Mongo 里分桶**：`/archives/list` 只取 `publishTime` 一列（83 篇 ≈ 几百字节），在 Go 里分组。
  这样 `$toDate`（UTC）与本地时间月界的不一致被彻底消灭，换库也不受影响
- `sitemap` 的 `lastmod`、`LinkApplication.createdAt` 的 ISO 输出同理

---

## 6. 限流与体积

见 `api-draft.md §0.7`。实现要点：

- `internal/httpx/middleware/ratelimit`：`golang.org/x/time/rate` 的 per-key limiter，
  map 超过 5000 条时顺带清理过期项（沿用旧实现思路，避免无限增长）
- 命中返回 `429` + `Retry-After`，`error.code = rate_limit.exceeded`
- body limit 中间件同时看 `Content-Length` 和实际读入字节数（防止分块传输绕过）
- 平台侧还有 Vercel 的 4.5 MB 硬上限，超了请求进不到函数——文档里明写，别让用户以为是我们的 bug

---

## 7. 测试

| 层 | 做法 |
|---|---|
| 单元 | `store` 的 `fake` 内存实现 → handler / service 直接测，不连库 |
| 契约 | 以线上旧后端为基准生成 `testdata/*.json` 快照，新接口做深比较（忽略时间戳、随机字段）|
| 集成 | `go test -tags=integration`，连**本地独立测试库**（见下）|

测试库：

- 本机没有 docker/mongod，需要先装一个（`apt install mongodb-org` 或解压 tarball 起
  `mongod --dbpath /tmp/aria-mongo`）
- 配置 `MONGODB_URI_TEST=mongodb://127.0.0.1:27017`、`TEST_DB_NAME=AriaBlogNext_test`
- `TestMain` 里建库、造 `__test_` 前缀的固定数据，跑完 `DropDatabase`；**绝不连生产库**
- 若某天不想装 mongod，退路是同一 Atlas 集群上的独立库名 `AriaBlogNext_test`（但默认不用）

---

## 8. Go 项目规范速览

- 标准布局：`cmd/`（可执行入口，每个 main 一个目录）+ `internal/`（外部不可 import）
- 包名小写单词、不用下划线；导出函数必须有注释
- 错误处理：`if err != nil { return ... }` 早返回；用 `%w` 包装链路；
  对外错误用自定义 `*apierr.Error` 携带 `code`；`message` 写人话，
  原始 `err.Error()` 只进 `details.cause`，**不塞进 `message`**
- 依赖注入：`main` 里构造 config → store → repository → service → handler → app，
  全程显式传参，**不用全局变量**（旧后端 `db.py` 的单例在这里变成一个显式的 `*store.Mongo`）
- 日志：`log/slog` 结构化输出，字段含 `requestId`、`method`、`path`、`status`、`ms`
- 接口返回切片时用空切片而非 `nil`（`[]T{}` 而不是 `nil`），避免 JSON 出 `null` 破坏前端 `data.map`

---

## 9. 胶水项目定位（`ariablognext-backend-glue`）

独立仓，一个很小的 HTTP 服务，职责只有一件：**实现旧 72 条契约，转调新后端**。

- 只做参数换算（`startl/endl → page/pageSize`）与响应重排（`{ok,data} → {message:"success",...}`、
  `error → {detail:{message:"fail",error:…}}`、图片上传的 `data.data.links.url` 嵌套）
- 不含任何业务逻辑，**不连数据库**
- 前端 / dashboard 把 `backEndUrl` 指到胶水域名即可，代码零改动
- 它的真正作用是「对齐测试」：跑通后可以按域逐个把前端从胶水切到新后端，切完一个域就删掉胶水里对应路由，
  全切完胶水项目退役
