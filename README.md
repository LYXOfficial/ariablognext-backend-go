# ariablognext-backend-go

[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

AriaBlogNext 博客后端的 Go 重写版：**Fiber v3 + MongoDB**，部署在 **Vercel**。

> ✅ **当前状态：P0–P4 全部实现并验收通过。**
> 56 条路由、统一信封、仓储抽象、鉴权/限流/体积限制、图床/邮件/GitHub/LLM 平台层、
> 友链申请全流程、导出/sitemap/语义搜索 stub 均已完成；前端仍走旧后端，由
> `ariablognext-backend-glue` 做兼容翻译，切流清单见 `sync/inbox/U-001`（工作区根目录）。
>
> 最近一次验收（2026-10-27）：`scripts/checks.sh` 全绿、`go test ./...` 12 包全过、
> `go test -tags=integration ./...`（本地 mongod）12 包全过、`tests/smoke.py` 29/29、
> 生产只读 `tests/compare.py` 40 ✓ / 1 ⚠ / 0 ✗。详见 `P4_DONE.md` 与 `docs/pitfalls.md`。

---

## 为什么重写

| | 现在（Python / FastAPI） | 这个仓库（Go / Fiber） |
|---|---|---|
| 路由 | 72 条，`/get/*` `/update/*` 动词前缀 | 56 条，`/资源/细分` + query |
| 响应 | `{message, data}` / `{detail:{...}}` 各自为政 | 统一信封 `{ok, requestId, data, meta | error}` |
| 数据层 | 直接 `motor` 到处查 | 仓储接口 + 可替换适配器（`DB_TYPE`）|
| 配置 | 散落的 `os.environ.get` | 单一 `config.yml` + `{{ env.X }}` 占位符 |
| 时区 | 依赖进程 `TZ`，归档分桶还是 UTC | 显式 `Asia/Shanghai`，不在数据库里分桶 |
| 搜索 | 全量拉 `mdContent` 在应用层剥 Markdown | 写入时维护 `searchText`，查询只投影必要列 |

前端**不改**：兼容旧接口的翻译层在独立仓库 `ariablognext-backend-glue`。

---

## 目录

```
cmd/server/            Vercel 入口（监听 $PORT），也是本地开发入口
internal/
  config/              配置加载（config.yml + 占位符解析 + 校验 + 脱敏）
  apperr/              错误码与错误类型
  app/                 Fiber 组装、统一信封渲染、中间件、路由表
  auth/                JWT
  model/               领域模型（字段名 = 数据库字段名）
  store/               仓储接口（不含任何 bson）
    fake/              内存实现，供单测
    mongo/             MongoDB 实现
  service/             业务逻辑（不碰 fiber）
  handler/             HTTP 层（只做参数绑定与调用，不写响应）
  platform/            S3 / SMTP / GitHub / LLM
  textutil/            Markdown 清理、字数、searchText
  validate/            友链申请校验
config.yml             唯一配置入口
docs/                  契约与设计文档（先读这个）
scripts/checks.sh      架构约束的静态守卫
tests/smoke.py         接口冒烟测试（只用标准库）
```

---

## 开发

```bash
go mod tidy
go run ./cmd/server          # http://127.0.0.1:2333（需要本机 mongod）

bash scripts/checks.sh                        # 架构约束（8 项）
go test ./...                                 # 单元测试（12 包）
go test -tags=integration ./...               # 集成测试（需 MONGODB_URI_TEST + TEST_DB_NAME）
python3 tests/smoke.py                        # 冒烟（服务需在跑）
go run ./cmd/seeddev                          # 灌开发种子（只允许 *_dev/_test 库）
python3 tests/glue_replay.py                  # 跨服务回放（需 glue 在 2335 跑）
```

更多操作细节（生产只读对比、集成测试库、跨服务回放）见 `tests/README.md`。

配置全部在 `config.yml`；敏感值写成 `{{ env.MONGODB_URI }}` 这类占位符，
启动时从环境变量解析，缺一个就报错退出——**不会带着半套配置跑起来**。

### 环境文件怎么分工

| 文件 | 用途 | 进 git？ |
|---|---|---|
| `.env.dev` | **日常开发就用这个**：本机 mongod、库名 `AriaBlogNext_dev`、一次性随机 `SECRET`、外部服务全关 | ❌ |
| `.env.production` | 生产值留档（与 Vercel 的 Environment Variables 一一对应）| ❌ |
| `config.local.yml` | 本地配置变体：图床/邮件/初筛/摘要 `enabled: false` | ❌ |
| `.env.example` | 变量清单模板，只有键名没有值 | ✅ |

`cmd/server` 启动时按 `.env.dev` → `.env` 的顺序找第一个存在的文件；
`.env.dev` 里的 `CONFIG_FILE=./config.local.yml` 指定用本地配置。

### 生产只读验证

真实数据里的坑 mock 不出来（`plainContent` 截断、`order` 缺字段、`coverFit` 为 null），
所以留了一条**连生产库但写不进去**的通路：

```bash
ENV_FILE=.env.production CONFIG_FILE=./config.prod-readonly.yml go run ./cmd/server
python3 tests/compare.py      # 与线上旧后端逐字段对比
```

`database.readOnly: true` 时，适配器里每个写方法第一行就返回 `store.ErrReadOnly`，
对外是 `503 storage.read_only`——不是约定，是代码。

**生产值绝不写进 `.env.dev`。** 本机没有 mongod 时，只能把 Atlas 连接串写进
`.env.dev` 并**把库名换成 `AriaBlogNext_dev`**，绝不能用 `AriaBlogNext`（那是线上库）。

---

## 文档

| 文件 | 内容 |
|---|---|
| [`TASKS.md`](TASKS.md) | 施工清单（按阶段，带验收命令）|
| [`docs/api-draft.md`](docs/api-draft.md) | 接口契约：56 条路由、参数、返回、错误码 |
| [`docs/api-v1.md`](docs/api-v1.md) | **v1.0 正式 API 文档**：对外承诺的路径/参数/错误码/注意事项 |
| [`docs/store-interfaces.md`](docs/store-interfaces.md) | 存储层接口定义 |
| [`docs/design-notes.md`](docs/design-notes.md) | 设计取舍：信封硬约束、数据抽象层、配置、时区、并发 |
| [`docs/testing.md`](docs/testing.md) | 测试方案：四层测试与具体用例 |
| [`AGENTS.md`](AGENTS.md) | 给 AI / 新同学的工作守则 |

---

## 与旧后端的关系

旧仓库 `ariablognext-backend`（Python）继续在线上跑，直到新服务和胶水层切换完成。
迁移路线见 `docs/glue.md §7`。

## License

[MIT](LICENSE)
