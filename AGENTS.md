# AGENTS.md

给在这个仓库里干活的 AI / 新同学。**先把 `TASKS.md` 读完再动手。**

---

## 1. 这个仓库是什么

AriaBlogNext 博客后端的 Go 重写版：**Fiber v3 + MongoDB**，部署在 **Vercel**
（Go Framework Preset，进程监听 `$PORT`）。前端本阶段不改，兼容旧接口由独立仓库
`ariablognext-backend-glue` 负责。

本仓库**只实现新接口**，旧路径（`/get/*`、`/update/*`、`/access/*`、`/flink/*`）
一条都不许出现。

---

## 2. 开始前的顺序

1. `TASKS.md` —— 施工清单，从上到下做，不要跳阶段
2. `docs/api-draft.md` —— 接口契约（唯一事实来源）
3. `docs/store-interfaces.md` —— 存储层接口，签名照抄
4. `docs/design-notes.md` —— 设计取舍，尤其 §1（信封硬约束）、§3（配置）、§5（时区）
5. `docs/testing.md` —— 每层测试写什么
6. `config.yml` —— 唯一配置入口

---

## 3. 硬性约束（会失败的命令，不是建议）

`bash scripts/checks.sh` 会逐条检查下面这些，**它挂了就修代码，不许改脚本**：

| 约束 | 原因 |
|---|---|
| `os.Getenv` 只能出现在 `internal/config` | 配置只能从 `config.yml` 走，避免实现细节散落 |
| handler 里不许 `c.JSON` / `c.Status` / `net/http` | 统一信封是编译期约束 |
| `bson` / `mongo-driver` 只能出现在 `internal/store/mongo/**` 和 `internal/store/open.go` | 数据抽象层不被 Mongo 语义污染 |
| `internal/store` 顶层不许 `bson.` / `primitive.` | 同上 |
| 不许 `fmt.Print*` | 日志统一 `log/slog` |
| 每个 `internal/` 子包都要有 `*_test.go` | 不许只写实现不写测试 |

---

## 4. 编码约定

- **注释写「为什么」，不写「是什么」**。尤其是踩坑点：为什么这里要转义、为什么不能用 `$toDate`、
  为什么 `mongo.Connect` 不传 ctx。下一轮 AI 会把没写原因的改动改回去。
- 注释、错误提示、日志**全中文**。错误提示要给人看：说清「发生了什么 + 怎么办」。
- 对外错误统一 `apperr.New(code, 中文message, details)`，**不要把 `err.Error()` 塞进 message**，
  原始错误放 `details.cause`。
- 返回切片时用 `[]T{}` 而不是 `nil`，否则 JSON 出 `null`，前端 `.map` 会炸。
- 依赖注入全部显式：`main` 里 `config → store → service → handler → app`，**不要全局变量**。
- 时间一律 unix 秒 `int64`；涉及「年月」的地方显式传 `*time.Location`，
  **绝对不要用 `time.Local`**。

---

## 5. 验证要求（按改动范围）

1. 任何改动：`bash scripts/checks.sh && go test ./...`
2. 改了接口或 handler：再加 `python3 tests/smoke.py`（服务要跑起来）
3. 改了仓储实现：再加 `go test -tags=integration ./...`（需要测试库，见 `docs/testing.md §4.1`）
4. 改了契约（只有在用户明确要求时）：跑 `-update` 重新生成 golden，并**人工 diff 一遍**

**不许**用「测试跳过」来让流水线变绿。

---

## 6. 数据库

- 库名 `AriaBlogNext`，与 twikoo 共用（`comment` / `counter` / `config` 是 twikoo 的，**不要写**）
- 三个环境文件都**不进 git**：`.env.dev`（日常）、`.env.production`（生产留档）、`config.local.yml`（本地配置变体）
- **日常开发只用 `.env.dev`**；它指向本机 mongod + `AriaBlogNext_dev`。`.env.production` 不是给人本地跑的
- 生产数据只读可以，**写操作只能在 `AriaBlogNext_dev` / `AriaBlogNext_test` 上做**
- 测试要自己造数据、跑完清理（`TestMain` 里 `Drop`）
- 破坏性操作前先备份到 `/tmp`
- **生产只读验证**只能这样跑：`ENV_FILE=.env.production CONFIG_FILE=./config.prod-readonly.yml go run ./cmd/server`。
  绝对不许把 `.env.production` 复制成 `.env`，也不许把 `readOnly` 改成 false 去「试一下」

---

## 7. 不要做的事

- ❌ 改 `docs/api-draft.md` 的契约（有疑问就停下来问）
- ❌ `git commit` / `git push`（用户自己提交）
- ❌ 引入清单外的依赖，或自行升级版本
- ❌ 把 mongo 类型泄漏到 handler / service
- ❌ 在 handler 里写业务逻辑（业务在 `internal/service`）
- ❌ 在 service 里碰 `fiber.Ctx`（service 只收 `context.Context`）
- ❌ 用 `fmt.Println` 调试完就留在代码里

---

## 8. 卡住了怎么办

先自己排查两步（读报错、读文档、搜现有代码），**20 分钟内解决不了就停下来问**，
把「我在做 X，预期 Y，实际 Z，试过 A/B」写清楚。**不要猜着改契约**。
