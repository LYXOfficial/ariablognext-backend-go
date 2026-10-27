# 施工坑记录（随做随记）

> 给下一轮 AI / 新同学。每条都写「现象 → 原因 → 解法」，别再踩第二遍。

## 2026-10-27 凌晨施工批次

### 1. `store.Open` 与 mongo 适配器互相 import 成环

- **现象**：`internal/store/open.go` 按 `cfg.Database.Type` 选实现，直接 import
  `internal/store/mongo`，而后者要 import `internal/store` 拿哨兵错误 → import cycle。
- **解法**：驱动注册表模式。`store` 包暴露 `RegisterDriver(name, fn)`；
  mongo 适配器在自己的 `init()` 里注册；`cmd/server` blank import
  `internal/store/mongo` 触发注册。文档里「open.go 唯一允许 import 驱动」的
  表述按此理解为「open.go 是唯一的选择点」。
- **教训**：接口包 → 适配器包的单向依赖决定了「选择器」只能靠注册表或依赖注入。

### 2. mongo-driver v2 的 options API 与 v1 完全不同

- **现象**：照 v1 习惯写 `options.Find().SetX()` 混进 `FindOptionsMerger` 类型报 undefined。
- **解法**：v2 的 `Find` 收 `opts ...options.Lister[options.FindOptions]`，
  多个 `options.Find()` builder 直接并列传即可（builder 自带 `List()`）；
  upsert 是 `options.UpdateOne().SetUpsert(true)`（不是 v1 的 `options.Update()`）。
- **教训**：v2 命令行选项全部走「builder + Lister 泛型」，抄 v1 代码必炸。

### 3. `IndexModel.Keys` 是 `any`

- **现象**：测试里 `m.Keys.String()` 编译失败。
- **解法**：断言前先 `.(bson.D)` 类型断言。

### 4. go:embed 无法引用包目录之外的文件

- **现象**：`config.yml` 在仓库根，`internal/config` 想 `//go:embed ../../config.yml` 不允许
  （embed pattern 禁止 `..`），symlink 也不行（embed 不跟随 symlink）。
- **解法**：把 `config.yml` 正身在 `internal/config/` 下，仓库根放指向它的 symlink
  （`config.yml -> internal/config/config.yml`）。文档里的根路径继续有效，git 正常跟踪。
- **教训**：embed 的文件必须物理存在于包目录内；symlink 放在 embed 引用方是不行的，放在「用户入口」没问题。

### 5. checks.sh 的 handler 禁令连测试文件一起扫

- **现象**：§4 禁止 `internal/handler` 出现 `"net/http"` import，但 handler 的单测
  天然要用 `http.NewRequest` 构造请求 → 测试文件也会被拦。
- **解法**：在 `internal/app` 上加 `TestReq(method, path, body, header) (status int, body []byte, err error)`
  测试助手，net/http 只出现在 `internal/app`；handler 测试全程不碰 net/http。
- **教训**：静态守卫扫的是文件不是包语义，测试代码也受同一套架构约束。

### 6. 本机没有 mongod（Arch Linux）

- **现象**：`.env.dev` 指向 `mongodb://127.0.0.1:27017` 但本机无 mongod、无 docker。
- **解法**：直接下载官方 ubuntu2404 tarball 到 `/tmp/aria-mongo/`（v8.0.14），
  `--dbpath /tmp/aria-mongo/db --port 27017 --fork` 起本地实例。
  对 Go 驱动来说它就是个标准 mongod，与发行版无关。
- **注意**：这是 /tmp 里的临时实例，重启机器后要重新起（解压目录还在，直接重新 --fork 即可）。

### 7. fake 存储 `ExportAll` 的形状

- **现象**：`RawExporter` 返回 `map[string][]json.RawMessage`（集合 → 文档数组），
  直接 `json.Marshal` 整个集合只会得到一个 []byte。
- **解法**：先 marshal 再 `Unmarshal` 成 `[]json.RawMessage` 拆开。 mongo 版导出的
  `_id` 字符串语义，fake 也要复现，否则单测过了线上还是崩。

### 8. fiber v3 的 CORS 中间件拒绝「allowOrigins=["*"] + allowCredentials=true」

- **现象**：`New()` 一启动就 panic（`[CORS] Configuration error: When 'AllowCredentials' is set
  to true, 'AllowOrigins' cannot contain a wildcard origin '*'`）。
  config.yml 里恰好就是这对组合。
- **原因**：浏览器本来就会拒绝「通配 origin + 凭证」的响应，fiber v3 干脆在配置期拦截。
- **解法**：`middleware.Cors` 里检测到通配 origin 时把 credentials 降级为 false。
  现状前后端都不用 cookie 凭证，配置文件保持原样；真要启用凭证必须先把 origins
  收敛成白名单，别把这里的降级逻辑改回去。

### 9. `apperr.From` 与 handler panic 的 cause

- **现象**：panic 被安全网接住后如果只走 `apperr.From`，`details.cause` 只会是
  "handler panic" 这个哨兵串，排查时看不到 panic 值本身。
- **解法**：Render 里先 `errors.As(err, &panicError)`，用 panic 值构造
  `apperr.Wrap(InternalError, ..., fmt.Errorf("%v", pe.r))`。测试断言 cause == panic 原文。

## 硬性规则（用户明令，任何阶段都适用）

- **云端（生产/Atlas）数据库绝对不可以写入。** 连云端库只有一条合法通路：
  `ENV_FILE=.env.production CONFIG_FILE=./config.prod-readonly.yml go run ./cmd/server`
  （readOnly 硬闸门）。任何脚本、任何集成测试、任何「顺手验证」都不得把
  .env.production 的连接串用于写操作，也不得改库名伪装。
- 云端凭证（SF_TOKEN / S3_* / GH_TOKEN / SMTP_*）只允许用于对应外部服务本身
  （摘要生成 / 图床上传 / workflow 触发 / 发邮件），与数据库无关。
- SMTP 测试邮件统一发到 lyx2010official@outlook.com，不要发给别人。

## 2026-10-27 凌晨（续）· 搜索行为（用户追加要求）

- **特殊字符搜索**：搜索词一律 `QuoteMeta` 按字面处理，`.*[](){}|?+` 不当正则；
  多关键词按空白分隔、AND 匹配（`SplitKeywords`，mongo/fake 两实现同一语义）。
- **Markdown 结构不进搜索索引**：外挂标签是 MDX 风格 `<Folding summary="…">…</Folding>`，
  `CleanMarkdown` 的 removeHTMLTags 会把整个标签（含属性文字）剥掉——
  搜 "fold" 只命中正文里的 fold，不会把所有带 Folding 外挂的文章都翻出来。
  改 CleanMarkdown 时必须保留这条性质（有专项测试）。

### 10. latestUpdateTime 的语义修正与过渡期耦合（用户明定）

- **目标态**：`latestUpdateTime` 只有胶水层写入；新 API `POST /site/update-time`
  支持自定义时间戳（body `{"time": unix秒}`，缺省 = 当前时间）；
  「写时间戳 + 顺带通知刷缓存」的耦合最终要删除，dashboard 将来单独请求。
- **过渡期（行为严格不变）**：胶水必须**暂时保留** refreshCache/siteinfo 的通知耦合——
  新后端的 `/site/update-time` 本身**不再**发通知（已按此实现，service.SiteService）。
  迁移期间的链路：dashboard → 胶水（写时间戳 + 通知 blog.yaria.top/refreshCache/siteinfo）
  → 新后端只负责落库。
- 已通知胶水协调会话该事实。

## P3 阶段坑（云端真实验证时踩到）

### 11. `_id` 空字符串会让插入绕过 ObjectID 生成

- **现象**：`InsertOne(model.LinkApplication{ID:""})` 后 `InsertedID` 是 `""`（string），
  类型断言 panic；后续 `ByID`/`Decide` 全部对不上，库里留下 `_id:""` 的脏记录。
- **原因**：结构体有 `bson:"_id"` 字段时，空串被驱动当成「显式主键」写入。
- **解法**：`applicationRepo.Create` 显式构造 `bson.M`（不含 `_id`），让驱动生成 ObjectID；
  返回 `InsertedID.(bson.ObjectID).Hex()`。插入成功后不要在校验/返回路径再 panic 一次。

### 12. ObjectID 解码成 string 需要 BSONOptions

- **现象**：`status` 接口报 `error decoding key _id: decoding an object ID into a string is not supported`。
- **解法**：client options 设 `SetBSONOptions(&options.BSONOptions{ObjectIDAsHexString: true})`。
  接口层 ID 一律 string（store-interfaces §0），没有这个选项就解不进来。

### 13. SiliconFlow 从本机很慢（Qwen3 带 reasoning）

- 最小请求实测 47s，正常摘要 11s+；`config.yml` 的 60s 超时会偶发超时（与 design-notes §16 描述一致）。
- 本地端到端验证用临时配置放大到 180s（`/tmp/aria-cloud-config.yml`），**不改仓库 config.yml**。
- 生产上这是既有的上游特性，错误已正确映射 `upstream.failed` + `details.upstream=siliconflow`。

### 14. TASKS 与契约的状态码冲突：图床未配置

- TASKS P3 验收写「503 + upstream.failed」，但 api-draft §0.3 错误码表规定
  `upstream.failed` = **502**（503 是 `storage.read_only`）。
- **以契约（api-draft）为准**：实现返回 502 + `upstream.failed`，文案「图床未配置，请联系站长启用」。
  design-notes §3.4 说「enabled=false 就不注册路由」，但那样前端只看到 404，排障更差；
  因此图床路由**始终注册**、按未配置给 502（已与用户确认过语义，施工清单验收为准）。

### 15. 邮件/初筛/图床关闭时的降级行为（已实测）

- `mail.enabled=false` 或 SMTP 不全 → 申请照常成功，`mailSent:false`；
- `flink.screening.enabled=false` → `screeningTriggered:false`，不影响入库；
- 有凭证时实测：SMTP 发信成功、GitHub repository_dispatch 成功、R2 上传/列出/删除均通。

### 16. 邮件模板必须逐字复刻旧版，不许自创

- **现象**：第一版用了简化卡片布局，用户一眼看出「样式和原后端不一致」。
- **解法**：`internal/platform/mailtemplates.go` 逐字复刻 `flink/mail.py`：
  同一套色值（主题 #ef859d / 浅 #f79dbc / 标题 #c5343e / 正文 #4c4948 / 灰 #8a8a8a /
  背景 #f7f9fe / 描边 #f2e4e9）、粉色渐变头部、`role=presentation` 表格布局、
  14px 圆角友链卡片（42px 圆形头像 + 主题色链接 + 描述）、浅粉 note 块（左侧 4px 主题色竖线）、
  22px 胶囊按钮、虚线页脚。四类邮件（回执/通过/未通过/博主提醒）的标题与正文文案也照抄。
- **验证方式**：用云端 SMTP 把四类邮件真实发到 lyx2010official@outlook.com 人工核对。
- **注意**：`fmt.Sprintf` 模板里的 CSS 百分号必须写 `%%`；用户输入一律 `html.EscapeString`。

## P4 阶段坑

### 17. fisrt 框架 BodyLimit 会先于我们的信封拦掉大请求

- **现象**：上传 4MB+ 的图片，返回的是 fasthttp 的纯文本 `body size exceeds the given limit`，
  不是统一信封（测试直接报 i/o timeout）。
- **解法**：`fiber.Config.BodyLimit` 设成 6MB（比最大档位 4MB 宽），
  真正的按路由判定在 `CheckBodySize`（返回 `image.too_large` / `validation.invalid_field` 信封）。
  生产 Vercel 还有 4.5MB 平台硬上限，超了进不到函数——文档里已写明，不是我们的 bug。

### 18. `fiber.App.Test` 默认 1 秒超时

- **现象**：涉及 S3 重试的 handler 测试报 `i/o timeout`，看起来像死锁。
- **解法**：`TestReq` 里显式传 `fiber.TestConfig{Timeout: 10 * time.Second}`。

### 19. 摘要生成的错误优先级

- **现象**：文章不存在时先报「摘要服务未启用（502）」，而不是 `post.not_found（404）`。
- **解法**：先查文章存在性，再判断 LLM 是否启用。错误码语义要准，否则前端没法区分「文章没了」和「上游挂了」。

### 20. 表格分隔行 `|:--|--:|` 不是 `---`

- **现象**：CleanMarkdown 把 `|:--|--:|` 当成正文留下 `:-- --:`。
- **解法**：`isTableSeparator` 只认 `|`、`:`、`-`、空格组成的行，不再依赖 `strings.Contains("---")`。

### 21. `MoveLink` 少了 upsert 会**丢数据**（集成测试抓到的）

- **现象**：把友链移到「不存在的目标组」时：`$push` 匹配 0 条**不报错**，紧接着 `$pull`
  却真的从源组删掉了该链接 → 链接凭空消失，接口还返回成功。
- **原因**：`update_one({"name": toGroup}, {$push: …})` 没带 `upsert: true`；
  Mongo 的 update 对「匹配不到」是静默成功。
- **解法**：`options.UpdateOne().SetUpsert(true)`（契约与 docs/testing.md §4.2 都要求
  「目标组不存在时自动创建」），pull 失败再回滚 push。
- **教训**：`update_one`/`delete_one` 一定要看 `MatchedCount`，否则「没匹配到」会被当成成功。
  这类问题单元测试（fake）发现不了，是 `-tags=integration` 连真库跑出来的。

### 22. 集成测试现在有了（`-tags=integration`）

- `internal/store/mongo/integration_test.go`：连 `MONGODB_URI_TEST` + `TEST_DB_NAME`（只允许
  `*_test`/`*_dev`），`TestMain` 里 Drop 干净库，覆盖：
  文章时区月界/分页/CountOnly/Full/重复 slug、分类标签归档、友链移动原子性与尾斜杠兼容、
  延迟计数、说说、摘要（Date 解码）、申请（ObjectID 主键、判重、初筛只改 pending、TTL 清理）、
  用户凭据、站点 key、整库导出形态。
- 没设置 `MONGODB_URI_TEST` 时整体 skip，`go test ./...` 不受影响。
- 跑法：
  ```bash
  MONGODB_URI_TEST=mongodb://127.0.0.1:27017 TEST_DB_NAME=AriaBlogNext_test \
    go test -tags=integration ./internal/store/mongo/ -v
  ```

### 23. 本地反复验证却从没跑过「生产启动路径」

- **现象**：`.env.dev` 里有 `CONFIG_FILE=./config.local.yml`，云端验证又用
  `CONFIG_FILE=/tmp/aria-cloud-config.yml`，于是 **`go:embed` 内置 config.yml 这条真实路径
  （Vercel 上不设 CONFIG_FILE）一直没被跑过**。
- **做法**：用一个不带 `CONFIG_FILE` 的 env 文件起服务（凭证真实、库指 dev），确认
  `config load` 走内置、四个子系统全开时能正常装配（S3/LLM 实测通），`PORT` 生效。
- **教训**：配置有多条来源时，要把**每条来源**都至少启动一次；只测分支最多的那一条，
  等于没测线上那条。

### 24. 验证「另一个仓库的在改代码」时，必须重新编译，别复用旧进程

- **现象**：glue 已采纳「后台只读摘要加 `generate=false`」，但我打 `/update/post/summary` 仍走生成路径
  （返回 502「摘要生成服务未启用」）。查了半天代码发现是对的——**2335 上跑的是旧编译产物**。
  用 `go build -o /tmp/glue-fresh ./cmd/server` 重新构建后立刻正常（`200 {"data":null,"message":"success"}`）。
- **教训**：跨仓库联调时，对方的代码随时在改；**先 `go build` 新产物再起**，或起进程前记录源码 mtime，
  否则会把「旧二进制」的行为当成对方的实现问题去报，白花时间（这次差点写成误报的 G 档）。
- 我的回放脚本（`tests/glue_replay.py`）只覆盖 go 侧契约，测不出这种「对方进程过期」的问题；
  可在脚本说明里加一句：**跑之前确认对端是新鲜构建**。

### 25. SSRF 只校验初始 URL 是不够的（已修）

- **现象**：`/links/theme-color` 与图床转存最初只对**传入的 URL** 做内网校验：
  `CheckRedirect` 只数跳数、不校验每一跳的 IP，且 `if ips, err := LookupIP(...); err == nil`
  在解析失败时**静默放行**（fail-open）。构造「公网 URL 302 到 `169.254.169.254`」即可绕过；
  DNS rebinding（校验时公网、连接时内网）同样能绕。
- **修法**：`internal/platform/safedial.go::NewSafeHTTPClient` —— 在 **DialContext** 里解析主机、
  把**所有**解析结果过黑名单（环回/私网/链路本地/未指定/组播），然后**直接连到已校验的 IP**
  （校验与连接绑定，消除 TOCTOU）；解析失败直接拒绝（fail-closed）。重定向复用同一 Transport，
  所以每一跳都受同样保护。生产一律 `allowPrivate=false`；只有测试用 `true`（httptest 在 127.0.0.1）。
- 旧后端其实是「手动跟重定向，每一跳都过内网校验」，这条是对齐它并补上 rebinding。
- 测试：`internal/platform/safedial_test.go`（直连内网被拒/allowPrivate 可连/重定向链被拦/IP 分类表）。

### 26. 零值 RequestTimeout 会让 handler 一进来 ctx 就过期

- **现象**：`/images/import` 在测试里恒返回 `image.unreachable`，cause 是
  `context deadline exceeded`，而且外面看是「拨号超时」——但实际 **DialContext 根本没被调用**。
  调试打印才发现 handler 里 `c.Context().Err()` 一开始就是 `context deadline exceeded`。
- **原因**：测试配置没设 `server.requestTimeout`（零值），`context.WithTimeout(ctx, 0)`
  立刻到期。生产配置校验保证 >0，所以线上不会踩；但零值配置的测试会得到莫名错误。
- **修法**：① `contextWithTimeout` 在 `d<=0` 时退化成 `context.WithCancel`（不设截止时间），
  宁可无超时也不要凭空秒死；② 测试配置补上 `RequestTimeout`/`DBTimeout`。
- **教训**：凡是 `context.WithTimeout(ctx, cfg.X)` 都该想一下 X 为零会怎样；
  「配置校验保证非零」不能替测试配置兜底。
