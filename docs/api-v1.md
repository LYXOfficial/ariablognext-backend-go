# AriaBlogNext Backend API · v1.0（正式版 · 全量接口参考）

> 生效日期：2026-10-28 ｜ 服务：`ariablognext-backend-go` ｜ 56 条路由
>
> 本文档是**对外承诺**：字段名、错误码、状态码一经发布不再随意变动；
> 加字段只会向后兼容地加，改语义必须升版本。设计讨论与历史依据见 `api-draft.md`。
>
> 三个环境：
>
> | 环境 | 基地址 | 用途 |
> |---|---|---|
> | 生产（经 glue） | `https://blognext-end.yaria.top` | 前端/第三方走**旧契约**（glue 翻译层），不是本文档的路径 |
> | 生产直连 | `https://neoend.yaria.top` | **本文档描述的接口** |
> | 本地开发 | `http://127.0.0.1:2333` | |

---

## 目录

- [1. 通用约定](#1-通用约定)：1.1 风格 ｜ 1.2 信封 ｜ 1.3 错误码总表 ｜ 1.4 鉴权 ｜ 1.5 分页 ｜ 1.6 限流与体积 ｜ 1.7 时间
- [2. 数据结构](#2-数据结构)
- [3. 接口参考](#3-接口参考)
  - [3.1 运维](#31-运维)：`/health` `/status`
  - [3.2 文章](#32-文章-posts)：13 条
  - [3.3 草稿](#33-草稿-drafts)：7 条
  - [3.4 分类体系](#34-分类体系)：3 条
  - [3.5 友链](#35-友链-links)：11 条
  - [3.6 友链申请](#36-友链申请-linksapplications)：5 条
  - [3.7 说说](#37-说说-speaks)：4 条
  - [3.8 站点](#38-站点-site)：2 条
  - [3.9 图床](#39-图床-images)：5 条
  - [3.10 鉴权](#310-鉴权-auth)：2 条
  - [3.11 其它](#311-其它)：`/sitemap` `/maintenance/export`
- [4. 调用注意事项](#4-调用注意事项)
- [5. 变更记录](#5-变更记录)

每个接口一节，统一格式：**功能一句话 → 鉴权/限流 → 请求参数表（位置/类型/必填/默认/说明）→ 响应 → 本接口专属错误码 → 备注**。

---

## 1. 通用约定

### 1.1 风格

- 路径 `/<资源>/<细分>` 两段式，无版本前缀（本文档即 v1 契约）
- **路径里不出现标识符**：`slug` / `id` / `name` / `time` 一律走 query 或 body
- 读 `GET`，写 `POST` / `PUT` / `DELETE`；不用 `PATCH`
- 参数与字段名统一**驼峰**，与数据库字段名一致

### 1.2 统一响应信封（硬约束）

成功（HTTP 200；`*/create` 为 201）：

```json
{
  "ok": true,
  "requestId": "01J9Z3K2Q7",
  "data": [],
  "meta": { "page": 1, "pageSize": 16, "count": 83, "hasMore": true }
}
```

失败：

```json
{
  "ok": false,
  "requestId": "01J9Z3K2Q7",
  "error": { "code": "post.not_found", "message": "没有找到这篇文章", "details": null }
}
```

1. 所有接口都带信封；**唯一例外** `GET /sitemap` 返回 `application/xml`
2. 没有 204——删除成功也是 `200 + data.deleted=true`
3. `data` 可为 `null` 但键必须存在；`details` 同理
4. `meta` 只在列表/分页类接口出现
5. `error.message` 面向人（中文）；`error.code` 面向程序，稳定不变
6. `details` 形状固定三种：校验失败 `{"fields":[{field,message}]}`；内部错误 `{"cause":"…"}`；上游错误 `{"cause":"…","upstream":"…","status":N}`；无附加信息为 `null`

### 1.3 错误码总表

| code | HTTP | 含义 |
|---|---|---|
| `validation.invalid_field` | 400 | 字段格式不对，细节在 `details.fields` |
| `validation.missing_field` | 400 | 必填缺失 |
| `auth.missing_token` | 401 | 没带 token |
| `auth.invalid_token` | 401 | token 无效/过期 |
| `auth.locked` | 403 | 登录失败过多（与密码错误同一文案） |
| `not_found.route` | 404 | 路径不存在（旧路径不兼容，不静默） |
| `post.not_found` | 404 | 文章不存在 |
| `post.slug_conflict` | 409 | 文章 slug 已存在 |
| `draft.not_found` | 404 | 草稿不存在 |
| `draft.slug_conflict` | 409 | 草稿 slug 已存在 |
| `link.not_found` | 404 | 友链/分组不存在 |
| `link.group_conflict` | 409 | 分组名已存在 |
| `application.not_found` | 404 | 申请不存在或已处理 |
| `application.duplicate` | 409 | 网址已收录 / 已有完全相同的待审申请 |
| `application.no_change` | 400 | 修改申请与现状完全一致 |
| `application.full` | 429 | 待审队列已满（20 条） |
| `image.unsupported_type` | 415 | 不是受支持的图片格式 |
| `image.too_large` | 413 | 超过体积上限 |
| `image.unreachable` | 502 | 对方站点下载失败/超时/不是图片 |
| `search.not_implemented` | 501 | 语义搜索预留位（v1.0.2 起 `/posts/semantic` 不再返回 501，码位保留） |
| `storage.read_only` | 503 | 只读模式（生产只读验证），写接口一律拒绝 |
| `rate_limit.exceeded` | 429 | 触发限流，带 `Retry-After`（秒） |
| `timeout.exceeded` | 504 | 服务端处理超时 |
| `upstream.failed` | 502 | 外部服务（SiliconFlow / GitHub / 图床）失败 |
| `internal.error` | 500 | 兜底 |

### 1.4 鉴权

| 级别 | 请求头 |
|---|---|
| 公开 | 无 |
| 后台 | `Authorization: Bearer <jwt>`（HS256，7 天，`POST /auth/login` 获取） |
| 内部 | `X-Internal-Token: <SECRET>`（与 JWT 共用同一密钥） |

### 1.5 分页（list 类接口共用）

| 参数 | 位置 | 类型 | 必填 | 默认 | 说明 |
|---|---|---|---|---|---|
| `page` | query | int | 否 | 1 | 从 1 开始（1–1,000,000） |
| `pageSize` | query | int \| `full` | 否 | 16 | 1–200；`full`=不分页（仅 posts/drafts/speaks 支持，上限 300 条） |
| `countOnly` | query | bool | 否 | false | true 时**不执行 find**，`data=null`，`meta={count}` |

三种响应形态：

```jsonc
// 默认：列表 + 总数一次拿
{ "ok": true, "data": [ … ], "meta": { "page": 1, "pageSize": 16, "count": 83, "hasMore": true } }
// countOnly=true：纯数量
{ "ok": true, "data": null, "meta": { "count": 27 } }
// pageSize=full：全量
{ "ok": true, "data": [ … ], "meta": { "page": 1, "pageSize": "full", "count": 83, "hasMore": false } }
```

`meta.count` 是**当前过滤条件下的总数**；`hasMore = page*pageSize < count`。

### 1.6 限流与请求体上限

| 范围 | 限制 |
|---|---|
| 全局（每 IP） | 300 次 / 分钟 |
| `POST /auth/login` | 10 次 / 10 分钟 |
| `POST /links/applications/apply` | 5 次 / 30 分钟 |
| `GET /links/theme-color` | 60 次 / 小时 |
| 请求体（默认） | 1 MB |
| 请求体（正文写入） | 2 MB（`PUT /posts/content`、`PUT /drafts/content`） |
| 请求体（图片上传） | 4 MB |

命中限流：`429 + Retry-After + rate_limit.exceeded`。按进程内实现，多实例各自独立。

### 1.7 时间

时间戳一律 **unix 秒（int64）**；「哪年哪月」固定按 **`Asia/Shanghai`** 划分。

---

## 2. 数据结构

**Post**：

| 字段 | 类型 | 说明 |
|---|---|---|
| `slug` | string | 唯一标识 |
| `title` | string | |
| `category` | string | 单值 |
| `tags` | []string | |
| `bannerImg` | string | 封面 URL |
| `description` | string \| null | 人工摘要 |
| `coverFit` | string \| null | 封面 object-position |
| `publishTime` | int64 | unix 秒 |
| `lastUpdatedTime` | int64 | unix 秒 |
| `wordCount` | int | 服务端按 markdown 重算 |
| `plainContent` | string | 纯文本；列表截 201 字 |
| `mdContent` | string | markdown 原文；**仅 detail 或 withContent=true 返回** |
| `context` | string \| null | 仅搜索命中时的片段（前后各 50 字） |

**Draft**：结构同 Post；`mdContent`/`plainContent`/`wordCount` 可为 `null`。

**Speak**：`{time: int64, content: string, plainContent: string}`

**LinkGroup**：`{name: string, description: string, order: int}`
**Link**：`{id: string, name, url, avatar, description, color, latency: float64}`

**LinkApplication**：

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | ObjectID hex |
| `type` | 0 \| 1 | 0=新增，1=修改 |
| `name` `link` `avatar` `descr` `color` `email` | string | 申请内容 |
| `targetUrl` `targetGroup` `targetId` | string \| null | type=1 的目标定位 |
| `status` | `pending` \| `approved` \| `rejected` | |
| `reason` | string | 拒绝理由 |
| `aiScreening` | object \| null | `{verdict, backlink, summary, reason, model, screenedAt}` |
| `createdAt` `decidedAt` | string | RFC3339（上海时区） |

---

## 3. 接口参考

### 3.1 运维

#### `GET /health`

存活探针。

- 鉴权：公开 ｜ 限流：全局

| 参数 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| – | – | – | – | 无参数 |

**响应** `data`：

| 字段 | 类型 | 说明 |
|---|---|---|
| `status` | string | 恒 `"ok"`（DB 不通则整个请求 503） |
| `db` | string | 恒 `"ok"` |
| `version` | string | 构建版本 |
| `time` | int64 | unix 秒 |

**错误**：DB 不通 → 503 `internal.error`。

> 供负载均衡 / 拨测用；要看依赖详情用 `/status`。

---

#### `GET /status`

状态页：自身运行时 + 各外部依赖可达性。

- 鉴权：公开 ｜ 限流：全局 ｜ 探测总耗时 ≤3s（单项并发，硬超时 3s）

| 参数 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| – | – | – | – | 无参数 |

**响应** `data`：

| 字段 | 类型 | 说明 |
|---|---|---|
| `version` | string | `YYMMDD-短hash`（如 `261027-385532d`）；无构建信息时 `dev` |
| `readOnly` | bool | 是否只读模式（写接口全部 503） |
| `uptimeSeconds` | int64 | 进程存活秒数 |
| `goVersion` | string | 如 `go1.24.1` |
| `goroutines` | int | 当前 goroutine 数 |
| `heapMB` | float | 堆占用（MB） |
| `routeCount` | int | 已注册路由数（56） |
| `stack` | array | `{name, version}`：Go / Fiber / mongo-driver / aws-sdk-go-v2 |
| `checks` | array | 见下 |

`checks[]` 每项：

| 字段 | 类型 | 说明 |
|---|---|---|
| `name` | string | `MongoDB` / `图床 S3` / `邮件 SMTP` / `LLM 摘要` / `GitHub 初筛` |
| `status` | string | `up` / `down` / `disabled`（未启用不是故障） |
| `latencyMS` | int64 | 探测耗时 |
| `detail` | string | down 时为失败原因；disabled 时为关闭原因；up 时通常为空 |

**备注**：探针全是轻量调用（DB `Ping`、S3 `ListObjectsV2 maxKeys=1`、SMTP TCP 握手、LLM `GET /models`、GitHub `/rate_limit`），不发真实业务请求、不耗额度、不发邮件。

---

### 3.2 文章 `/posts`

#### `GET /posts/list`

获取文章列表：分页 + 过滤（分类/标签/归档）+ 全文搜索，一次请求同时返回列表与总数。

- 鉴权：公开 ｜ 限流：全局

| 参数 | 位置 | 类型 | 必填 | 默认 | 说明 |
|---|---|---|---|---|---|
| `page` `pageSize` `countOnly` | query | | 否 | | 见 §1.5 |
| `category` | query | string | 否 | – | 分类精确匹配 |
| `tag` | query | string | 否 | – | 标签包含匹配 |
| `year` | query | int | 否 | – | 0–9999；归档过滤，按上海时区计月 |
| `month` | query | int | 否 | – | 1–12；**必须与 year 同给**，否则 400 |
| `keyword` | query | string | 否 | – | 全文搜索，≤64 字（非正则；匹配规则见下方「搜索行为」） |
| `titleOnly` | query | bool | 否 | false | keyword 只匹配标题 |
| `withContent` | query | bool | 否 | false | 返回 `mdContent`（RSS 全量用） |
| `order` | query | string | 否 | `desc` | `asc`/`desc`，按 `publishTime` |

**响应**：`data=[Post]`（无 mdContent；plainContent 截 201 字）+ `meta`。搜索命中时每项带 `context`，且**标题命中排在正文命中之前**。

**搜索行为**（2026-10-28 增强）：

1. **精确优先**：中文按子串、英文按**整词**（英文语义单位是空格分隔的单词，`fib` 不命中 `fiber`），并带**拼写容错**（≤7 字母容 1 处编辑、≥8 字母容 2 处，如 `verce` → `vercel`）。
2. **部分匹配**：精确无结果时按中文 n-gram 兜底——命中任一**三元组**，或命中**稀有双字组**（出现在 ≤5% 文档里），或累计权重达标；单个常见双字组不足以命中，所以 `网站` 不会被 `网`/`站` 分居命中。
3. **别名/概念词**：配置 `search.synonyms` 支持「查询词 → 别名列表」，别名可为普通词或 `/正则/`，例如 `年份: ['/(19|20)\d{2}\s*年/']` 让「年份」命中写下「2023年」的文章。普通词别名若出现在 >25% 的文档里会被自动忽略（避免 `img.0v0.my` 这类通用串把搜索撑成全站结果）；`/正则/` 别名不受此限——它是显式写的概念规则，天生可能命中很多篇（如「日期」「网址」）。
4. **排序**：原词命中 > 别名命中 > 部分匹配；同档内标题命中前置。

**错误**：`validation.invalid_field`（参数越界/month 缺 year/keyword 超长）。

```
GET /posts/list?category=技术&tag=go&page=2&pageSize=16
GET /posts/list?keyword=fiber&titleOnly=true
```

---

#### `GET /posts/detail`

获取单篇文章完整内容（含 `mdContent`）。后台编辑器也用此接口，故无单独的 content 读接口。

- 鉴权：公开

| 参数 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `slug` | query | string | 是 | 文章标识 |

**响应**：`data=Post`（全字段）。**错误**：404 `post.not_found`。

---

#### `GET /posts/statistics`

站点文章统计。

- 鉴权：公开

**响应**：`data={"totalWordCount":152340}`（后续统计维度在此扩展）。

---

#### `GET /posts/slugs`

全部文章 slug，不分页。

- 鉴权：公开

**响应**：`data=["hello-world", …]`（按 publishTime 倒序，输出稳定）。

---

#### `GET /posts/navigation`

上一篇 / 下一篇。

- 鉴权：公开

| 参数 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `slug` | query | string | 是 | 基准文章 |

**响应**：`data={"newer": PostBrief|null, "older": PostBrief|null}`
（`newer`=更新的一篇、`older`=更旧的一篇；边界为 `null`。旧接口的 `previous/next` 名实相反，新接口已修正，glue 做反向映射。）

**错误**：404 `post.not_found`。

---

#### `GET /posts/related`

相关文章：按共有 tag 随机取样，排除自身。

- 鉴权：公开

| 参数 | 位置 | 类型 | 必填 | 默认 | 说明 |
|---|---|---|---|---|---|
| `slug` | query | string | 是 | | 基准文章 |
| `limit` | query | int | 否 | 6 | 1–20 |

**响应**：`data=[PostBrief]`。文章无 tag 时返回 `data:[]`（旧行为是 404，属修正）。

---

#### `GET /posts/summary`

获取 AI 摘要。缓存优先；未命中时生成一次并落库（每篇最多触发一次）。

- 鉴权：公开 ｜ 上游：SiliconFlow，超时放宽至 90s

| 参数 | 位置 | 类型 | 必填 | 默认 | 说明 |
|---|---|---|---|---|---|
| `slug` | query | string | 是 | | 文章标识 |
| `generate` | query | bool | 否 | true | false=**只读缓存**，绝不触发上游（后台预览用）；miss 时 404 |

**响应**：`data={"summary":"…"}`。**错误**：404 `post.not_found`；502 `upstream.failed`（生成失败/未启用）。

---

#### `GET /posts/semantic`

语义搜索（2026-10-28 落地，bge-m3 + 混合检索）。

- 鉴权：公开 ｜ 上游：SiliconFlow embeddings（`features.semanticSearch=true` 时）

| 参数 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `query` | query | string | 是 | 自然语言或关键词均可 |

**行为按服务端开关自动分流，调用方无需感知**：

| `features.semanticSearch` | 行为 |
|---|---|
| `true`（且向量已回填） | 语义召回（余弦 ≥ `minScore`）∪ 关键词召回，RRF 融合排序 |
| `false` / 向量化失败 | 自动降级为**纯关键词搜索**（与 `GET /posts/list?keyword=` 同源同序） |

**结果分层与排序**（2026-10-28 定）：关键词精确命中 → 关键词模糊命中 → 语义命中，同层内有序：

| 层 | 含义 | `score` |
|---|---|---|
| 1 关键词精确 | 查询作为整段子串命中（中文子串 / 英文整词） | `1.0` |
| 2 关键词模糊 | n-gram 部分匹配、拼写容错、别名/概念词命中 | `0.8` |
| 3 语义 | 向量余弦 ≥ `minScore`，**按余弦从高到低** | 余弦值（0~1）|

同一篇文章只出现一次（关键词命中后不再进语义层）。`score` 单调不增，可直接用于排序/过滤。

**响应**：`data=[{slug,title,bannerImg,publishTime,context,matchType,matchScore,score}]`，**不设 limit**——返回全部命中，翻页由前端实现。

| 字段 | 类型 | 说明 |
|---|---|---|
| `matchType` | string | 匹配类型：`exact`（关键词精确）/ `fuzzy`（模糊/别名）/ `semantic`（语义）|
| `matchScore` | float | 匹配度 0~1：精确 `1.0`、模糊 `0.8`、语义为余弦值 |
| `score` | float | 历史字段，与 `matchScore` 同值（保持兼容）|
| `context` | string \| null | 命中片段；模糊命中为命中的 n-gram 附近，语义命中为命中块开头 120 字 |
`score` 为 RRF 名次分（`1/(60+rank)`，两模式量纲一致）；`context` 为命中片段（语义命中 = chunk 开头 120 字；关键词命中 = searchText 定位片段），前端高亮逻辑对不含字面 query 的片段自动降级为无高亮。

**防「硬匹」**：语义侧余弦低于阈值（默认 0.52，实测校准：真相关 0.53–0.67，无关/硬匹 0.36–0.51）不进结果；全低于阈值时返回纯关键词结果（可能为空数组）——保留「没有就说没有」。

**错误**：400 `validation.missing_field`（query 为空）。向量库未回填时语义路为空，不报错。

---

#### `POST /posts/create`

新建文章（元信息；正文用 `PUT /posts/content` 另写）。

- 鉴权：后台 ｜ 请求体上限 1MB

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `slug` | body | string | 是 | 唯一，冲突 409 |
| `title` | body | string | 是 | |
| `category` | body | string | 否 | |
| `tags` | body | []string | 否 | |
| `bannerImg` | body | string | 否 | |
| `description` | body | string \| null | 否 | |
| `coverFit` | body | string \| null | 否 | |
| `publishTime` | body | int64 | 否 | unix 秒；**缺省自动取当前时间** |
| `lastUpdatedTime` | body | int64 | 否 | 缺省取 publishTime |

**响应**：**201** `data={"slug":"hello-world"}`。**错误**：409 `post.slug_conflict`（唯一索引判重）。

---

#### `PUT /posts/update`

更新文章元信息，**局部更新语义**：只改 body 里出现的字段（没传的不动）。

- 鉴权：后台

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `slug` | body | string | 是 | 定位文章 |
| `title` `category` `bannerImg` | body | string | 否 | 出现才更新 |
| `tags` | body | []string | 否 | |
| `description` `coverFit` | body | string \| null | 否 | |
| `publishTime` `lastUpdatedTime` | body | int64 | 否 | |

**响应**：`data={"updated":true}`。**错误**：404 `post.not_found`。

---

#### `PUT /posts/content`

只更新正文。服务端重算 `wordCount` / `plainContent` / `searchText`。

- 鉴权：后台 ｜ 请求体上限 2MB

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `slug` | body | string | 是 | |
| `markdown` | body | string | 是 | markdown 原文 |

**响应**：`data={"wordCount":1830}`。**错误**：404 `post.not_found`。

---

#### `DELETE /posts/delete`

删除文章。**幂等**：slug 不存在也返回成功。

- 鉴权：后台

| 参数 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `slug` | query | string | 是 | |

**响应**：`data={"deleted":true}`。

---

#### `POST /posts/summary`

强制重新生成 AI 摘要（管理员操作；与 GET 版不同，无视缓存冷却）。

- 鉴权：后台 ｜ 超时放宽 90s

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `slug` | body | string | 是 | |

**响应**：`data={"summary":"…"}`。**错误**：404 `post.not_found`；502 `upstream.failed`。

---

### 3.3 草稿 `/drafts`（全部后台）

草稿与文章对称，差异：`mdContent`/`plainContent`/`wordCount` 可为 `null`；错误码 `draft.*`。

#### `GET /drafts/list`

- 鉴权：后台

| 参数 | 位置 | 类型 | 必填 | 默认 | 说明 |
|---|---|---|---|---|---|
| `page` `pageSize` `countOnly` | query | | 否 | | 同 §1.5（支持 full） |
| `keyword` | query | string | 否 | – | **标题**模糊搜索；空 = 列全部（等价旧 draftsInfo） |
| `withContent` | query | bool | 否 | false | 返回 mdContent |

**响应**：`data=[Draft]` + `meta`。

#### `GET /drafts/detail`

| 参数 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `slug` | query | string | 是 | |

**响应**：`data=Draft`。**错误**：404 `draft.not_found`。

#### `GET /drafts/slugs`

**响应**：`data=[slug]`。

#### `POST /drafts/create`

Body 同 `POST /posts/create`（publishTime 缺省取当前时间）。**响应**：**201** `{"slug":"…"}`。错误：409 `draft.slug_conflict`。

#### `PUT /drafts/update`

Body 同 `PUT /posts/update`（局部更新）。**响应**：`{"updated":true}`。错误：404。

#### `PUT /drafts/content`

Body：`{slug, markdown}`（2MB）。**响应**：`{"wordCount":N}`。错误：404。

#### `DELETE /drafts/delete`

Query：`?slug=`。**响应**：`{"deleted":true}`（幂等）。

---

### 3.4 分类体系

#### `GET /tags/list`

全站标签及各自文章数。

- 鉴权：公开

| 参数 | 位置 | 类型 | 必填 | 默认 | 说明 |
|---|---|---|---|---|---|
| `countOnly` | query | bool | 否 | false | true 时只要种类数 |

**响应**：`data=[{"name":"博客","count":18}]`；`meta.count`=**去重后的标签种类数**（即旧 tagCount）。每项 `count` 是该标签下文章数。

#### `GET /categories/list`

同 `/tags/list`，维度是分类。

#### `GET /archives/list`

归档分桶。

- 鉴权：公开

| 参数 | 位置 | 类型 | 必填 | 默认 | 说明 |
|---|---|---|---|---|---|
| `countOnly` | query | bool | 否 | false | true 时只要月份数 |

**响应**：`data=[{"year":2026,"month":10,"count":7}]` 倒序；`meta.count`=月份桶数。**月份按上海时区划分**（不用 Mongo 的 UTC 聚合）。

---

### 3.5 友链 `/links`

#### `GET /links/list`

友链全量分组。

- 鉴权：公开

| 参数 | 位置 | 类型 | 必填 | 默认 | 说明 |
|---|---|---|---|---|---|
| `countOnly` | query | bool | 否 | false | true=只要友链条目总数 |

**响应**：`data=[LinkGroup]`（组内 `links[]`）；分组按 `order` 升序（缺 order 视为 0，数据库排序）；`meta.count`=**友链条目总数**（不是分组数）。

---

#### `GET /links/theme-color`

从友链头像图提取主题色。

- 鉴权：公开 ｜ 限流：60 次/小时/IP ｜ 响应带 `Cache-Control: public, max-age=86400`

| 参数 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `url` | query | string | 是 | 图片 URL（http/https） |

**响应**：`data={"color":"#c95d7f"}`。
**错误**：400（非 http(s)/内网地址）、415 `image.unsupported_type`、413 `image.too_large`（512KB）、502 `image.unreachable`、429。
安全约束：SSRF 防护（拒绝内网/环回/云元数据地址）、总时限 8s、最多 3 次重定向。

---

#### `POST /links/create`

在分组内新增友链。

- 鉴权：后台

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `group` | body | string | 是 | 目标分组名 |
| `id` | body | string | 是 | 友链 ID（调用方生成） |
| `name` `url` | body | string | 是 | |
| `avatar` `description` `color` | body | string | 否 | |
| `latency` | body | float64 | 否 | 延迟 ms，通常由链路检查回写 |

**响应**：`data={"created":true}`。**错误**：404 `link.not_found`（分组不存在）。

#### `PUT /links/update`

更新友链内容（按 group+id 定位）。

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `group` `id` | body | string | 是 | 定位 |
| `name` `url` `avatar` `description` `color` | body | string | 否 | 新值 |

**响应**：`{"updated":true}`。错误：404 `link.not_found`。

#### `DELETE /links/delete`

| 参数 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `group` | query | string | 是 | |
| `id` | query | string | 是 | |

**响应**：`{"deleted":true}`。错误：404。

#### `POST /links/move`

把友链从一个分组移到另一个分组（原子操作：写入新组 → 从旧组移除，失败回滚）。

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `fromGroup` | body | string | 是 | |
| `toGroup` | body | string | 是 | |
| `id` | body | string | 是 | |

**响应**：`{"moved":true}`。错误：404。

#### `POST /links/groups/create`

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `name` | body | string | 是 | 分组名，唯一 |
| `description` | body | string | 否 | |
| `order` | body | int | 否 | 排序权重 |

**响应**：`{"created":true}`。错误：409 `link.group_conflict`。

#### `PUT /links/groups/update`

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `oldName` | body | string | 是 | 现名 |
| `name` `description` | body | string | 否 | 新值 |
| `order` | body | int | 否 | |

**响应**：`{"updated":true}`。错误：404 / 409。

#### `DELETE /links/groups/delete`

| 参数 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `name` | query | string | 是 | |

**响应**：`{"deleted":true}`。错误：404 `link.not_found`。

#### `POST /links/latency`（内部）

链路检查结果回写。调用方：`lyxofficial/check-flink`（GitHub Actions）。

- 鉴权：**内部**（`X-Internal-Token`；旧实现 secret 放 body 明文，已废弃）

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `items` | body | array | 是 | 非空；每项 `{id:string, latency:float64}` |

**响应**：`data={"updated":12}`（实际更新条数；空 id 的项跳过）。

#### `POST /links/check`（后台）

手动触发外部 `check-flink` 仓库的链路检查 workflow。

- 鉴权：后台（管理员主动动作，失败不静默）

**响应**：`data={"dispatched":true}`。**错误**：502 `upstream.failed`（dispatch 失败或功能未启用）。

---

### 3.6 友链申请 `/links/applications`

#### `POST /links/applications/apply`

提交友链申请（访客功能）。触发 AI 初筛 + 给管理员发通知邮件。

- 鉴权：公开 ｜ 限流：全局 300/min + 本路由 5 次/30 分钟/IP + 待审队列容量 20

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `name` | body | string | 是 | 站点名 |
| `link` | body | string | 是 | 站点 URL（http/https） |
| `avatar` | body | string | 否 | 头像 URL |
| `descr` | body | string | 否 | 一句话介绍（≤200 字） |
| `email` | body | string | 是 | 通知邮箱 |
| `color` | body | string | 否 | 主题色，缺省按 `#888888` 归一化 |
| `type` | body | int | 否 | 0=新增（默认），1=修改已有友链 |
| `targetUrl` | body | string | type=1 必填 | 要修改的已收录站点地址 |

**响应**：`data={"screeningTriggered":true,"mailSent":true}`。

校验与判重：
- XSS 正则过滤、URL/颜色白名单；错误以 `details.fields` 逐字段返回（中文）
- 判重归一化：trim、URL 去尾斜杠、颜色缺省 `#888888`、邮箱小写
- `application.duplicate`：网址已收录，或与现有待审申请逐字段完全相同
- `application.no_change`：type=1 但内容与现状完全一致

**错误**：400 `validation.*` / `application.no_change`、409 `application.duplicate`、429 `application.full` / `rate_limit.exceeded`。

---

#### `GET /links/applications/status`

申请人自助查询进度。**公开的原因**：只返回 email/name 匹配的记录，与旧后端等价。

| 参数 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `email` | query | string | 二选一 | 申请时填的邮箱 |
| `name` | query | string | 二选一 | 申请时的站点名 |

**响应**：`data=[LinkApplication]`，**最多 20 条**。**错误**：两者都不传 → 400（防止枚举所有申请）。

---

#### `GET /links/applications/list`（后台）

待审申请列表。

**响应**：`data=[LinkApplication]`，按 `createdAt` 升序（最早的先审）；`meta.count`=待审总数。

---

#### `POST /links/applications/decide`（后台）

审批申请（通过/拒绝二合一，替代旧 approve/reject 两条接口）。

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `id` | body | string | 是 | 申请 ObjectID |
| `decision` | body | string | 是 | `approved` \| `rejected` |
| `group` | body | string | 否 | approved 时目标分组（type=0 入组；type=1 可借它换组） |
| `overrides` | body | object | 否 | approved 时手工改写申请内容 `{name,url,avatar,description,color}` |
| `reason` | body | string | 否 | rejected 时的拒绝理由（非空才发邮件） |

**响应**：`data={"mailSent":bool}`。
**错误**：404 `application.not_found`（不存在或已处理）、404 `link.not_found`（目标分组/友链不存在）。

审批行为：approved + type=0 → 友链追加进 `group`；approved + type=1 → 用 overrides 覆盖原友链（`targetGroup+targetId` 定位，可选换组）。

---

#### `POST /links/applications/screening`（内部）

AI 初筛结果回写。调用方：`flink-screening` GitHub Actions workflow。

- 鉴权：**内部**

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `id` | body | string | 是 | 申请 ObjectID |
| `verdict` | body | string | 是 | `pass` \| `review` |
| `backlink` | body | string | 否 | 回链地址（≤500 字，超长截断） |
| `summary` | body | string | 否 | 初筛摘要（≤200 字） |
| `reason` | body | string | 否 | 理由（≤300 字） |
| `model` | body | string | 否 | 使用的模型名（≤50 字） |

**响应**：`{"updated":true}`。**错误**：404 `application.not_found`。

---

### 3.7 说说 `/speaks`

#### `GET /speaks/list`

- 鉴权：公开

| 参数 | 位置 | 类型 | 必填 | 默认 | 说明 |
|---|---|---|---|---|---|
| `page` `pageSize` `countOnly` | query | | 否 | | 同 §1.5（支持 full） |

**响应**：`data=[Speak]` 按 `time` 倒序 + `meta`。

#### `POST /speaks/create`（后台）

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `content` | body | string | 是 | |
| `time` | body | int64 | 否 | unix 秒；缺省取当前时间 |

**响应**：`{"created":true}`。

#### `PUT /speaks/update`（后台）

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `time` | body | int64 | 是 | 定位（主键） |
| `content` | body | string | 是 | 新内容 |

**响应**：`{"updated":true}`。错误：404。

#### `DELETE /speaks/delete`（后台）

| 参数 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `time` | query | int64 | 是 | 正的 unix 秒 |

**响应**：`{"deleted":true}`（幂等）。错误：400（格式）。

---

### 3.8 站点 `/site`

#### `GET /site/info`

- 鉴权：公开

**响应**：`data={"latestUpdateTime":1761400000}`（站点最后更新时间，unix 秒）。

#### `POST /site/update-time`（后台）

刷新站点更新时间。**body 整体可选**：不传/`{}`/`{"time":N}` 均合法——不传写当前时间。

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `time` | body | int64 | 否 | 覆写值；缺省 = 当前时间 |

**响应**：`data={"updated":true}`。副作用：fire-and-forget 通知博客站刷缓存（失败不影响本请求）。

---

### 3.9 图床 `/images`（全部后台；`media.enabled=false` 时整组路由不注册，表现为 404）

#### `GET /images/list`

列出图床对象（图片扩展名，按 `lastModified` 倒序）。**不分页，也不支持 full**。

**响应**：`data={"images":["https://img.0v0.my/…"], "items":[{key,size,lastModified,url}]}`。

#### `POST /images/upload`

- 请求体：**multipart/form-data**，上限 4MB

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `file` | form-data | file | 是 | jpeg/png/webp/gif（按扩展名白名单） |

**响应**：`data={"url":"https://img.0v0.my/<key>"}`。
**错误**：415 `image.unsupported_type`、413 `image.too_large`、502 `upstream.failed`。

#### `PUT /images/reupload`

用新文件覆盖旧图（保持原 URL 不变）。

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `url` | form-data | string | 是 | **必须是本图床（publicURL 前缀）的地址**，否则拒绝（防误删别人域名） |
| `file` | form-data | file | 是 | 新图片，格式同 upload |

**响应**：`data={"url":"<原 URL>"}`。

#### `POST /images/import`

把外链图转存进图床。

- 请求体：JSON，上限 1MB（默认档）

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `url` | body | string | 是 | 外链图片地址（http/https；SSRF 校验同 theme-color） |

**响应**：`data={"url":"…"}`
**错误**：400、413（对方图超 3MB）、415、502 `image.unreachable`（下载失败/超时 20s/不是图片）。

#### `DELETE /images/delete`

| 参数 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `url` | query | string | 是 | 本图床的图片地址（前缀校验同上） |

**响应**：`{"deleted":true}`。

---

### 3.10 鉴权 `/auth`

#### `POST /auth/login`

登录换取 JWT。

- 鉴权：公开 ｜ 限流：10 次/10 分钟/IP

| 字段 | 位置 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `user` | body | string | 是 | |
| `password` | body | string | 是 | |

**响应**：`data={"token":"<jwt>"}`（HS256，7 天）。
**错误**：400（缺字段）、403 `auth.locked`（密码错误或连续失败锁定——**同一文案**，不暴露锁定状态）。

#### `GET /auth/verify`

校验当前 token。

- 鉴权：后台

**响应**：`data={"user":"ariasaka","exp":1761400000}`（exp=过期时间 unix 秒）。token 本身无效会在鉴权中间件被拦（401）。

---

### 3.11 其它

#### `GET /sitemap`

站点地图。**全服务唯一的非 JSON 响应**（给爬虫的文档，不是 API）。

- 鉴权：公开

**响应**：`application/xml`，`<urlset>` 含全部文章页 + 首页/说说/友链/关于；`lastmod` 按上海时区格式化（文章取 `lastUpdatedTime`）。

#### `POST /maintenance/export`（内部）

全库导出。调用方：私有仓库 `ariablognext-autobackup`（定时备份到 R2）。

- 鉴权：**内部** ｜ 超时放宽至 120s（扫全库约 2500 文档）

**响应**：`data={"collections":{"Posts":[…],"Speaks":[…],"comment":[…],…}}`
每个集合是一个文档数组，`_id` 为字符串；twikoo 的集合原样带出。集合列表是动态的。

**错误**：502 `upstream.failed`（功能未启用）；501（存储实现不支持）。

---

## 4. 调用注意事项

1. **分页上限**：数字 `pageSize` 1–200；`full` 一次最多 300 条，超限报 `validation.invalid_field` 而不是静默截断。`full` 只对 posts/drafts/speaks 开放。
2. **写接口对「目标不存在」返回 404**（旧后端是静默 200）；删接口幂等返回 true。不要用「删不存在的东西也 200」做存在性探测。
3. **搜索是字面匹配不是正则**：`keyword` 里的 `*` `(` 等元字符不会生效；外挂标签（如 `<Folding>`）不参与命中，只搜标题与正文纯文本；标题命中排在正文命中前。
4. **slug 冲突不会覆盖**：重复 create 得 409，先查 detail 再决定。
5. **元信息更新是局部语义**：`PUT /posts/update` 等只改 body 里出现的字段；想清空可空字段传 `null`。
6. **图床接口的 URL 前缀校验**：reupload/delete 的 `url` 必须以配置的 `publicURL` 开头，操作不了别的域名的图。
7. **限流按 IP**：取 `X-Forwarded-For` 的真实客户端 IP；代理层不要伪造该头。命中时按 `Retry-After` 退避。
8. **`requestId` 贯穿日志**：报告问题附上响应里的 `requestId`，服务端日志能直接对上。
9. **外部调用方迁移对照**（`SECRET` 不变，只改 URL 与请求头）：
   - `check-flink` → `POST /links/latency` + `X-Internal-Token`
   - `flink-screening.yml` → `POST /links/applications/screening` + `X-Internal-Token`
   - `ariablognext-autobackup` → `POST /maintenance/export` + `X-Internal-Token`，读 `json.data.collections`
10. **旧路径不兼容**：`/get/*`、`/update/*`、`/access/*`、`/flink/*` 一律 404。前端走 glue（`blognext-end.yaria.top`）的旧契约；直连必须用本文档路径。
11. **兼容性承诺**：v1.0 内不改已有字段语义、不删字段；新增字段不升版本；破坏性变更升 v2 并保留平行期。

---

## 5. 变更记录

| 日期 | 版本 | 变更 |
|---|---|---|
| 2026-10-28 | v1.0.3 | 关键词匹配增强：中文子串 / 英文整词+拼写容错 / 稀有 n-gram 兜底 / 配置化别名与正则概念词；修复搜索结果 context 与文章错位 |
| 2026-10-28 | v1.0.2 | `/posts/semantic` 落地：bge-m3 向量 + RRF 混合检索，关闭时回退关键词；不再返回 501；`maxPageSize` 语义端点不限个数 |
| 2026-10-28 | v1.0.1 | 文档重写为逐接口规格（参数表/响应/错误码/备注）；无接口行为变更 |
| 2026-10-27 | v1.0 | 首个正式版：56 条路由（含 `/status`）；`maxPageSize` 100→200 |
