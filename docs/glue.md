> 这是 `ariablognext-backend-glue` 仓库的文档副本，**以该仓库内的版本为准**。
> 本文件只是方便在后端仓库里对照阅读，两处如有不一致，以后端仓库根目录外的 glue 仓库为准。

# 胶水层（`ariablognext-backend-glue`）文档 · 草稿 v0.1

> 目的：让 blog / dashboard **一行代码都不改**就能跑在新后端上，同时把「旧契约」和「新契约」
> 的差异全部集中在一个可以随时删掉的小项目里。

---

## 1. 定位与边界

**做什么**

- 实现旧后端那 72 条路径，把请求转发到新后端并做三件事：
  1. 参数换算（`startl/endl` → `page/pageSize`、`type=full` → `content=true`、`query` → `keyword`…）
  2. 响应重排（`{ok,data}` → `{message:"success", …}`；`error` → `{detail:{message:"fail",error}}`）
  3. 补齐旧接口特有字段（`count`、`totalCount`、`jwt`、`time`、`mailSent`…）

**不做什么**

- 不连数据库、不做业务校验、不做鉴权判断（token 原样透传给新后端，让新后端返回 401）
- 不缓存、不做兜底数据
- 不实现新功能

**为什么值得单独一个项目**：它天然是「契约的可执行说明书」。哪天有人问「旧接口到底返回什么」，
它比文档可靠；迁移结束直接删仓库，不会在新后端里留一堆 `if legacy` 分支。

---

## 2. 部署形态

| 项 | 值 |
|---|---|
| 独立 Vercel 项目 | `ariablognext-backend-glue` |
| 域名 | **接管旧的 `blognext-end.yaria.top`** |
| 新后端 | `blognext-go.yaria.top` |
| 上游地址 | env `UPSTREAM=https://blognext-go.yaria.top` |
| 运行方式 | 单文件 Go 服务（`net/http` 就够，不需要 Fiber），Vercel Go preset |
| 依赖 | 无数据库、无 SDK，只 `net/http` + `encoding/json` |

**关键点：胶水接管旧域名，前端零改动。** 前端 `config.tsx` / `dashboardConfig.ts` 里的
`backEndUrl` 一个字符都不用动，DNS/别名切过去即可。

```
blog.yaria.top ──┐
                 ├──> blognext-end.yaria.top  ──> glue ──> blognext-go.yaria.top
admin/*.yaria.top┘
```

---

## 3. 转换总则

### 3.1 成功响应

```go
// 新后端
{"ok":true,"requestId":"…","data":<D>,"meta":{…}}
```
```go
// 旧契约（胶水输出）
{"message":"success", <旧字段>…}
```

`<旧字段>` 每个接口都不一样，见 §4 的逐条表。三种典型：

| 旧形态 | 胶水怎么拿 | 胶水输出 |
|---|---|---|
| `{message, data}` | `list` 的 `data` | `{"message":"success","data":…}` |
| `{message, count}` | `list?countOnly=true` 的 `meta.count` | `{"message":"success","count":N}` |
| `{message, data, totalCount}` | **一次** `list`：`data` 与 `meta.count` 同时拿到 | `{"message":"success","data":…,"totalCount":N}` |
| `{message, time}` | `/site/info` 的 `data.latestUpdateTime` | `{"message":"success","time":N}` |

> 因为新接口的 `list` 默认就带 `meta.count`，旧接口里那些「列表 + 总数」的形态
> 全部**一次请求**搞定，比现在旧后端「先 count 再 find」还少一次往返。

### 3.2 失败响应

```go
// 新后端
{"ok":false,"requestId":"…","error":{"code":"post.not_found","message":"没有找到这篇文章","details":null}}
```
```go
// 旧契约（FastAPI 的 HTTPException 渲染结果）
{"detail":{"message":"fail","error":"没有找到这篇文章"}}
```

规则：

- `error.message` → `detail.error`（原样搬运中文，前端已经在展示它）
- `details.fields[]` → 拼进 `detail.error`，多字段时用「；」连接
  （旧接口每次只返回一句，胶水别一次塞多条）
- **HTTP 状态码透传**，不要统一改成 200：前端有 3 处依赖 `res.ok`
- 旧接口里少数几个返回的是**字符串** detail（`/access/user/login` 403、`verify` 401），
  胶水对这些路径输出 `{"detail":"Invalid credentials"}`，与旧行为一致

### 3.3 分页换算

```go
// 旧（offset/limit 混合）：startl 是起点下标，endl 是终点下标
// 前端实际只用了两种写法：
//   ?startl=0&endl=6               （最新 6 篇）
//   ?startl=32&endl=48             （第 3 页，pageSize=16）
//   ?type=full                     （不带分页，RSS 要全部）
```

换算：

```go
// 前端实际只用了三种写法：
//   ?startl=0&endl=6            最新 6 篇
//   ?startl=32&endl=48          第 3 页（pageSize=16）
//   ?type=full                  不带分页，RSS 要全部 83 篇

if type == "full" {
    // 新接口直接支持全量，不用翻页拼接
    q = "pageSize=full&withContent=true"
} else {
    limit := 16
    if endl > startl { limit = endl - startl }
    page  := startl/limit + 1
    q = fmt.Sprintf("page=%d&pageSize=%d", page, limit)
}
```

旧的 `type=full` 现在对应 `pageSize=full`，一次拿完，不再需要循环拼接。

### 3.4 字段级差异（必须逐条处理）

| 旧字段 | 新字段 | 说明 |
|---|---|---|
| `jwt` | `data.token` | 登录接口 |
| `count` | `data.count` | 计数类接口 |
| `time` | `data.latestUpdateTime` | `/get/siteInfo/lastUpdateTime` |
| `totalCount` | 无 | 由 `/count` 提供 |
| `previous` / `next` | `data.newer` / `data.older` | **方向是反的**，见 §5.3 |
| `data.data.links.url` | `data.url` | 图片上传，见 §5.1 |
| `plainContent`（201 字截断）| 同名字段 | 新后端也在列表里截 201 字，保持一致 |

### 3.5 透传类

| 旧路径 | 处理 |
|---|---|
| `GET /get/sitemap/sitemapGen` | 直接转发 `GET /sitemap` 的 **XML 原样**，不改 Content-Type |
| `POST /get/export/collections` | 转发 `POST /maintenance/export`，把 `data.collections` 提到顶层 |
| `POST /flink/screening` | 转发 + 把 body 里的 `secret` 挪到 `X-Internal-Token` 头 |
| `DELETE /update/*/delete*RenderedHtmlCache` | 不转发，直接回 `{"message":"success"}`（功能已删，无人调用）|

---

## 4. 逐条映射表（72 条）

> 新接口的 `list` 默认返回 `data + meta.count`，所以旧接口里「列表 + 总数」的形态
> 一次请求就能拼出来；只要数量的，一律加 `countOnly=true`。

### 4.1 公开读（blog 用）

| 旧 | 新 | 参数换算 | 响应重排 |
|---|---|---|---|
| `GET /get/post/postsInfo` | `GET /posts/list` | `startl/endl → page/pageSize`；`type=full → pageSize=full&withContent=true` | `{message, data}` |
| `GET /get/post/postBySlug` | `GET /posts/detail` | `slug` | `{message, data}` |
| `GET /get/post/postCount` | `GET /posts/list?countOnly=true` | – | `{message, count:meta.count}` |
| `GET /get/post/totalWordCount` | `GET /posts/statistics` | – | `{message, count:data.totalWordCount}` |
| `GET /get/post/postSlugs` | `GET /posts/slugs` | – | `{message, data}` |
| `GET /get/post/postNavigation` | `GET /posts/navigation` | `slug` | `{message, previous:data.newer, next:data.older}` |
| `GET /get/post/relatedPosts` | `GET /posts/related` | `limit=6` | `{message, data}`（无 tag 时新后端返回 `[]`，旧接口是 404，按 `[]` 输出）|
| `GET /get/post/postSummary` | `GET /posts/summary` | `slug` | `{message, data:data.summary}` |
| `GET /get/post/searchPosts` | `GET /posts/list` | `query → keyword`，`pageSize=100` | `{message, data}` |
| `GET /get/post/searchPostsByTitle` | `GET /posts/list` | `title → keyword`，`titleOnly=true` | `{message, data}` |
| `GET /get/post/searchPostsByTitleCount` | `GET /posts/list?countOnly=true` | 同上 | `{message, count:meta.count}` |
| `GET /get/tag/tags` | `GET /tags/list` | – | `{message, data}` |
| `GET /get/tag/tagCount` | `GET /tags/list?countOnly=true` | – | `{message, count:meta.count}`（去重标签数）|
| `GET /get/tag/tagInfo` | `GET /posts/list` | `tag` + 分页换算 | `{message, data, totalCount:meta.count}`（**一次请求**）|
| `GET /get/category/categories` | `GET /categories/list` | – | `{message, data}` |
| `GET /get/category/categoryCount` | `GET /categories/list?countOnly=true` | – | `{message, count:meta.count}` |
| `GET /get/category/categoryInfo` | `GET /posts/list` | `category` + 分页换算 | `{message, data, totalCount:meta.count}` |
| `GET /get/archive/archives` | `GET /archives/list` | – | `{message, data}` |
| `GET /get/archive/archiveInfo` | `GET /posts/list` | `year/month` + 分页换算 | `{message, data, totalCount:meta.count}`；`data` 为空时回 **404**（照抄旧行为）|
| `GET /get/speaks/speaks` | `GET /speaks/list` | 分页换算 | `{message, data}` |
| `GET /get/speaks/speaksCount` | `GET /speaks/list?countOnly=true` | – | `{message, count:meta.count}` |
| `GET /get/flink/flinks` | `GET /links/list` | – | `{message, data}` |
| `GET /get/flink/flinkCount` | `GET /links/list?countOnly=true` | – | `{message, count:meta.count}`（条目总数）|
| `GET /get/flink/themeColor` | `GET /links/theme-color` | `url` | `{message, data:"#xxx"}`；**必须透传 `Cache-Control`**，错误重排成 `{detail:{message:"fail",error}}` + 保留 CORS 头 |
| `GET /get/siteInfo/lastUpdateTime` | `GET /site/info` | – | `{message, time:data.latestUpdateTime}` |
| `GET /get/sitemap/sitemapGen` | `GET /sitemap` | – | 原样 XML 直通 |
| `GET /flink/apply/status` | `GET /links/applications/status` | `email` / `name` | `{message, data}` |

### 4.2 后台读（dashboard 用）

| 旧 | 新 | 说明 |
|---|---|---|
| `GET /get/draft/draftsInfo` | `GET /drafts/list` | 分页换算；`{message, data}` |
| `GET /get/draft/draftBySlug` | `GET /drafts/detail` | |
| `GET /get/draft/draftSlugs` | `GET /drafts/slugs` | |
| `GET /get/draft/draftCount` | `GET /drafts/list?countOnly=true` | `{message, count:meta.count}` |
| `GET /get/draft/searchDraftsByTitle` | `GET /drafts/list` | `title → keyword` |
| `GET /get/draft/searchDraftsByTitleCount` | `GET /drafts/list?countOnly=true` | `title → keyword`；`{message, count:meta.count}` |
| `GET /update/post/summary` | `GET /posts/summary` | 只读缓存（见 §5.4）|
| `GET /update/flink/applications` | `GET /links/applications/list` | |
| `GET /update/image/listImages` | `GET /images/list` | `{message, data:{images:[...]}}` |
| `GET /update/flink/dispatchCheckLatencyWorkflow` | `POST /links/check` | 方法变化，胶水内部用 POST |
| `GET /access/user/verify` | `GET /auth/verify` | 成功 `{message:"success"}`；失败 `{"detail":"Invalid token"}`（字符串）|

### 4.3 写（dashboard + 友链申请）

| 旧 | 新 | 响应重排 |
|---|---|---|
| `POST /access/user/login` | `POST /auth/login` | `{message, jwt:data.token}`；403 → `{"detail":"Invalid credentials"}` |
| `POST /update/post/addPost` | `POST /posts/create` | `{message:"success"}`；`post.slug_conflict` → 409 `{"detail":"slug already exists"}` |
| `PUT /update/post/updatePostInfo` | `PUT /posts/update` | `{message:"success"}` |
| `PUT /update/post/updatePostMarkdown` | `PUT /posts/content` | `{message:"success"}` |
| `DELETE /update/post/deletePost` | `DELETE /posts/delete` | `{message:"success"}` |
| `POST /update/draft/addDraft` | `POST /drafts/create` | 同上 |
| `PUT /update/draft/updateDraftInfo` | `PUT /drafts/update` | |
| `PUT /update/draft/updateDraftMarkdown` | `PUT /drafts/content` | |
| `DELETE /update/draft/deleteDraft` | `DELETE /drafts/delete` | |
| `POST /update/speaks/newSpeaks` | `POST /speaks/create` | |
| `PUT /update/speaks/updateSpeaks` | `PUT /speaks/update` | |
| `DELETE /update/speaks/deleteSpeaks` | `DELETE /speaks/delete` | |
| `POST /update/siteInfo/latestUpdateTime` | `POST /site/update-time` | |
| `POST /update/post/refreshSummary` | `POST /posts/summary` | `{message, data:data.summary}` |
| `POST /update/flink/addFlink` | `POST /links/create` | |
| `PUT /update/flink/updateFlink` | `PUT /links/update` | |
| `DELETE /update/flink/deleteFlink` | `DELETE /links/delete` | |
| `POST /update/flink/addGroup` | `POST /links/groups/create` | |
| `PUT /update/flink/updateGroup` | `PUT /links/groups/update` | |
| `DELETE /update/flink/deleteGroup` | `DELETE /links/groups/delete` | |
| `POST /update/flink/moveFlinkGroup` | `POST /links/move` | |
| `POST /update/flink/approveApplication` | `POST /links/applications/decide` | 补 `decision:"approved"`；`{message, mailSent}` |
| `POST /update/flink/rejectApplication` | `POST /links/applications/decide` | 补 `decision:"rejected"`；`{message, mailSent}` |
| `POST /update/flink/pushFlinkStatus` | `POST /links/latency` | body `{data:{linkStatus:[…]},secret}` → `{items:[…]}` + 头 |
| `POST /flink/apply` | `POST /links/applications/apply` | `{message, screeningTriggered, mailSent}` |
| `POST /flink/screening` | `POST /links/applications/screening` | `secret` → 头 |
| `POST /update/image/uploadImage` | `POST /images/upload` | **`{message, data:{data:{links:{url}}}}`**（见 §5.1）|
| `PUT /update/image/reuploadImage` | `PUT /images/reupload` | 同上嵌套 |
| `POST /update/image/rehostImageByUrl` | `POST /images/import` | `{message, data:{url}}` |
| `DELETE /update/image/deleteImage` | `DELETE /images/delete` | `{message, data:{deleted:url}}` |
| `POST /get/export/collections` | `POST /maintenance/export` | `{message, collections:data.collections}` |

### 4.4 直接回 success（功能已删）

| 旧 | 处理 |
|---|---|
| `DELETE /update/post/deleteRenderedHtmlCache` | `{"message":"success"}` |
| `DELETE /update/post/deleteAllRenderedHtmlCache` | `{"message":"success"}` |
| `DELETE /update/draft/deleteRenderedHtmlCache` | `{"message":"success"}` |

---

## 5. 特殊 case 详解

### 5.1 图片上传的双层嵌套

旧接口为了兼容 Vditor 的返回格式，返回：

```json
{"message":"success","data":{"data":{"links":{"url":"https://img.0v0.my/…"}}}}
```

dashboard 读的是 `(await res.json()).data.data.links.url`。**这个嵌套必须原样保留**，
在胶水里显式构造，不要试图「顺便清理」。

### 5.2 `themeColor` 的错误形态

前端 `themeColor.ts` 只认 `detail.error` 里的中文：

```ts
const detail = data?.detail;
const msg = typeof detail === "object" && detail !== null ? detail.error : "";
```

所以胶水必须输出 `{"detail":{"message":"fail","error":"头像图片下载失败（…）"}}`，
不能是字符串，也不能是空对象。同时 **CORS 头不能丢**（旧后端在这个接口上专门处理过）。

### 5.3 `navigation` 的方向

旧接口：

```python
nextPost     = find(publishTime < 当前).sort(-1)   # 更旧的那篇
previousPost = find(publishTime > 当前).sort(+1)   # 更新的那篇
```

名字和行为相反。新接口 `newer` / `older` 语义正确，胶水映射：

```go
out["previous"] = data["newer"]
out["next"]     = data["older"]
```

### 5.4 `postSummary` 两种语义

| 旧 | 语义 | 新 |
|---|---|---|
| `GET /get/post/postSummary` | **公开**，缓存优先，miss 则生成一次 | `GET /posts/summary`（公开，行为一致）|
| `GET /update/post/summary` | 后台，**只读缓存**，不触发生成 | 同一个 `GET /posts/summary` |

后台那条的唯一差别是「不该花 SiliconFlow 额度」。因为每篇最多生成一次且结果落库，
后台读到无缓存的概率极低，所以直接复用公开接口，不再单开一条。

### 5.5 RSS 的全量拉取

`/get/post/postsInfo?type=full` 会返回全部 83 篇含 `mdContent`（最大 140 KB）。

新后端用 `pageSize=full&withContent=true` 一次给完，**不需要循环翻页**。
这是全站唯一一次「取全部」，保留现状即可（真到几千篇时再加翻页）。

### 5.6 空结果的 404

旧 `archiveInfo` 在区间内没有文章时返回 **404**（不是空数组），前端据此显示「这个月没有文章」。
胶水必须照抄，别顺手改成 200。

---

## 6. 一致性校验（胶水的 CI）

```
make diff   # 对同一组请求分别打旧后端（线上）和新后端，逐字段比较
```

覆盖：

1. **只读接口全矩阵**：27 条公开 GET × 若干参数组合，比对 JSON 结构（忽略 `requestId`、时间戳、随机抽样）
2. **写接口**：跑在 `AriaBlogNext_test` 测试库上，造数据 → 写 → 读回 → 比对 → 清理
3. **错误分支**：404/401/409/429 各来一发，比对状态码与 `detail.error` 文案
4. **二进制**：sitemap 的 XML 逐字节比对

任何一处不一致，以**旧后端**为准改胶水，除非我们明确决定「这里要修」。

---

## 7. 迁移路线

```
阶段 A  新后端上线 blognext-go.yaria.top（只读域先跑通）
        └ 胶水部署到 blognext-end.yaria.top，全量 72 条转发
        └ 前端零改动，此时可随时切回旧后端（DNS 一分钟的事）

阶段 B  按域灰度：blog 的只读域先确认无误
        └ 之后每确认一个域，就把前端该域的请求改成直连新后端（改一个 URL 常量）
        └ 对应胶水路线下线

阶段 C  全部迁移完 → 胶水项目归档（保留 tag 备查），旧后端仓库归档
```

**注意顺序**：必须「前端先改、胶水后删」。反过来会 404。

---

## 8. 退役时要做的事

- 仓库打 tag `final` 并置为 archived，不删（它是旧契约的唯一可执行记录）
- 在 `api-draft.md` 里补一节「历史契约」，指向该 tag
- 把 `SECRET` 相关的三个外部调用方（初筛 workflow、check-link、`ariablognext-autobackup`）都改到新接口
