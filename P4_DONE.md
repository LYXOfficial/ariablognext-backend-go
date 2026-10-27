P4 COMPLETE 2026-10-27T03:06:00+08:00

# ariablognext-backend-go · 交付占位文件

> 本文件由 go 后端施工会话在 P4 收口时写入，代表**可交付、可接管**。
> 首行 `P4 COMPLETE` 之后的内容是给接手 agent（无通信能力）的完整交接信息。
> 本文件未 commit，不影响仓库状态。

---

## 一、P4 四项验收证据（2026-10-27 03:00–03:06 实测）

### 1. 静态守卫 `bash scripts/checks.sh`
```
== 8. 每个包都必须有测试文件 ==
  ✓ 包测试文件齐全

全部通过 ✅
```
（8 项全过：gofmt / build / vet / 禁 os.Getenv / 禁 handler 写响应+import net/http /
禁 mongo 泄漏 / 禁 bson 进接口层 / 禁 fmt.Print / 包测试齐全）

### 2. `go test ./...`
```
ok  internal/app            0.009s
ok  internal/app/middleware (cached)
ok  internal/apperr         (cached)
ok  internal/auth           (cached)
ok  internal/config         (cached)
ok  internal/handler        13.466s
ok  internal/service        0.010s
ok  internal/store/fake     (cached)
ok  internal/store/mongo    0.005s
ok  internal/textutil       0.002s
ok  internal/validate       (cached)
```
覆盖率（docs/testing.md §6 要求）全部达标：
| 包 | 实测 | 要求 |
|---|---|---|
| internal/app | **91.2%** | ≥90% |
| internal/handler | **82.7%** | ≥80% |
| internal/service | **82.0%** | ≥80% |
| internal/textutil | **90.1%** | ≥90% |
| internal/validate | **90.0%** | ≥90% |

### 3. `go vet ./...` 与 `gofmt -l cmd internal`
两者均**无输出**（干净）。

### 4. `python3 tests/smoke.py`
```
全部通过 29 / 29 ✅          # 公开接口 + 参数校验 + 鉴权 + stub + 旧路径404 + CORS
全部通过 31 / 31 ✅          # 加 --token 后多验 /auth/verify 与 /drafts/list
```

### 4.5 只读硬闸门（P4 后修复的缺陷，已复验）
初次实测发现：生产只读模式下写接口返回 **500 internal.error**（`store.ErrReadOnly`
未在渲染层映射）。已修复于 `internal/app/render.go`：
`errors.Is(err, store.ErrReadOnly)` → `503 + storage.read_only`。
复验（`ENV_FILE=.env.production CONFIG_FILE=./config.prod-readonly.yml`，端口 2444，
内部令牌调 `POST /links/latency`）：
```json
{"ok":false,"requestId":"K46T8PVhPD_…","error":{"code":"storage.read_only",
 "message":"服务正以只读模式运行（生产只读验证），写操作不可用","details":null}}
http=503
```
单测钉住：`internal/app/ctx_test.go::TestRenderReadOnlyErrMapsTo503`。

### 5. `python3 tests/compare.py`（生产只读）
```
共 41 项：40 通过，1 警告，0 失败
✅ 没有失败项（警告项是刻意的语义修正：只读闸门探测方式）
```
**✗ 数量 = 0**。新后端以
`ENV_FILE=.env.production CONFIG_FILE=./config.prod-readonly.yml` 连线上库只读运行，
与线上旧后端逐字段比对：83 篇文章、79 个标签、17 个分类、归档总数/分桶、
说说 120 条、友链 4 组/128 条、最后更新时间——全部一致。

---

## 二、路由核对

- **已注册路由数 = 55**（`grep -c '{Method:' internal/handler/register.go`），
  与 `docs/api-draft.md §2` 的 55 条表逐条对齐（posts 13 / drafts 7 / tags/categories/archives 3 /
  links 12 / applications 4 / speaks 4 / site 2 / images 5 / auth 2 / sitemap+export+health 3）。
- **旧路径全部 404**（实测）：`/get/post/postsInfo`、`/update/post/addPost`、
  `/posts/content`（GET，已废弃）以及任意未知路径都返回 `not_found.route` + 404。
- 删除类接口返回 `200 + {"deleted":true}`（非 204）；`/posts/semantic` 固定
  `501 + search.not_implemented`；所有错误响应都带 `error.details` 键（可为 null）。

---

## 三、已知偏差 / 未做项（重要，glue 与前端侧注意）

1. **图床未配置时的状态码**：契约错误码表规定 `upstream.failed = 502`
   （TASKS P3 验收文字写的是 503）。实现按**契约**返回 **502 + `upstream.failed`**，
   文案「图床未配置，请联系站长启用」。glue 侧如需 503 请勿自己改，先对契约。
2. **`/site/update-time` 不再通知刷新缓存**：refreshCache 耦合按用户要求**留在胶水侧**
   （glue 转发旧接口后自行 GET `blog.yaria.top/refreshCache/siteinfo`）。
   新接口支持 body `{"time": unix秒}`，缺省当前时间。
3. **刻意语义修正（compare 里的 ⚠ 类）**：
   - 归档分桶按 `Asia/Shanghai`（不再 UTC）；
   - 标签/分类按名称稳定排序（旧实现按不存在的 `date` 字段排 = 未排序）；
   - `/posts/navigation` 用 `newer/older`（旧 `previous/next` 名实相反，glue 需反向映射）；
   - `/posts/related` 无标签返回 `[]`（旧接口 404）。
4. **`POST /site/update-time` 有 `data.latestUpdateTime`**（旧接口无 data），glue 注意兼容。
5. **create 接口返回 201**（`/posts/create`、`/drafts/create`），其它成功接口 200。
6. **搜索行为增强**：多关键词空格分隔 AND；特殊字符按字面（QuoteMeta）；
   Markdown 结构（`<Folding>` 等外挂标签）不进索引。
   **生产库无 `searchText` 字段且禁止回填**（云端库绝不写），查询走
   「searchText 优先 + title/plainContent fallback」双路，功能不受影响。
7. **`/sitemap` 的 lastmod 用上海时区日期**（P4 后补齐为「每篇取 `lastUpdatedTime`」）：
   与旧后端逐条比对 87 条，URL 集合完全一致、74 条日期一致、13 条「新 = 旧 + 1 天」，
   原因是旧后端用进程 TZ（线上 UTC）、新后端按契约固定 Asia/Shanghai（design-notes §5），
   属刻意修正。glue 复验时 lastmod 允许「最多晚 1 天」，详见 sync `GO-003`。
8. **未做（留待后续，不在本次交付范围）**：
   - `internal/store/pgstore`（Postgres 适配器）——契约预留，未实现；
   - 语义搜索真实实现（`/posts/semantic` 固定 501，`cmd/build-embeddings` 只打印跳过）；
   - 图片上传/转存的端到端自动化测试（已用云端 R2 人工验证，未写集成测试）；
   - 集成测试 `-tags=integration` 的用例集（本次用 fake + 生产只读覆盖；
     `MONGODB_URI_TEST` 未设置时相关测试 skip，`go test ./...` 不受影响）。

---

## 四、环境现状（给接手 agent）

### 进程与端口
| 端口 | 状态 | 说明 |
|---|---|---|
| 127.0.0.1:27017 | **运行中**（mongod 8.0.14） | `/tmp/aria-mongo/mongodb-linux-x86_64-ubuntu2404-8.0.14/bin/mongod --dbpath /tmp/aria-mongo/db` |
| 127.0.0.1:2333 | **运行中**（go 后端，`.env.dev` + `config.local.yml`） | 供 glue 联调；外部依赖全关（不发信/不传图/不触发 Actions） |
| 2444 / 2445 | 已清理（03:12 复核） | 生产只读实例与降级实例都停掉了；需要时按上面命令临时拉起 |

### 重启方法
```bash
# 1) mongod（重启机器后需要）
mkdir -p /tmp/aria-mongo/db
/tmp/aria-mongo/mongodb-linux-x86_64-ubuntu2404-8.0.14/bin/mongod \
  --dbpath /tmp/aria-mongo/db --port 27017 --fork \
  --logpath /tmp/aria-mongo/mongod.log --bind_ip 127.0.0.1

# 2) 后端（本地联调配置）
cd /media/Coding/AriaBlogNext/ariablognext-backend-go
go run ./cmd/seeddev          # 幂等灌种子（只允许 *_dev/*_test 库）
ENV_FILE=.env.dev go run ./cmd/server      # 127.0.0.1:2333

# 3) 生产只读（对比用，绝不写库）
ENV_FILE=.env.production CONFIG_FILE=./config.prod-readonly.yml go run ./cmd/server
```

### dev 库账号（本地联调用）
- 库：`AriaBlogNext_dev`
- 用户名 `seedadmin` / 密码 `seed-admin-password`
- 拿 token：
  ```bash
  curl -s -X POST localhost:2333/auth/login -H 'Content-Type: application/json' \
    -d '{"user":"seedadmin","password":"seed-admin-password"}'
  ```
- 种子内容：5 篇文章（含外挂标签/特殊字符/多关键词样本）、3 条说说、2 个友链分组、站点时间戳。

### 云端实测过的凭证用法（用户已授权）
- LLM（SiliconFlow）、S3（Cloudflare R2）、SMTP（阿里企业邮）、GitHub token
  都做过真实端到端验证；临时凭证文件在 `/tmp/aria-cloud.env` 与
  `/tmp/aria-cloud-config.yml`（**均不在仓库、不进 git**，重启机器即失效）。
- **数据库铁律**：云端（生产）库绝不可写；所有写操作只能落 `AriaBlogNext_dev` / `AriaBlogNext_test`。
  种子/回填工具内部都强制校验库名后缀。

---

## 五、回看位置（约定）

如果接手 agent 在联调中发现**属于 go 后端的问题**，请写到：

```
/media/Coding/AriaBlogNext/HANDOVER-glue-takeover.md
  └─ §9.1「待 go 侧修复（由你填写）」
```

go 侧守候方式（每轮检查一次即可，不要长 sleep）：
```bash
awk '/^### 9.1/,/^### 9.2/' /media/Coding/AriaBlogNext/HANDOVER-glue-takeover.md
```

**当前状态：§9.1 为空**（2026-10-27 03:12 检查，交接文件已生成但接手 agent 尚未写入问题）。
go 侧已按用户要求进入守候状态：发现问题就修，修完在 P4_DONE.md 追加
「§7 修复记录」并在 HANDOVER §9.1 对应条目打勾说明。
建议每条包含：复现命令、期望 vs 实际、相关接口路径、错误码与 requestId。

同理，go 侧如果发现 glue/前端需要配合的行为差异，会写在
`/media/Coding/AriaBlogNext/ariablognext-backend-go/docs/pitfalls.md`（已有 24 条）
和本文件「三、已知偏差」区。

---

## 九、本地全栈实况（2026-10-27 11:42 起，用户指示「库指向线上，直接上云」）

用户明确指示本地后端**直连线上 Atlas（AriaBlogNext 库、可写）**，作为直接上云前的真实演练。
当前在跑的四个进程：

| 端口 | 服务 | 说明 |
|---|---|---|
| 2333 | backend-go | `ENV_FILE=.env.production`（内嵌 config.yml，四子系统全开，readOnly=false），连线上 Atlas |
| 2334 | glue | `/tmp/glue-live`（新鲜构建），UPSTREAM=http://127.0.0.1:2333 |
| 3000 | 博客前端 | `pnpm dev`，`src/config.tsx` backEndUrl → http://127.0.0.1:2334（工作区改动，未提交） |
| 3001 | 管理面板 | `pnpm dev`，`src/dashboardConfig.ts` backEndUrl → http://127.0.0.1:2334（同上） |

验证结果：
- 博客首页 SSR 渲染**线上真实数据**：83 篇文章（分页 6 页）、17 个分类、79 个标签、
  说说（最新 1 天前）、最新评论（twikoo 生产服务）、公告、统计（22.45w 字）；
- 经 glue 的旧契约链路全通：postCount/postsInfo/totalWordCount/tags/flinks/speaksCount/
  lastUpdateTime/themeColor（真实 jpg 头像取色成功，webp 按契约拒绝）；
- 面板登录页正常；未登录时 draft 接口 401（预期）；
- glue CORS 预检通过（`Access-Control-Allow-Origin` 等）。

⚠️ **注意**：此模式下面板/后端的**所有写操作都直接作用于线上库**（审批、删文、改配置都是真的）；
`ensureIndexes` 已在线上库幂等建好 7 个索引。邮件/GitHub dispatch/R2 上传也都是真实副作用。
此前「生产库只读」的铁律自本条起按用户指示解除（仅限本地这套全权限实例）。

## 7.5 行为不一致批量审计（2026-10-27 中午，用户要求「全部揪出来」）

用生产真实数据逐接口对比新旧后端，又修掉 4 个真实不一致（详见 `docs/pitfalls.md` #27/#28）：

1. **取色算法**：逐行复刻旧实现（BOX 64×64 → 跳透明/极浅/极深 → HSL 分桶彩度加权 →
   normalize_for_card），复验 3/6 头像颜色与旧后端完全一致、3/6 仅 ±1~4 通道差
   （BOX 缩放边界实现差异，视觉无感知，用户确认不追）。修前 6/6 全不同且出现
   `#000000`/`#fdfdfd` 废色。
2. **webp 头像**：加 `golang.org/x/image/webp`+`bmp`（decode-only）——⚠️ 依赖清单外
   偏离，为行为一致必须加。
3. **图片转存魔数嗅探**：content-type 不可信（octet-stream），复刻旧 `sniff_image_type`。
4. **口径对齐**：wordCount 作用于 markdown 原文、公式复刻旧正则（`\p{L}\p{N}_` + CJK
   逐字，双计口径）；文章 plainContent 换行压空格截 201；说说 plainContent 用
   `replace_and_remove_tags`（<a>→[链接]、<img>→[图片]）。
5. **环境变量语义修订**：未定义 → 按空处理（EMBEDDING_API_KEY 类预留占位符不再逼着
   每个环境定义）；必填性由 Validate 保证，校验失败时列出未定义名单。

## 八、sync 信箱（glue ↔ go 唯一通信通道）

工作区根目录 `sync/`（协议见 `sync/README.md`）。go 侧强制检查点：每次开始干活前、
每完成一个阶段、写总结前，扫 `sync/inbox/` 里 `to: go` 的 `G-*` 文件，处理完移到 `sync/closed/`。

已处理：
| 档号 | 问题 | 结论 | 落点 |
|---|---|---|---|
| G-001 | `POST /site/update-time` 空 body 返回 400 | **fixed**（新增 `Ctx.BindOptional`） | `internal/app/ctx.go`、`internal/handler/writes.go`、测试 `TestSiteUpdateTimeEmptyBody` |

回看状态（2026-10-27 04:00）：glue 已收取并归档 GO-001/002/003（全部 `fixed`）——
- GO-001：503 `storage.read_only` 透传，旧前端看到 503 + detail 文案，glue 侧零改动；
- GO-002：glue 采纳「严格对齐旧行为」方案：404→200、speaks 的 400→200、addFlink 做 upsert 仿真
  （先查组→缺则建），重名建组仍透传 409（刻意不仿真）；
- GO-003：sitemap 逐字节直通，lastmod 判据记为「URL 强一致 + 最多晚 1 天」。

已寄出（`to: glue`）：
- **GO-001**：生产只读模式下写接口返回 503 `storage.read_only`（原为 500）。
- **GO-004**：建议 glue 对 `updateSpeaks` 的 400 用 `details.fields[0].field=="time"` 做锚点，
  别只锚定中文文案；go 侧已用测试钉住该结构（`TestSpeakUpdateNotFoundShapeIsStable`）。
- **GO-003**：sitemap `lastmod` 口径说明（每篇 `lastUpdatedTime` + 上海时区，87 条里 13 条比旧后端晚 1 天）。
- **GO-002**：写接口「目标不存在」新旧行为不同（旧恒 200 静默成功；新按契约 404/400），
  附 10 行对照表 + 一条语义错位（旧 `addFlink` 带 upsert 会自动建组，新 `/links/create` 要求组已存在），
  并给出 glue 侧两种处理方案。

### 用 glue 会话自己的运行实例复跑回放（2026-10-27 05:07）

发现 glue 会话自己在 `127.0.0.1:2334` 起了实例（upstream 指向本后端 2333）。
直接对它跑 `tests/glue_replay.py http://127.0.0.1:2334` → **0 失败（34 项断言）**，
说明「他们的运行时 + 我最新代码（SSRF/体积/令牌加固后）」组合正常。

回放产生的 dev 数据已清理：`seeddev -wipe` + 清掉 `replay-*` 申请，
核对 posts=5 / speaks=3 / links=3（2+1）/ replay 申请=0。

### 安全/健壮性加固（2026-10-27 05:00）

1. **内部令牌改常量时间比较**（`crypto/subtle`）：`!=` 会按首个不同字节提前返回，
   理论上可逐字节试探 `SECRET`。测试覆盖「正确 / 长度不同 / 前缀加长 / 少一字符」四种。
2. **分块传输绕过请求体上限**：中间件只查 `Content-Length`，`Transfer-Encoding: chunked`
   没有这个头，会放过 1MB 档位的 2MB 请求（只剩框架 6MB 兜底）。
   已让 `Ctx` 记住路由档位（`Add` 里写 `bodyLimit`），`Bind`/`FormFile`/`RawBody` 读完
   body 后再量一次。测试用**真实监听端口的 chunked 请求**验证（fiber.Test 的 dump 不可靠）。
3. **零值 `RequestTimeout` 让 ctx 秒死**：测试配置漏设导致 `context.WithTimeout(ctx,0)`
   立刻过期，外部调用全是 `context deadline exceeded` 且看不出原因。
   `contextWithTimeout` 已在 `d<=0` 时退化为 `context.WithCancel`（不设截止），
   测试配置补 `RequestTimeout`/`DBTimeout`。记录进 `docs/pitfalls.md #26`。

### SSRF 加固：把内网校验挪到 dial 阶段（2026-10-27 04:58）

复查安全面发现真实缺口：取色与图床转存只校验**初始 URL**，
`CheckRedirect` 不校验每一跳、`LookupIP` 失败时 fail-open → 「公网 URL 302 到
`169.254.169.254`」或 DNS rebinding 都能绕过（旧后端是「每一跳都过内网校验」）。

已修：`internal/platform/safedial.go::NewSafeHTTPClient` 在 DialContext 里解析并校验**所有** IP、
直接连到已校验 IP（消除 TOCTOU）、解析失败即拒绝；重定向复用同一 Transport 天然受保护。
`theme.go` 与 `FetchExternalImage` 都改用它；测试 `safedial_test.go`（直连内网被拒、
allowPrivate 可连、重定向链被拦、IP 分类表）。记录进 `docs/pitfalls.md #25`。

### 冷启动交付快照（2026-10-27T04:52:10+08:00，一次性重跑全套验收）

清库重灌种子 → 冷启动 → 全部验收项，结果固化如下：

| 验收项 | 结果 |
|---|---|
| `scripts/checks.sh`（8 项架构守卫） | ✅ 全绿 |
| `go test -count=1 ./...` | **11 包全过** |
| `go test -tags=integration -count=1 ./...`（本地 mongod） | **11 包全过** |
| `go vet` / `gofmt -l` | 无输出 |
| `tests/smoke.py` | **29 / 29** |
| `tests/glue_replay.py`（glue 新鲜构建 → 本后端） | **0 失败**（34 项断言） |
| 路由数 | **55** |
| 覆盖率 app / handler / service / textutil / validate | **91.4% / 82.2% / 81.1% / 90.9% / 90.0%** |

### 导出顺序确定化（2026-10-27 04:41，采纳 glue 在 GO-008 的建议）

glue 独立复验导出时指出：**两次导出的自然顺序不稳定**，逐位置 zip 会得到「2539/2539 全不同」
的假差异。已在 go 侧解决：导出前按 `_id` 升序稳定排序，同一份数据的 `data.collections`
**字节级一致**（响应体的 `requestId` 仍每次不同，校验和要取 collections 本体）。

- 承重测试：`TestExportDeterministicOrdering`
- 实测（生产只读）：两次导出 collections 的 md5 相同（`86469460…`）；Posts 按 `_id` 升序；
  与旧接口逐文档比对 2539 条 **0 差异**
- 已寄 sync `GO-011`，glue 可以省掉「先排序」那一步

### 与 glue 在改代码的联调（2026-10-27 04:35）

glue 恢复活动后（`docs/glue.md` 04:31 被改），用他们**当前未提交代码**重新构建并回放：
- `tests/glue_replay.py` → **0 失败**；
- 他们已采纳 GO-007：后台只读摘要实测 `200 {"data":null,"message":"success"}`（旧形状，不再触发生成）；
- 他们已采纳 GO-002：`docs/glue.md` 新增 §5.7「写接口目标不存在的静默成功」。

**踩坑记录**：第一次测 `/update/post/summary` 时仍走生成路径，误以为是他们的实现问题——
实际是 2335 上跑着**旧编译产物**。`go build` 后立刻正常。已写进 `docs/pitfalls.md #24` 与
`tests/README.md`（跨仓库联调前必须重新构建对方代码）。

### 覆盖率复算（2026-10-27 04:33）

改动累积后复算门槛包覆盖率，发现 `internal/app` 掉到 **89.9%**（低于 docs/testing.md §6 的 90%），
补齐 `BindOptional` 的四个分支（空 body / 有 body / 非法 body / 带 Content-Type 的空 body）、
`RawBody` 空体、`Meta` 两种构造后回到 **91.4%**。当前：

| 包 | 覆盖率 | 门槛 |
|---|---|---|
| internal/app | **91.4%** | ≥90% |
| internal/handler | **82.2%** | ≥80% |
| internal/service | **81.1%** | ≥80% |
| internal/textutil | **90.9%** | ≥90% |
| internal/validate | **90.0%** | ≥90% |

### 切流清单已交付（2026-10-27 04:30，sync `U-001`）

把「前端/后台/外部调用方从旧后端切到 glue」的完整待办写成了给用户的清单：
改哪一行（`frontend/src/config.tsx:37`、`dashboard/src/dashboardConfig.ts:9`、
`.gitea/workflows/deploy-to-liteyuki.yml:47` 的健康检查 URL）、glue 的 `UPSTREAM`、
三处外部调用方（备份 / check-flink / flink-screening）的 URL+请求头+响应路径改动、
切流后 3 条自检命令、回滚方式（改回 `backEndUrl`，`SECRET` 不变所以不用重新登录），
以及 4 条已知行为差异（搜索变少、归档时区、排序、写接口 404）。

顺带验证：方法不匹配（`GET /posts/create`、`PUT /health` 等）统一返回
`404 not_found.route` 信封，不是裸 405。

### 生产启动路径验证（2026-10-27 05:00，`go:embed` 内置配置 + 子系统全开）

之前所有本地验证都用 `CONFIG_FILE` 指向 `.env.dev` 的本地变体或 `/tmp` 云端副本，
**从未跑过 Vercel 上真正走的那条路：不设 `CONFIG_FILE` → 用 go:embed 的内置 `config.yml`
（media/mail/screening/summary 全开）**。本轮补验：

```bash
# 临时 env：真实云端凭证 + 本地 dev 库，但故意不带 CONFIG_FILE
PORT=2445 ENV_FILE=/tmp/aria-embedded.env go run ./cmd/server
```

结果：
- 启动日志 `msg=配置加载完成 … readOnly=false`、`msg=服务启动 … routes=55`，`PORT` 生效（`:2445`）；
- `GET /health` → `db:ok`；脱敏日志里 `apiKey`/`jwtSecret`/`internalToken` 均显示 `***`；
- **S3 真实 R2**：`GET /images/list` → 2156 张、元素为 `str`；
- **LLM 真实 SiliconFlow**：`POST /posts/summary`（cloud-note，原无缓存）→ 45s 返回并落库；
- （邮件与 GitHub dispatch 在 P3 已用同一批凭证实测过，此处不重复触发）

### 修复后的生产只读回归（2026-10-27 04:52）

改动累积后重跑生产只读验证，确认零回归：

- `tests/compare.py`：**40 通过 / 1 警告 / 0 失败**（与 P4 交付时一致）；
- 搜索正文深处召回：`keyword=这段英文` → 1 篇（与旧后端一致；修前是 0）；
- taxonomy 列表 `meta.count`：tags=79、categories=17、archives=26（新增字段，旧前端忽略）；
- 只读硬闸门：内部令牌调写接口 → `503 storage.read_only`；
- 导出形状抽查：`FLinkApplications.createdAt` = `"2026-10-17T12:25:03.650000"`（字符串，非扩展 JSON）。

### 集成测试补齐 + 抓到 MoveLink 数据丢失（2026-10-27 04:40）

补上 TASKS 一直挂着的 `-tags=integration` 测试（`internal/store/mongo/integration_test.go`，
连 `MONGODB_URI_TEST`/`TEST_DB_NAME`，TestMain Drop 干净库；无测试库时整体 skip）。

**第一次跑就抓到真实数据丢失缺陷**：`MoveLink` 的目标组不存在时，`$push` 匹配 0 条静默成功、
紧接着 `$pull` 真的删了源组链接 → 友链凭空消失且接口返回成功。已修：目标组 `SetUpsert(true)`
（契约与 docs/testing.md §4.2 都要求自动建组），pull 失败回滚 push；测试覆盖
「自动建组成功」「源组/源链接不存在返回 ErrNotFound 且不动数据」。

集成测试全量（11 包 + 集成标签）全绿；`go test ./...`（不带标签）同样全绿。
记录进 `docs/pitfalls.md` #21/#22。

### 跨服务真机回放（2026-10-27 04:12，go + glue 端到端）

起了一个 glue 实例（`PORT=2335 UPSTREAM=http://127.0.0.1:2333`，只运行不改其仓库），
用**旧契约**对 dev 后端做全链路回放，脚本已收进仓库：`tests/glue_replay.py`（用法见 `tests/README.md §6`）。

- 覆盖 20 条只读旧接口的旧格式形状 + 写接口全链路（文章/说说/友链/申请）+ 限流；
  **最终 0 失败，可重复跑（纳秒级幂等后缀）**。
- 过程中我自己踩了个坑（不是 glue 的问题）：旧 `moveFlinkGroup` 的 body 是 **snake_case**
  （`from_group/to_group/link_id`），我按新接口的 camelCase 传 → 400；glue 的转换是对的。
- 顺手验证了 `type=full` 带 `mdContent`、`plainContent ≤201`、`tags:[]` 不是 null、
  图床未配置时不死 500、apply 的 5 次/30 分钟限流生效。

### 字段形状核对（2026-10-27 04:25，生产真实数据逐接口比对键集合）

用生产只读数据把剩余只读接口的**键集合**逐一对齐，结论（详见 sync `GO-009`）：
- **完全一致**：`postsInfo↔/posts/list`（11 键）、`flinks↔/links/list`（链接 7 键）、
  `speaks↔/speaks/list`、`lastUpdateTime↔/site/info`；
- **无害超集**（新返回更多键，旧前端忽略）：tagInfo/archiveInfo 条目多 `plainContent`、
  友链分组多 `order`、搜索结果返回完整摘要（旧只有 4 键）；
- **新侧唯一缺少**：详情里不再有 `cachedHtml`（该字段已废弃，前端 `src/` 0 引用、
  dashboard 仅一行可选类型声明无运行时读取）；
- ⚠ 提醒 glue：旧 `tagInfo`/`archiveInfo` 顶层是 **`totalCount`** 而不是 `count`。

### 全库导出形状对齐（2026-10-27 04:15，契约 §3.9「与旧接口完全一致」逐字验证）

**问题**：初版用 `bson.MarshalExtJSON`，Date 输出 `{"$date":…}`、int64 输出 `{"$numberLong":…}`、
ObjectID 输出 `{"$oid":…}`，与旧 FastAPI json 形状不同；备份仓库按旧格式解析会坏。
另有隐蔽坑：**内嵌文档解码成 `bson.D`**，漏处理时嵌套 Date 退回 Go 的 `...Z` 形态。

**修法**：`internal/store/mongo/export.go::toPlainJSON` 递归转换：
Date → `2006-01-02T15:04:05.000000`（naive UTC，与 Python `isoformat()` 一致）、
ObjectID → 24 位 hex、int64 → 数字、`bson.D`/`bson.A` 递归。
测试：`internal/store/mongo/store_test.go::TestToPlainJSONShape`。

**实测（生产只读实例 vs 线上旧接口，同一份生产数据）**：
11 个集合、**2539 条文档、逐字段不同 0 条**（`Drafts 8 / FLinkApplications 33 / FLinks 4 /
PostSummary 83 / Posts 83 / Speaks 120 / Users 1 / comment 2203 / config 2 / counter 1 / webSiteInfo 1`）。
已寄 sync `GO-008` 通知 glue 可放心直通。

### 摘要「只读缓存」模式（2026-10-27 04:33，审查 dashboard 弹窗行为时发现）

**问题**：旧后端有两个摘要接口——公开 `GET /get/post/postSummary`（miss 时生成一次）与
后台预览 `GET /update/post/summary`（**只读缓存**，miss 返回 `200 data:null`）。glue 把两者
都转到 `GET /posts/summary`（会自动生成），导致 **dashboard 打开编辑弹窗时静默等最多 90 秒**
并消耗一次 SiliconFlow 额度。

**修法**：`GET /posts/summary` 新增可选参数 `generate`（缺省 `true`，契约行为不变）；
`generate=false` 时只读缓存：命中 `{"summary":"…"}`、未命中 `200 {"summary":null}`（与旧一致，不生成不报错）。
落点：`internal/handler/posts.go`、`internal/service/service.go::SummaryCached`；
测试 `TestPostsSummaryCachedOnly`。实测 dev：命中 6ms、未命中 6ms、默认形态仍缓存优先。
已寄 sync `GO-007`，请 glue 把后台只读那条映射加 `generate=false`。

### 图床列表形状修复（2026-10-27 04:20，审查 dashboard 实际用法时发现）

**问题**：旧 `/update/image/listImages` 返回 `{"images":["https://…"]}`（**URL 字符串数组**），
dashboard 把它当 `string[]` 用（`key={img}`、`img` 当图片地址）；而新 `/images/list` 之前返回
对象数组，glue 又是「原样透传 data」→ 图片墙会把 `[object Object]` 当 URL，整片图裂。

**修法**：`internal/handler/images.go::List` 的 `data.images` 改回 **URL 字符串数组**（旧形状），
富信息放 `data.items`（旧前端忽略未知键），并按 `lastModified` 倒序（契约 §3.8「按日期倒序」）。
测试：`internal/handler/images_test.go::TestImagesListReturnsURLStrings`；
真实 R2 实测 2156 张、`images[0]` 为 `str`、`lastModified` 严格降序。
已寄 sync `GO-006` 通知 glue：**不要**再加 object→string 转换（会拆成单字符）。

### 搜索保真度修复（2026-10-27 04:05，sync 等待期自检发现）

**问题**：生产库没有 `searchText`（云端库只读、按铁律不回填），fallback 只匹配
`title+plainContent`，而 `plainContent` 在库里截断到 201 字 → **正文中段的关键词搜不到**
（旧后端搜全文 `mdContent`）。实测 3 个「只出现在正文深处」的词：旧后端各 1 篇、新后端各 **0 篇**。

**修法**：库里存在无 `searchText` 的文档时走 Go 侧全文匹配（`mongo/post.go::pageFullText`）：
取候选 → `textutil.BuildSearchText(title, mdContent)` 现算可检索文本 → 多关键词 AND /
排序 / 分页 / context 全在 Go 里完成；有 `searchText` 的库仍走正则快路径。
`textutil` 同时把剥标签的精度调细：丢弃标签名/URL/结构性属性，但**保留 `title=`/`summary=`
这类渲染后可见的属性文字**（否则 `<Link title="…服务器…">` 里的正文会丢）。

**验证（生产只读 vs 线上旧后端，逐词比对 slug 集合）**：
- 正文词 12 个样本（Twikoo/评论/主题/图片/服务器/配置/部署/Hexo/Butterfly/pjax/弹幕/腾讯云）
  **12/12 完全一致**；
- `fold` 19→2、`Folding` 18→1：旧后端把 `<Folding>` 标签名/属性也算命中，
  新后端按用户要求只认正文——**刻意减少**（详见 sync GO-005）。

### 契约偏差修复（2026-10-27 03:32，sync 等待期自检发现）

1. **`/tags/list`、`/categories/list`、`/archives/list` 非 countOnly 时缺 `meta.count`**
   （契约 §3.3 要求 `data + meta={count}`）。已补：三者都返回 `meta.count`（去重后的种类数 /
   月份桶数），countOnly 形态不变（data=null，meta 只有 count）。
   测试：`internal/handler/read_handlers_test.go::TestTaxonomyListsCarryCountMeta`；
   golden 已重生成（tags/categories/archives 三个快照带 meta）。
2. **`tags` 可能输出 `null`**（存量文档缺字段时）——AGENTS.md 明令切片不能出 null。
   已加 `model.Post.Normalize()` / `model.Draft.Normalize()`，mongo/fake 适配器解码后统一兜底成 `[]`；
   顺带修掉 fake `clonePost` 用 `append([]string(nil), …)` 把空切片又变回 nil 的坑。
   测试：`TestPostTagsNeverNull`（list + detail 两个形态）。

### 全路由真机自扫（56 次调用，2026-10-27 03:24）

按「55 条路由 + 1 条方法探测」逐条带合法参数/正确令牌调用 dev :2333：

- 54 条返回 2xx；1 条为已知未配置子系统的预期失败（`/links/check` → 502 `upstream.failed`，
  dev 无 `GH_TOKEN`）；图片 5 条按设计返回 502 `upstream.failed`（media.enabled=false）。
- 1 条「失败」是扫描脚本自身把 `GET /maintenance/export`（本就只 POST）列入，属误报；
  `POST /maintenance/export` → 200。
- 扫描写入的临时数据（move 过的友链、扫描说说、扫描申请、扫描文章/草稿）已全部复原：
  `cmd/seeddev -wipe` + 清理 `sweep@example.com` 申请，核对 posts=5 / speaks=3 /
  links=2 组 3 条 / 扫描申请=0。

## 七、修复记录（P4 之后）

| 时间 | 问题 | 修复 | 复验 |
|---|---|---|---|
| 2026-10-27 03:10 | 只读模式写接口返回 500（`store.ErrReadOnly` 未映射） | `app/render.go` 统一映射为 503 `storage.read_only` | 生产只读实例实测 503 + 单测钉住 |

## 六、文档索引（都在仓库内）

| 文件 | 内容 |
|---|---|
| `docs/api-draft.md` | 55 条接口契约（唯一事实来源） |
| `docs/store-interfaces.md` | 存储层接口 |
| `docs/design-notes.md` | 设计取舍（信封/配置/时区/并发） |
| `docs/testing.md` | 五层测试方案 |
| `docs/pitfalls.md` | **施工踩坑 24 条 + 硬性规则**（改动前必读） |
| `tests/README.md` | 冒烟/集成/生产只读的操作步骤 |
| `TASKS.md` | 施工清单（P0–P4，均已勾完） |
| `AGENTS.md` | 仓库工作守则 |
