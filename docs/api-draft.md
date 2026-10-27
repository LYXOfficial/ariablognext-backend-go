# AriaBlogNext Go 后端 · 接口文档（草稿 v0.3）

> 面向 `ariablognext-backend-go`。前端本阶段不改，由胶水项目
> `ariablognext-backend-glue` 把旧契约翻译到本服务（见 `glue.md`）。
>
> v0.2 → v0.3：**删掉所有 `/xxx/count` 路径**，改为 `list` 默认返回 count + 列表、
> 加 `countOnly=true` 只取数量；`pageSize` 支持 `full`；所有参数统一驼峰；
> 预留语义搜索 stub（`/posts/semantic`）。

---

## 0. 约定

### 0.1 URL 风格

- **没有统一前缀**，路径是「资源 / 细分」两段：`/<资源>/<细分>[/<子细分>]`
- 路径里**不出现标识符**，`slug` / `id` / `name` / `time` 一律走 query
  （顺带解决 slug 里带 `/`、中文、`#` 的转义问题）
- 读 `GET`，写 `POST` / `PUT` / `DELETE`；不用 `PATCH`
- **参数名与字段名统一驼峰**（`pageSize` / `countOnly` / `titleOnly` / `withContent` /
  `fromGroup` / `targetUrl`），且**沿用手头数据库的字段名**，不做下划线翻译——
  胶水层因此几乎不需要字段映射表

### 0.2 统一响应信封（硬约束）

成功：

```json
{
  "ok": true,
  "requestId": "01J9Z3K2Q7",
  "data": [ ],
  "meta": { "page": 1, "pageSize": 16, "count": 83, "hasMore": true }
}
```

失败：

```json
{
  "ok": false,
  "requestId": "01J9Z3K2Q7",
  "error": {
    "code": "internal.error",
    "message": "服务内部错误，请稍后重试",
    "details": { "cause": "server selection timeout: no available servers" }
  }
}
```

规则：

1. **永远带信封**（`/sitemap` 返回 XML 是唯一例外，它是给爬虫的文档不是 API）
2. **不返回 204**。删除成功返回 `200 + {"ok":true,"data":{"deleted":true}}`
3. `data` 允许为 `null`，但键必须存在
4. `meta` 只在列表/分页类接口出现，形状见 §0.4
5. `error.message` 面向人（中文，说清「发生了什么 + 怎么办」）；`error.code` 面向程序，稳定不变
6. **`error.details` 承载真实原因**：内部错误、上游错误的原始报错都放这里，
   不只写日志（个人项目，排查效率优先）。栈信息不返回，只返回错误串

`details` 形状固定为对象：

| 场景 | details |
|---|---|
| 字段校验失败 | `{"fields":[{"field":"link","message":"必须以 http:// 或 https:// 开头"}]}` |
| 内部错误 | `{"cause":"<原始 error 字符串>"}` |
| 上游错误 | `{"cause":"…","upstream":"siliconflow","status":50602}` |
| 无附加信息 | `null` |

### 0.3 错误码表

| code | HTTP | 含义 |
|---|---|---|
| `validation.invalid_field` | 400 | 字段格式不对，细节在 `details.fields` |
| `validation.missing_field` | 400 | 必填缺失 |
| `auth.missing_token` | 401 | 没带 token |
| `auth.invalid_token` | 401 | token 无效/过期 |
| `auth.locked` | 403 | 登录失败过多（与「密码错误」同一文案，不暴露锁定状态）|
| `not_found.route` | 404 | 路径不存在 |
| `post.not_found` | 404 | 文章不存在 |
| `post.slug_conflict` | 409 | 文章 slug 已存在 |
| `draft.not_found` | 404 | 草稿不存在 |
| `draft.slug_conflict` | 409 | 草稿 slug 已存在 |
| `link.not_found` | 404 | 友链/分组不存在 |
| `link.group_conflict` | 409 | 分组名已存在 |
| `application.not_found` | 404 | 申请不存在或已处理 |
| `application.duplicate` | 409 | 网址已收录 / 已有完全相同的待审申请 |
| `application.no_change` | 400 | 修改申请内容与现状完全一致 |
| `application.full` | 429 | 待审队列已满（20 条）|
| `image.unsupported_type` | 415 | 不是受支持的图片格式 |
| `image.too_large` | 413 | 超过体积上限 |
| `image.unreachable` | 502 | 对方站点下载失败/超时/不是图片 |
| `search.not_implemented` | 501 | 语义搜索预留位，尚未实现 |
| `storage.read_only` | 503 | 服务以只读模式运行（连生产库做验证时），写接口一律拒绝 |
| `rate_limit.exceeded` | 429 | 触发限流，带 `Retry-After` |
| `timeout.exceeded` | 504 | 服务端处理超时（默认 8 秒）|
| `upstream.failed` | 502 | 外部服务（SiliconFlow / GitHub / 图床）失败 |
| `internal.error` | 500 | 兜底 |

### 0.4 分页与计数（list 一条接口搞定）

`list` 类接口默认**同时返回列表与总数**，用 `countOnly` 控制要不要列表。

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `page` | int | 1 | 从 1 开始 |
| `pageSize` | int \| `full` | 16 | 1–200；`full` 表示不分页、一次返回全部 |
| `countOnly` | bool | false | 只要数量，不要列表——**省流量模式** |

三种响应形态：

```jsonc
// 默认：count + 列表（一次请求，服务端并行跑 count 与 find）
{ "ok": true, "data": [ … ], "meta": { "page": 1, "pageSize": 16, "count": 83, "hasMore": true } }

// countOnly=true：只有数量，data 为 null
{ "ok": true, "data": null, "meta": { "count": 27 } }

// pageSize=full：全量，不分页
{ "ok": true, "data": [ … ], "meta": { "page": 1, "pageSize": "full", "count": 83, "hasMore": false } }
```

- `meta.count` 是**当前过滤条件下的总数**（与 `list` 共用同一套过滤参数），
  所以「文艺分类有几篇」「搜索命中几条」都是一次请求
- `countOnly=true` 时服务端**不执行 find**，只跑一次 count
- `pageSize=full` 只对 `posts` / `drafts` / `speaks` 开放（其他资源传 `full` 直接
  `validation.invalid_field` 拒绝，避免把图片目录整个拉下来）
- `pageSize=full` 有 **300 条硬上限**：先 count，`count > 300` 时返回
  `validation.invalid_field`（「共 1200 条，超过 300 条上限，请分页拉取」），
  避免某天文章涨到几千条被一次拉爆
- `hasMore = page*pageSize < count`

### 0.5 鉴权

| 场景 | 头 |
|---|---|
| 后台 | `Authorization: Bearer <jwt>`（HS256，7 天）|
| 服务间（GitHub Actions、备份 Action）| `X-Internal-Token: <SECRET>` |

### 0.6 时间与时区（本次修复项）

- 库里时间戳都是 **unix 秒（`int64`）**，无歧义
- 一切「几月」「哪年」固定用 **`Asia/Shanghai`**，通过 `_ "time/tzdata"` 内嵌 + `time.LoadLocation`，
  **不依赖运行环境 `TZ`**
- **不在数据库里分桶**：`/archives/list` 只取 `publishTime` 一列（83 篇、几百字节），在 Go 里分组。
  这样 `$toDate`（UTC）与本地月界的不一致被彻底消灭，换库也不受影响，且查询更快
- 顺带修掉现存 bug：`tagInfo` / `categoryInfo` 排序字段写成不存在的 `date`，等于没排序 → 改按 `publishTime`

### 0.7 限流与体积（一等公民）

| 范围 | 限制 |
|---|---|
| 全局（每 IP） | 300 次 / 分钟 |
| `POST /auth/login` | 10 次 / 10 分钟（现在 5 次，放宽）|
| `POST /links/applications/apply` | 5 次 / 30 分钟（维持）|
| `GET /links/theme-color` | 60 次 / 小时（维持，另带 `Cache-Control: public, max-age=86400`）|
| 请求体（默认） | 1 MB |
| 请求体（正文写） | 2 MB（`PUT /posts/content`、`PUT /drafts/content`）|
| 请求体（图片） | 4 MB（Vercel 平台硬上限 4.5 MB，留余量）|

命中限流返回 `429` + `Retry-After` + `error.code = rate_limit.exceeded`。
进程内实现（与现状等价），多实例下按实例独立——写在注释里，不假装是全局限流。

---

## 1. 数据结构

### 1.1 Post / Draft

| 字段 | 类型 | 说明 |
|---|---|---|
| `slug` | string | 唯一（有 unique index）|
| `title` | string | |
| `category` | string | 单值 |
| `tags` | []string | |
| `bannerImg` | string | 封面 |
| `description` | string \| null | 人工摘要，48/83 篇为空 |
| `coverFit` | string \| null | 封面 `object-position` |
| `publishTime` | int64 | unix 秒 |
| `lastUpdatedTime` | int64 | unix 秒 |
| `wordCount` | int \| null | 草稿里 4/8 条缺 |
| `plainContent` | string | 列表里截断到 201 字（存量数据本来就截断了，见 `design-notes.md §2.5`）|
| `mdContent` | string | 仅 `detail` 或 `withContent=true` 返回 |
| `context` | string \| null | 仅搜索命中时返回的片段 |

### 1.2 Speak

`{time:int64, content:string, plainContent:string}`

### 1.3 LinkGroup / Link

```
LinkGroup { name, description, order }
Link      { id, name, url, avatar, description, color, latency }
```

### 1.4 LinkApplication

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | ObjectID 字符串 |
| `type` | 0 \| 1 | 0=新增，1=修改 |
| `name` `link` `avatar` `descr` `color` `email` | string | 申请内容 |
| `targetUrl` `targetGroup` `targetId` | string \| null | 修改申请的目标 |
| `status` | `pending` \| `approved` \| `rejected` | |
| `reason` | string | 拒绝理由 |
| `aiScreening` | object \| null | `{verdict, backlink, summary, reason, model, screenedAt}` |
| `createdAt` `decidedAt` | RFC3339（上海时区） | 库里是 `Date` |

---

## 2. 接口总表（56 条，旧 72 条）

| # | 方法 | 路径 | 鉴权 | 替代的旧接口 |
|---|---|---|---|---|
| 1 | GET | `/posts/list` | 公开 | postsInfo / searchPosts / searchPostsByTitle(+Count) / tagInfo / categoryInfo / archiveInfo / postCount |
| 2 | GET | `/posts/detail` | 公开 | postBySlug |
| 3 | GET | `/posts/statistics` | 公开 | totalWordCount |
| 4 | GET | `/posts/slugs` | 公开 | postSlugs |
| 5 | GET | `/posts/navigation` | 公开 | postNavigation |
| 6 | GET | `/posts/related` | 公开 | relatedPosts |
| 7 | GET | `/posts/summary` | 公开 | postSummary（缓存优先，miss 生成一次）|
| 8 | POST | `/posts/summary` | 后台 | refreshSummary（强制重生成）|
| 9 | POST | `/posts/create` | 后台 | addPost |
| 10 | PUT | `/posts/update` | 后台 | updatePostInfo |
| 11 | PUT | `/posts/content` | 后台 | updatePostMarkdown |
| 12 | DELETE | `/posts/delete` | 后台 | deletePost |
| 13 | GET | `/posts/semantic` | 公开 | （预留 stub，返回 501）|
| 14 | GET | `/drafts/list` | 后台 | draftsInfo / searchDraftsByTitle(+Count) / draftCount |
| 15 | GET | `/drafts/detail` | 后台 | draftBySlug |
| 16 | GET | `/drafts/slugs` | 后台 | draftSlugs |
| 17 | POST | `/drafts/create` | 后台 | addDraft |
| 18 | PUT | `/drafts/update` | 后台 | updateDraftInfo |
| 19 | PUT | `/drafts/content` | 后台 | updateDraftMarkdown |
| 20 | DELETE | `/drafts/delete` | 后台 | deleteDraft |
| 21 | GET | `/tags/list` | 公开 | tags / tagCount |
| 22 | GET | `/categories/list` | 公开 | categories / categoryCount |
| 23 | GET | `/archives/list` | 公开 | archives |
| 24 | GET | `/links/list` | 公开 | flinks / flinkCount |
| 25 | GET | `/links/theme-color` | 公开 | themeColor |
| 26 | POST | `/links/create` | 后台 | addFlink |
| 27 | PUT | `/links/update` | 后台 | updateFlink |
| 28 | DELETE | `/links/delete` | 后台 | deleteFlink |
| 29 | POST | `/links/move` | 后台 | moveFlinkGroup |
| 30 | POST | `/links/latency` | 内部 | pushFlinkStatus |
| 31 | POST | `/links/check` | 后台 | dispatchCheckLatencyWorkflow |
| 32 | POST | `/links/groups/create` | 后台 | addGroup |
| 33 | PUT | `/links/groups/update` | 后台 | updateGroup |
| 34 | DELETE | `/links/groups/delete` | 后台 | deleteGroup |
| 35 | POST | `/links/applications/apply` | 公开 | /flink/apply |
| 36 | GET | `/links/applications/status` | 公开 | /flink/apply/status |
| 37 | GET | `/links/applications/list` | 后台 | update/flink/applications |
| 38 | POST | `/links/applications/decide` | 后台 | approveApplication + rejectApplication |
| 39 | POST | `/links/applications/screening` | 内部 | /flink/screening |
| 40 | GET | `/speaks/list` | 公开 | speaks / speaksCount |
| 41 | POST | `/speaks/create` | 后台 | newSpeaks |
| 42 | PUT | `/speaks/update` | 后台 | updateSpeaks |
| 43 | DELETE | `/speaks/delete` | 后台 | deleteSpeaks |
| 44 | GET | `/site/info` | 公开 | lastUpdateTime |
| 45 | POST | `/site/update-time` | 后台 | latestUpdateTime |
| 46 | GET | `/images/list` | 后台 | listImages |
| 47 | POST | `/images/upload` | 后台 | uploadImage |
| 48 | PUT | `/images/reupload` | 后台 | reuploadImage |
| 49 | POST | `/images/import` | 后台 | rehostImageByUrl |
| 50 | DELETE | `/images/delete` | 后台 | deleteImage |
| 51 | POST | `/auth/login` | 公开 | /access/user/login |
| 52 | GET | `/auth/verify` | 后台 | /access/user/verify |
| 53 | GET | `/sitemap` | 公开 | sitemapGen |
| 54 | GET | `/health` | 公开 | （新增）|
| 55 | GET | `/status` | 公开 | （新增，2026-10-27）状态页：版本/技术栈/依赖可达性，统一信封 200 |
| 55 | POST | `/maintenance/export` | 内部 | get/export/collections |

---

## 3. 接口详情

### 3.1 posts

#### `GET /posts/list`

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `page` | int | 1 | |
| `pageSize` | int \| `full` | 16 | 1–200，或 `full` |
| `countOnly` | bool | false | 只要数量 |
| `category` | string | – | 精确匹配 |
| `tag` | string | – | `tags` 数组包含 |
| `year` | int | – | 与 `month` 组合，按 `Asia/Shanghai` 计月 |
| `month` | int | – | 1–12；给 `month` 必须给 `year` |
| `keyword` | string | – | 全文搜索，见 §3.1.1 |
| `titleOnly` | bool | false | `keyword` 只匹配标题 |
| `withContent` | bool | false | 是否返回 `mdContent`（RSS 用）|
| `order` | string | `desc` | `asc` / `desc`，按 `publishTime` |

**200**

```json
{
  "ok": true,
  "requestId": "01J9Z3K2Q7",
  "data": [
    {
      "slug": "hello-world", "title": "你好，世界", "category": "随笔",
      "tags": ["博客"], "bannerImg": "https://img.0v0.my/…", "description": null,
      "coverFit": null, "publishTime": 1759000000, "lastUpdatedTime": 1759000000,
      "wordCount": 1830, "plainContent": "从今天开始写博客…", "context": null
    }
  ],
  "meta": { "page": 1, "pageSize": 16, "count": 83, "hasMore": true }
}
```

`countOnly=true` 时：

```json
{ "ok": true, "requestId": "01J9Z3K2Q7", "data": null, "meta": { "count": 83 } }
```

#### 3.1.1 搜索行为

- 搜索走**写入时维护的 `searchText` 字段**（标题 + 全文纯文本，小写，去 Markdown 语法），
  查询只投影 `slug/title/publishTime/bannerImg/searchText`，
  **不再像现在这样把 83 篇 `mdContent`（平均 8.5 KB、最长 140 KB）全量拉出来在应用层剥 Markdown**
- `keyword` 先 `regexp.QuoteMeta` 转义，长度限制 64，空串等于不过滤
- 排序：`publishTime desc`；**标题命中排在正文命中之前**
- `context` 取命中位置前后各 50 字，找不到就不给
- 接口保持在 `Searcher` 抽象后面，以后接向量搜索 / Atlas Search 不改上层

#### `GET /posts/detail`

`?slug=` → **200** `data` = 完整 Post（含 `mdContent`、`plainContent`）
**错误**：`post.not_found`

> 后台编辑器也用这个接口，所以不单独提供 `GET /posts/content`。
> `PUT /posts/content` 保留，用于「只改正文」。

#### `GET /posts/statistics`

**200** `data = {"totalWordCount": 152340}`
（预留扩展位：以后要平均字数、分类分布都加这里）

#### `GET /posts/slugs`

**200** `data = ["hello-world", "…"]`（83 条，不分页）

#### `GET /posts/navigation`

`?slug=` → **200**

```json
{ "ok": true, "data": {
    "newer": { "slug": "…", "title": "…", "bannerImg": "…", "publishTime": 1759 },
    "older": null
} }
```

> 命名修正：旧接口的 `previous` 其实是**更新**的那篇、`next` 是**更旧**的那篇，名字与行为相反。
> 新接口用 `newer` / `older`，胶水层做反向映射。

#### `GET /posts/related`

`?slug=&limit=`（默认 6，最大 20）→ 按共有 tag 随机取样，排除自身。
**200** `data = [PostBrief]`；文章无 tag 时返回 `data: []`（旧接口返回 404，属于行为修正）

#### `GET /posts/summary`（公开）

`?slug=` → **200** `data = {"summary": "…"}`
缓存优先；没有缓存时生成一次并落库（每篇最多触发一次），与旧 `get/post/postSummary` 一致。
旧后台「只读缓存」的 `update/post/summary` 合并到这里。
**错误**：`post.not_found`、`upstream.failed`

#### `POST /posts/summary`（后台）

**Body** `{"slug":"hello-world"}` → **200** `data = {"summary":"…"}`（强制重生成）

#### `POST /posts/create`（后台）

```json
{ "slug": "hello-world", "title": "你好", "category": "随笔",
  "tags": ["博客"], "bannerImg": "https://…", "description": null,
  "coverFit": null, "publishTime": 1759000000, "lastUpdatedTime": 1759000000 }
```

**201** `data = {"slug":"hello-world"}`；**错误**：`post.slug_conflict`

#### `PUT /posts/update`（后台）

`slug` + 可改字段 → **200** `data = {"updated":true}`；**错误**：`post.not_found`

#### `PUT /posts/content`（后台）

`{"slug":"hello-world","markdown":"# 你好\n…"}`，服务端重算 `wordCount` / `plainContent` / `searchText`。
**200** `data = {"wordCount":1830}`

#### `DELETE /posts/delete`（后台）

`?slug=` → **200** `data = {"deleted":true}`（slug 不存在也返回 true，幂等）

#### `GET /posts/semantic`（预留 stub）

> **这一版不实现**，只是占位，避免以后加路由时前端/胶水再改一次契约。

| 参数 | 类型 | 说明 |
|---|---|---|
| `query` | string | 自然语言查询 |
| `limit` | int | 默认 6 |

**501**

```json
{ "ok": false, "requestId": "…",
  "error": { "code": "search.not_implemented", "message": "语义搜索还没做，先用关键词搜索", "details": null } }
```

落地时的预留设计（写进 `design-notes.md §2.6`）：
- `Posts.embedding []float32` + `EmbeddingMeta{model, dim, updatedAt}`
- `store.SemanticSearcher` 可选接口，与现有 `Searcher` 并列
- `cmd/build-embeddings` 增量重建
- 配置 `EMBEDDING_MODEL` / `EMBEDDING_API`，缺省时该路由返回 501

---

### 3.2 drafts（全部需要后台鉴权）

结构与 posts 对称，差异：

- `mdContent` / `plainContent` / `wordCount` 可为空（库里 4/8 条没有），模型用可空类型，**不要用零值伪造**
- `GET /drafts/list` 支持 `keyword`（标题模糊），等价旧 `searchDraftsByTitle`；
  空 `keyword` 即旧 `draftsInfo` 的「列全部」；`countOnly=true` 即旧 `draftCount` / `searchDraftsByTitleCount`
- `GET /drafts/slugs` 保留（等价 `draftSlugs`）

| # | 方法 | 路径 | 参数 / Body | 返回 |
|---|---|---|---|---|
| 14 | GET | `/drafts/list` | `page` `pageSize`(`full`) `countOnly` `keyword` | `data=[DraftBrief]` + `meta` |
| 15 | GET | `/drafts/detail` | `?slug=` | `data=Draft` |
| 16 | GET | `/drafts/slugs` | – | `data=[slug]` |
| 17 | POST | `/drafts/create` | 同 posts/create | `201` |
| 18 | PUT | `/drafts/update` | 同 posts/update | `{"updated":true}` |
| 19 | PUT | `/drafts/content` | `{slug, markdown}` | `{"wordCount":N}` |
| 20 | DELETE | `/drafts/delete` | `?slug=` | `{"deleted":true}` |

---

### 3.3 tags / categories / archives

| # | 方法 | 路径 | 参数 | 返回 |
|---|---|---|---|---|
| 21 | GET | `/tags/list` | `countOnly` | `data=[{"name":"博客","count":18}]` + `meta={count:42}` |
| 22 | GET | `/categories/list` | `countOnly` | `data=[{"name":"随笔","count":12}]` + `meta={count:9}` |
| 23 | GET | `/archives/list` | `countOnly` | `data=[{"year":2026,"month":10,"count":7}]`，倒序，上海时区 |

> 标签/分类的 `meta.count` 是**去重后的种类数**（旧 `tagCount` / `categoryCount`）；
> 每项的 `count` 是该标签/分类下的文章数。

---

### 3.4 links

#### `GET /links/list`

| 参数 | 说明 |
|---|---|
| `countOnly` | 只要友链总数（旧 `flinkCount`）|

```json
{ "ok": true, "data": [
  { "name": "友达", "description": "…", "order": 0,
    "links": [ { "id": "a1b2c3d4", "name": "某站", "url": "https://x.example",
                 "avatar": "https://…", "description": "…", "color": "#f08", "latency": 32.5 } ] }
], "meta": { "count": 27 } }
```

`meta.count` = **友链条目总数**（不是分组数）。分组按 `order` 升序，**排序由数据库完成**；
缺 `order` 的分组按 0 处理。

#### `GET /links/theme-color?url=`

**200** `data = {"color":"#c95d7f"}`，带 `Cache-Control: public, max-age=86400`
**错误**：`validation.invalid_field`（非 http(s)/指向内网）、`image.unreachable`、
`image.too_large`、`image.unsupported_type`、`rate_limit.exceeded`
> SSRF 防护、8 秒总时限、最多 3 次重定向、512KB 上限全部保留。

#### `POST /links/create` / `PUT /links/update` / `DELETE /links/delete`

- create：`{group,id,name,url,avatar,description,color,latency}`
- update：`{group,id,name,url,avatar,description,color}`
- delete：`?group=&id=`
**错误**：`link.not_found`

#### `POST /links/move`

`{fromGroup,toGroup,id}` → `data={"moved":true}`
> 下沉为仓储原子方法：Mongo 侧「push 新组 → pull 旧组 → 失败回滚」，SQL 侧用事务。

#### `POST /links/latency`（内部 token）

`{"items":[{"id":"a1b2c3d4","latency":32.5}]}` → `data={"updated":12}`
> 旧实现 secret 放 body 明文，改成 `X-Internal-Token` 头。

#### `POST /links/check`（后台）

触发 GitHub Actions 链路检查 → `data={"dispatched":true}`；失败 `upstream.failed`

#### `POST /links/groups/create` / `PUT /links/groups/update` / `DELETE /links/groups/delete`

- create `{name,description,order}`；update `{oldName,name,description,order}`；delete `?name=`
- **错误**：`link.group_conflict`（重名）、`link.not_found`

---

### 3.5 links/applications

#### `POST /links/applications/apply`（公开）

```json
{ "name": "站点名", "link": "https://x.example", "avatar": "https://…",
  "descr": "一句话介绍", "email": "a@b.c", "color": "#f08",
  "type": 0, "targetUrl": "" }
```

**200** `data = {"screeningTriggered": true, "mailSent": true}`
**错误**：`validation.*`、`application.duplicate`、`application.no_change`、
`application.full`、`rate_limit.exceeded`
> XSS 正则、URL/颜色白名单、判重归一化（trim / 去尾斜杠 / 颜色缺省 `#888888` / 邮箱小写）全部保留。

#### `GET /links/applications/status`

`?email=&name=`（至少一个）→ `data = [LinkApplication]`，最多 20 条

#### `GET /links/applications/list`（后台）

`data = [LinkApplication]`，按 `createdAt` 升序（最早的先审），`meta.count` 为待审总数

#### `POST /links/applications/decide`（后台）

```json
{ "id": "66f0…", "decision": "approved",
  "group": "友达",
  "overrides": { "name": "…", "link": "…", "avatar": "…", "descr": "…", "color": "#f08" },
  "reason": "" }
```

- `approved`：`type=0` 追加到 `group`；`type=1` 覆盖原友链（允许借 `group` 换组）
- `rejected`：写 `reason`，非空才发邮件
**200** `data = {"mailSent": true}`
**错误**：`application.not_found`、`link.not_found`

#### `POST /links/applications/screening`（内部 token）

`{id,verdict:"pass"|"review",backlink,summary,reason,model}` → `data={"updated":true}`
**错误**：`application.not_found`

---

### 3.6 speaks

| # | 方法 | 路径 | 参数 / Body | 返回 |
|---|---|---|---|---|
| 40 | GET | `/speaks/list` | `page` `pageSize`(`full`) `countOnly` | `data=[Speak]` + `meta`，`time` 倒序 |
| 41 | POST | `/speaks/create` | `{time,content}` | `{"created":true}` |
| 42 | PUT | `/speaks/update` | `{time,content}` | `{"updated":true}` |
| 43 | DELETE | `/speaks/delete` | `?time=` | `{"deleted":true}` |

---

### 3.7 site

#### `GET /site/info` → `data = {"latestUpdateTime": 1761400000}`

#### `POST /site/update-time`（后台）→ 写当前时间戳 + fire-and-forget 通知博客站刷缓存

---

### 3.8 images

| # | 方法 | 路径 | 说明 | 返回 |
|---|---|---|---|---|
| 46 | GET | `/images/list` | 列出图床文件（按日期倒序），**不支持 `pageSize=full`** | `data={"images":[...]}` |
| 47 | POST | `/images/upload` | `multipart: file`（≤4MB）| `data={"url":"…"}` |
| 48 | PUT | `/images/reupload` | `multipart: url + file` | `data={"url":"…"}` |
| 49 | POST | `/images/import` | `{"url":"https://外链/图.png"}` 转存 | `data={"url":"…"}` |
| 50 | DELETE | `/images/delete` | `?url=` | `data={"deleted":true}` |

**错误**：`image.unsupported_type` / `image.too_large` / `image.unreachable` / `upstream.failed`

---

### 3.9 auth / 其它

#### `POST /auth/login` → `data = {"token":"<jwt>"}`；错误 `auth.locked`

#### `GET /auth/verify` → `data = {"user":"ariasaka","exp":1761400000}`

#### `GET /sitemap`

返回 `application/xml`（唯一的非 JSON 响应），上海时区格式化 `lastmod`。

#### `GET /health` → `data = {"status":"ok","db":"ok","version":"<sha>","time":…}`；DB 不通 503

#### `POST /maintenance/export`（内部 token，**必须保留**）

调用方：**私有仓库 `ariablognext-autobackup`**——定时调用它，把返回的 `collections`
拆成 `exported_collections/<集合名>.json`（每个文件是一个文档数组，`_id` 是字符串）。
鉴权就是 `SECRET`，走 `X-Internal-Token` 头。

**200** `data = {"collections": {"Posts":[…], "Speaks":[…], "comment":[…], …}}`

- `collections` 内部结构与旧接口**完全一致**（数组、`_id` 字符串、twikoo 的
  `comment`/`config` 保持自己的 32 位 hex 字符串主键），只是外面多了一层信封
- 这条路由单独放宽超时（`config.yml` 的 `timeoutOverrides` 配 120s），
  因为它要扫全库（约 2500 条文档）
- 集合列表是动态的：`Keep` 已删，导出里不会再出现它

> ⚠️ 备份仓库要改三处：URL、鉴权头、读 `json.data.collections`；
> 它的「期望集合」清单里还要去掉 `Keep`（同步改 `r2backup/import_collections.py` 的 `EXPECTED`）。

---

## 4. 废弃 / 合并清单

### 4.1 直接删除

| 旧接口 | 依据 |
|---|---|
| `DELETE /update/post/deleteRenderedHtmlCache` | 前后端均无调用；`cachedHtml` 在 83 篇里 82 篇为 `null` |
| `DELETE /update/post/deleteAllRenderedHtmlCache` | 同上 |
| `DELETE /update/draft/deleteRenderedHtmlCache` | 同上 |

> `draftsInfo`、`draftSlugs` 保留（成为 `/drafts/list`、`/drafts/slugs`）。

### 4.2 合并

| 旧接口 | 合并到 |
|---|---|
| `postsInfo` / `searchPosts` / `searchPostsByTitle` / `tagInfo` / `categoryInfo` / `archiveInfo` / `postCount` | `GET /posts/list`（`count` 在 `meta`，`countOnly` 取纯数量）|
| `searchPostsByTitleCount` / `speaksCount` / `flinkCount` / `draftCount` / `searchDraftsByTitleCount` / `tagCount` / `categoryCount` | 对应 `list?countOnly=true` |
| `approveApplication` + `rejectApplication` | `POST /links/applications/decide`（`decision` 字段）|

### 4.3 不能删（调用方在别的仓）

| 旧接口 | 调用方 | 迁移成本 |
|---|---|---|
| `POST /flink/screening` | `.github/workflows/flink-screening.yml` | 改 URL + 鉴权头（`SECRET` 不变）|
| `POST /update/flink/pushFlinkStatus` | 仓库 `lyxofficial/check-flink` | 同上 |
| `POST /get/export/collections` | `ariablognext-autobackup`（私有仓库）| 改 URL + 鉴权头 + 读 `data.collections`；期望集合列表去掉 `Keep` |

### 4.4 顺带修复的既有问题

| 位置 | 问题 | 处理 |
|---|---|---|
| `tagInfo` / `categoryInfo` | 按不存在的 `date` 排序 | 改按 `publishTime` |
| `addPost` / `addDraft` | 先 `count` 再 `insert`，并发下会写入两条同 slug | unique index + 重复键 → `post.slug_conflict` |
| `flinks` | 全量取出后在应用层 `sort` | 交给数据库 `sort({order:1})` |
| `searchPosts` | 全量拉 `mdContent` 后在应用层剥 Markdown | 新增 `searchText` 字段 + 只投影必要列 |
| `Speaks` | 缺 `time` 索引 | **已加** `time_-1` |
| `Keep` 集合 | 0 文档的历史遗留 | **已删除**（备份 `/tmp/aria-keep-backup-20261026-225016.json`）|
| `FLinkApplications` | 无索引 | 数据量小（33 条），**按你的意见不动** |

---

## 5. 已确认 / 遗留

### 5.1 已定

| 事项 | 结论 |
|---|---|
| `/maintenance/export` 信封 | **不开例外**，统一信封。`ariablognext-autobackup` 在胶水下线时一并改（URL + 鉴权头 + 读 `data.collections`）|
| `pageSize=full` 上限 | **300 条**，超限报 `validation.invalid_field` |
| `searchText` 回填 | 可以改库；性能要求见 `design-notes.md §2.5` |
| 语义搜索 stub | **挂上路由**，固定 501 |
| 配置来源 | 全部集中到 `config.yml`，敏感值写成 `{{ env.XXX }}`，程序不直读 env（见 `design-notes.md §3`）|

### 5.2 遗留（迁移期要做的三件事，不在本服务内）

1. `ariablognext-autobackup`（私有）：改 URL + 鉴权头 + 改读 `data.collections`，并把 `Keep` 从期望集合里去掉（**胶水下线时**做）
2. `lyxofficial/check-flink`：改 URL + 请求头改用 `X-Internal-Token`
3. `.github/workflows/flink-screening.yml`：同上

这三处都只是一两行改动，`SECRET` 值本身不变。
