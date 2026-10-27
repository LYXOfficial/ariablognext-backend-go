# AriaBlogNext Backend API · v1.0（正式版）

> 生效日期：2026-10-27 ｜ 服务：`ariablognext-backend-go` ｜ 56 条路由
>
> 本文档是**对外承诺**：字段名、错误码、状态码一经发布不再随意变动；
> 加字段只会向后兼容地加，改语义必须升版本。设计依据与历史讨论见 `api-draft.md`。

- **生产入口（经 glue）**：`https://blognext-end.yaria.top`（旧路径，前端/第三方用这个）
- **本服务直连**：`https://neoend.yaria.top`（本文档描述的是这里的原生接口）
- 本地开发：`http://127.0.0.1:2333`

---

## 1. 通用约定

### 1.1 风格

- 路径是 `/<资源>/<细分>` 两段式，**没有版本前缀**（本文档即 v1 契约本身）
- **路径里不出现标识符**：`slug` / `id` / `name` / `time` 一律走 query，
  天然规避 slug 里 `/`、中文、`#` 的转义问题
- 读 `GET`，写 `POST` / `PUT` / `DELETE`；不用 `PATCH`
- 参数名与字段名统一**驼峰**，与数据库字段名一致，不做下划线翻译

### 1.2 统一响应信封（硬约束）

成功（HTTP 200；`POST */create` 为 201）：

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
  "error": {
    "code": "post.not_found",
    "message": "没有找到这篇文章",
    "details": null
  }
}
```

规则：

1. 所有接口都带信封。**唯一例外** `GET /sitemap` 返回 `application/xml`（给爬虫的文档，不是 API）
2. 没有 204——删除成功也是 `200 + {"ok":true,"data":{"deleted":true}}`
3. `data` 允许为 `null`，但**键必须存在**（`details` 同理，无附加信息时输出 `null`）
4. `meta` 只出现在列表/分页类接口
5. `error.message` 面向人（中文，说清「发生了什么 + 怎么办」）；`error.code` 面向程序，**稳定不变**
6. `error.details` 承载真实原因（内部/上游错误的原始报错串），排查效率优先；不返回栈信息

`details` 的三种形状：

| 场景 | details |
|---|---|
| 字段校验失败 | `{"fields":[{"field":"link","message":"必须以 http:// 或 https:// 开头"}]}` |
| 内部错误 | `{"cause":"<原始 error 字符串>"}` |
| 上游错误 | `{"cause":"…","upstream":"siliconflow","status":50602}` |

### 1.3 错误码总表

| code | HTTP | 含义 |
|---|---|---|
| `validation.invalid_field` | 400 | 字段格式不对，细节在 `details.fields` |
| `validation.missing_field` | 400 | 必填缺失 |
| `auth.missing_token` | 401 | 没带 token |
| `auth.invalid_token` | 401 | token 无效/过期 |
| `auth.locked` | 403 | 登录失败过多（与密码错误同一文案，不暴露锁定状态） |
| `not_found.route` | 404 | 路径不存在（旧后端路径已全部下线，不会静默兼容） |
| `post.not_found` / `draft.not_found` | 404 | 文章/草稿不存在 |
| `post.slug_conflict` / `draft.slug_conflict` | 409 | slug 已存在（唯一索引判重，无竞态） |
| `link.not_found` | 404 | 友链/分组不存在 |
| `link.group_conflict` | 409 | 分组名已存在 |
| `application.not_found` | 404 | 申请不存在或已处理 |
| `application.duplicate` | 409 | 网址已收录 / 已有完全相同的待审申请 |
| `application.no_change` | 400 | 修改申请与现状完全一致 |
| `application.full` | 429 | 待审队列已满（20 条） |
| `image.unsupported_type` | 415 | 不是受支持的图片格式 |
| `image.too_large` | 413 | 超过体积上限 |
| `image.unreachable` | 502 | 对方站点下载失败/超时/不是图片 |
| `search.not_implemented` | 501 | 语义搜索预留位（`/posts/semantic` 恒 501） |
| `storage.read_only` | 503 | 服务以只读模式运行（生产只读验证），写接口一律拒绝 |
| `rate_limit.exceeded` | 429 | 触发限流，响应带 `Retry-After`（秒） |
| `timeout.exceeded` | 504 | 服务端处理超时 |
| `upstream.failed` | 502 | 外部服务（SiliconFlow / GitHub / 图床）失败 |
| `internal.error` | 500 | 兜底 |

### 1.4 鉴权

| 场景 | 请求头 |
|---|---|
| 公开接口 | 无 |
| 后台接口（下表标「后台」） | `Authorization: Bearer <jwt>`（HS256，7 天有效，`POST /auth/login` 获取） |
| 服务间调用（标「内部」） | `X-Internal-Token: <SECRET>`（与 JWT 共用同一密钥） |

token 无效/缺失 → 401 `auth.missing_token` / `auth.invalid_token`。

### 1.5 分页与计数

`list` 类接口**一次请求同时返回列表与总数**，`countOnly=true` 则只取数量（服务端不执行 find）：

```
默认：            data=[…], meta={page, pageSize, count, hasMore}
countOnly=true：  data=null, meta={count}
pageSize=full：   全量一次返回（仅 posts / drafts / speaks 支持）
```

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `page` | int | 1 | 从 1 开始 |
| `pageSize` | int \| `full` | 16 | 1–200，或 `full` |
| `countOnly` | bool | false | 只要数量 |

- `meta.count` 是**当前过滤条件下的总数**（「某分类几篇」「搜索命中几条」都是一次请求）
- `pageSize=full` 有 **300 条硬上限**，超过报 `validation.invalid_field`
- `hasMore = page*pageSize < count`

### 1.6 限流与体积

| 范围 | 限制 |
|---|---|
| 全局（每 IP） | 300 次 / 分钟 |
| `POST /auth/login` | 10 次 / 10 分钟 |
| `POST /links/applications/apply` | 5 次 / 30 分钟 |
| `GET /links/theme-color` | 60 次 / 小时 |
| 请求体（默认） | 1 MB |
| 请求体（正文写入） | 2 MB（`PUT /posts/content`、`PUT /drafts/content`） |
| 请求体（图片上传） | 4 MB |

命中限流：`429 + Retry-After + rate_limit.exceeded`。限流为进程内实现，多实例按实例独立。

### 1.7 时间与时区

- 所有时间戳都是 **unix 秒（int64）**，无时区歧义
- 一切「哪年哪月」的划分固定按 **`Asia/Shanghai`**（tzdata 内嵌，不依赖容器 TZ）

---

## 2. 数据结构

**Post**（列表项不含 `mdContent`；`plainContent` 截断到 201 字）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `slug` | string | 唯一 |
| `title` `category` | string | category 单值 |
| `tags` | []string | |
| `bannerImg` | string | 封面 URL |
| `description` `coverFit` | string \| null | |
| `publishTime` `lastUpdatedTime` | int64 | unix 秒 |
| `wordCount` | int | |
| `plainContent` | string | 纯文本（列表截 201 字） |
| `mdContent` | string | 仅 `detail` 或 `withContent=true` 返回 |
| `context` | string \| null | 仅搜索命中时的片段（前后各 50 字） |

**Draft**：结构同 Post；`mdContent` / `plainContent` / `wordCount` 可为 `null`（缺省不伪造零值）。

**Speak**：`{time: int64, content: string, plainContent: string}`

**LinkGroup / Link**：`LinkGroup {name, description, order}`；`Link {id, name, url, avatar, description, color, latency}`

**LinkApplication**：`{id, type(0新增|1修改), name, link, avatar, descr, color, email, targetUrl, targetGroup, targetId, status(pending|approved|rejected), reason, aiScreening, createdAt, decidedAt}`（时间为 RFC3339 上海时区）

---

## 3. 接口详情

### 3.1 文章 `/posts`

#### `GET /posts/list`（公开）

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `page` `pageSize` `countOnly` | | | 见 §1.5 |
| `category` | string | – | 精确匹配 |
| `tag` | string | – | tags 数组包含 |
| `year` / `month` | int | – | 归档过滤，按上海时区计月；`month` 必须与 `year` 同给 |
| `keyword` | string | – | 全文搜索（≤64 字符），见下 |
| `titleOnly` | bool | false | keyword 只匹配标题 |
| `withContent` | bool | false | 返回 `mdContent`（RSS 用） |
| `order` | string | `desc` | 按 `publishTime` 升/降序 |

**搜索行为**：命中写入时维护的 `searchText`（标题+正文纯文本）；用户输入按字面子串匹配（非正则）；**标题命中排在正文命中之前**；命中项带 `context` 片段（前后各 50 字）。

#### 其它读接口（公开）

| 接口 | 参数 | 返回 / 说明 |
|---|---|---|
| `GET /posts/detail` | `?slug=` | 完整 Post（含 mdContent）；404 `post.not_found` |
| `GET /posts/statistics` | – | `data={"totalWordCount":152340}` |
| `GET /posts/slugs` | – | 全部 slug 数组，不分页 |
| `GET /posts/navigation` | `?slug=` | `data={"newer":PostBrief|null,"older":PostBrief|null}` |
| `GET /posts/related` | `?slug=&limit=`（默认 6，最大 20） | 按共有 tag 随机取样，排除自身；无 tag 返回 `[]`（不是 404） |
| `GET /posts/summary` | `?slug=` | AI 摘要，缓存优先；无缓存时生成一次并落库（错误 `upstream.failed`） |
| `GET /posts/semantic` | `?query=&limit=` | **恒 501** `search.not_implemented`（语义搜索预留位） |

#### 写接口（后台）

| 接口 | Body / 参数 | 返回 / 错误 |
|---|---|---|
| `POST /posts/create` | `{slug,title,category,tags,bannerImg,description,coverFit,publishTime,lastUpdatedTime}` | **201** `{"slug":"…"}`；409 `post.slug_conflict` |
| `PUT /posts/update` | `slug` + 可改字段 | `{"updated":true}`；404 |
| `PUT /posts/content` | `{slug, markdown}` | 服务端重算 `wordCount`/`plainContent`/`searchText`；返回 `{"wordCount":N}` |
| `DELETE /posts/delete` | `?slug=` | `{"deleted":true}`，**幂等**（不存在也 true） |
| `POST /posts/summary` | `{"slug":"…"}` | 强制重生成摘要，`{"summary":"…"}`（外部调用 SiliconFlow，可能 502） |

### 3.2 草稿 `/drafts`（全部后台）

与 posts 对称：`GET /drafts/list`（`keyword` 为标题模糊，空 = 列全部）、`GET /drafts/detail`、`GET /drafts/slugs`、`POST /drafts/create`（201）、`PUT /drafts/update`、`PUT /drafts/content`、`DELETE /drafts/delete`（幂等）。错误码 `draft.*`。

### 3.3 分类体系

| 接口 | 参数 | 返回 |
|---|---|---|
| `GET /tags/list`（公开） | `countOnly` | `data=[{name,count}]`；`meta.count`=**去重种类数** |
| `GET /categories/list`（公开） | `countOnly` | 同上 |
| `GET /archives/list`（公开） | `countOnly` | `data=[{year,month,count}]` 倒序，上海时区 |

### 3.4 友链 `/links`

#### `GET /links/list`（公开）

`?countOnly=` → 分组按 `order` 升序（缺 order 视为 0，**数据库排序**）；
`meta.count` 是**友链条目总数**（不是分组数）。

#### `GET /links/theme-color?url=`（公开）

`data={"color":"#c95d7f"}` + `Cache-Control: public, max-age=86400`。
SSRF 防护（拒内网/环回/云元数据）、8s 总时限、≤3 次重定向、512KB 上限。
错误：`validation.invalid_field` / `image.unreachable` / `image.too_large` / `image.unsupported_type` / `rate_limit.exceeded`。

#### 写接口（后台）

| 接口 | Body / 参数 | 错误 |
|---|---|---|
| `POST /links/create` | `{group,id,name,url,avatar,description,color,latency}` | |
| `PUT /links/update` | `{group,id,name,url,avatar,description,color}` | `link.not_found` |
| `DELETE /links/delete` | `?group=&id=` | `link.not_found` |
| `POST /links/move` | `{fromGroup,toGroup,id}` | 原子换组，失败回滚 |
| `POST /links/groups/create` | `{name,description,order}` | 409 `link.group_conflict` |
| `PUT /links/groups/update` | `{oldName,name,description,order}` | |
| `DELETE /links/groups/delete` | `?name=` | `link.not_found` |

#### 机器调用

| 接口 | 鉴权 | Body | 说明 |
|---|---|---|---|
| `POST /links/latency` | 内部 | `{"items":[{"id","latency"}]}` | check-flink 上报延迟；secret 走头，**不进 body** |
| `POST /links/check` | 后台 | – | 触发 GitHub Actions 链路检查，`{"dispatched":true}` |

### 3.5 友链申请 `/links/applications`

#### `POST /links/applications/apply`（公开，5 次/30 分钟/IP）

```json
{ "name":"站点名", "link":"https://x.example", "avatar":"https://…",
  "descr":"一句话介绍", "email":"a@b.c", "color":"#f08",
  "type":0, "targetUrl":"" }
```

→ `data={"screeningTriggered":bool,"mailSent":bool}`（会触发 AI 初筛 + 给管理员发邮件）

- 校验：XSS 正则、URL/颜色白名单；判重做归一化（trim / URL 去尾斜杠 / 颜色缺省 `#888888` / 邮箱小写）
- 错误：`validation.*`、`application.duplicate`、`application.no_change`（type=1 与现状完全一致时拒）、`application.full`、`rate_limit.exceeded`

#### 其它

| 接口 | 鉴权 | 说明 |
|---|---|---|
| `GET /links/applications/status` | 公开 | `?email=&name=`（至少一个），最多 20 条 |
| `GET /links/applications/list` | 后台 | 待审列表，`createdAt` 升序（最早先审），`meta.count`=待审总数 |
| `POST /links/applications/decide` | 后台 | `{id, decision:"approved"|"rejected", group?, overrides?, reason?}`；approved 时 type=0 追加到 group、type=1 覆盖原友链（可借 group 换组）；rejected 写 reason（非空才发邮件）→ `{"mailSent":bool}` |
| `POST /links/applications/screening` | 内部 | `{id, verdict:"pass"|"review", backlink, summary, reason, model}` |

申请记录保留 14 天，过期自动清理（每次提交时顺带）。

### 3.6 说说 `/speaks`

| 接口 | 鉴权 | 参数 / Body | 返回 |
|---|---|---|---|
| `GET /speaks/list` | 公开 | 分页三件套 | `data=[Speak]` 按 `time` 倒序 + `meta` |
| `POST /speaks/create` | 后台 | `{time,content}` | 201 `{"created":true}` |
| `PUT /speaks/update` | 后台 | `{time,content}` | `{"updated":true}` |
| `DELETE /speaks/delete` | 后台 | `?time=` | `{"deleted":true}`（幂等） |

### 3.7 站点 `/site`

| 接口 | 鉴权 | 说明 |
|---|---|---|
| `GET /site/info` | 公开 | `data={"latestUpdateTime":1761400000}` |
| `POST /site/update-time` | 后台 | 写当前时间戳 + fire-and-forget 通知博客站刷缓存（通知失败不影响响应）；body 可选（`{"time":N}` 覆写） |

### 3.8 图床 `/images`（全部后台）

| 接口 | 参数 | 返回 |
|---|---|---|
| `GET /images/list` | – | 图床文件按日期倒序（**不支持 full**） |
| `POST /images/upload` | `multipart: file`（≤4MB） | `data={"url":"…"}` |
| `PUT /images/reupload` | `multipart: url + file` | `data={"url":"…"}` |
| `POST /images/import` | `{"url":"https://外链/图.png"}`（JSON） | 转存外链图，`data={"url":"…"}` |
| `DELETE /images/delete` | `?url=` | `{"deleted":true}` |

错误：`image.unsupported_type` / `image.too_large` / `image.unreachable` / `upstream.failed`。

### 3.9 鉴权 `/auth`

| 接口 | 鉴权 | Body / 说明 |
|---|---|---|
| `POST /auth/login` | 公开 | `{user,pass}` → `data={"token":"<jwt>"}`；连续失败 403 `auth.locked`（与密码错误同文案） |
| `GET /auth/verify` | 后台 | `data={"user":"ariasaka","exp":1761400000}` |

### 3.10 运维 `/health` `/status` `/sitemap` `/maintenance`

| 接口 | 鉴权 | 说明 |
|---|---|---|
| `GET /health` | 公开 | 存活探针：`{"status":"ok","db":"ok","version":"…","time":…}`；DB 不通 503 |
| `GET /status` | 公开 | 状态页：版本（`YYMMDD-短hash`）、技术栈版本、自身运行时、DB/图床/SMTP/LLM/GitHub 可达性（`up/down/disabled` 三态，单项 3s 超时）。统一信封 200 |
| `GET /sitemap` | 公开 | `application/xml`，`lastmod` 按上海时区 |
| `POST /maintenance/export` | 内部 | 全库导出 `data={"collections":{…}}`（超时放宽 120s）；调用方：`ariablognext-autobackup`。`SECRET` 不变 |

---

## 4. 调用注意事项

1. **分页上限**：数字 `pageSize` 1–200；`full` 一次最多 300 条，超限报错而不是静默截断。
2. **写接口对「目标不存在」返回 404**（旧后端是静默 200），删接口则幂等返回 true——语义按资源区分，客户端不要用「删除不存在的也 200」做探测。
3. **搜索是字面匹配不是正则**：`keyword` 里的 `*` `(` 等元字符不会生效；外挂标签（如 `<Folding>`）不参与命中，只搜标题与正文纯文本。
4. **slug 冲突不会覆盖**：重复 `POST /posts/create` 得 409；先查 `GET /posts/detail` 再决定。
5. **图片相关接口依赖图床配置**：`media.enabled=false` 时（如本地开发）对应路由不注册，表现为 404 而不是 500。
6. **限流按 IP**：Vercel 后面取 `X-Forwarded-For` 的真实客户端 IP；代理层不要伪造该头。
7. **`requestId` 贯穿日志**：报告问题时附上响应里的 `requestId`，服务端日志能直接对上。
8. **外部调用方迁移对照**（SECRET 不变，只改 URL 与头）：
   - `check-flink`：`POST /links/latency` + `X-Internal-Token` 头
   - `flink-screening.yml`：`POST /links/applications/screening` + `X-Internal-Token` 头
   - `ariablognext-autobackup`：`POST /maintenance/export` + `X-Internal-Token` 头，读 `json.data.collections`
9. **旧路径不兼容**：`/get/*`、`/update/*`、`/access/*`、`/flink/*` 一律 404 `not_found.route`。前端走的是 glue（`blognext-end.yaria.top`）翻译后的旧契约，直连本服务必须用本文档的路径。
10. **兼容性承诺**：v1.0 内不改已有字段语义、不删字段；新增字段不升版本；破坏性变更升 v2 并保留 v1 平行期。

---

## 5. 变更记录

| 日期 | 版本 | 变更 |
|---|---|---|
| 2026-10-27 | v1.0 | 首个正式版：56 条路由（含 `/status`）；`maxPageSize` 100→200 |
