# NEPT-5 代码评审

固定点：`origin/feature/nept-7-multica-upstream-issues`（`e3813b08d`）
评审范围：`e3813b08d...629e4fa81`（分诊 1 个 commit + 实现与返工 5 个 commit）
评审输入：`brief.md`（分诊结论与方案）、`AGENTS.md`、`CONTRIBUTING.md`、`apps/docs/content/docs/developers/conventions.mdx`
方式：规范轴、需求轴各由一名只读评审者独立进行，两轴分别判定；返工后重跑两轴。

---

## 第 1 轮

### 规范轴

结论：需改。

1. 六语种环境变量文档写「其他调用方一律 403」，但 `/api/instance/access` 对未授权者按设计返回 `200` + `false`；与实现矛盾的文档必须同 PR 修正。
2. 新增端点未补 `packages/core/api/schema.ts` 的 zod schema（判断项，见文末取舍）。
3. 自写 `instanceOverviewDays` 与 `parseExactSinceParamInTZ` 重复解析同一 `days` 参数。
4. 未复用 `handler.RequireHumanActor`，在 handler 内重写了机器凭据判定。

### 需求轴

结论：需改。

1. `brief.md` 测试计划要求的 `store_test.go`（跨工作区聚合、窗口过滤、求和、归档与 `kind='system'` 排除）完全缺失。
2. 「`tz=Asia/Shanghai` 与 UTC 切日差异体现在 `usage[].date`」未断言。
3. 「各列表不为 `null`」的断言用长度比较，nil 与空切片长度同为 0，等于没有守住。

### 返工处置（第 1 轮）

- 规范轴 1：六语种改为「`access` 返回 `false`、`overview` 返回 403」。
- 规范轴 3：抽出共享 `daysParam`，handler 改用 `resolveViewingTZ` + `sinceFromDays`。
- 规范轴 4：`/api/instance` 路由组挂 `RequireHumanActor`，handler 内保留注释说明的兜底判定。
- 需求轴 1–3：补 `store_test.go`（先落在 handler 包）、UTC/上海切日断言、JSON 数组非 null 断言。
- 附带：`summary` 只读 runs/failed（token/成本改由已加载的 usage 行求和，少一次全表扫描）；修正注释里「既有时间索引覆盖」的错误说法。

## 第 2 轮

### 规范轴

结论：需改。

1. 测试文件位置错位：`instanceadmin/store.go` 的数据库测试落在 `handler` 包，违背「测试与实现同目录」。
2. handler 测试手写 `INSERT`/`DELETE` 造行，未用仓库指定的 `dbfx` 夹具。
3. `instance_admin.go` 调 `resolveViewingTZ`，而该函数注释明确要求「不要在缺少 `?tz=` 的客户端场景复制这次查库」；等于每个 overview 请求两次 `GetUser`。
4. `.env.example` 的注册控制段缺 `INSTANCE_ADMIN_EMAILS`。

### 需求轴

结论：需改。

1. `store_test.go` 仍不在 `instanceadmin` 包内（与测试计划点名位置不符）。
2. 「重复项无害」未覆盖。
3. `.env.example` 缺项。
4. 注释声称窗口与 dashboard 一致，实际 dashboard 的日期序列是 N+1 天、本接口是 N 天，注释与事实不符。
5. 工作区 `last_activity_at` 的定义未说明（与用户侧 client 心跳口径不同）。

### 返工处置（第 2 轮）

- 规范轴 1 / 需求轴 1：测试移入 `internal/instanceadmin/store_test.go`，自带 `DATABASE_URL` harness（无库时跳过），把 cutoff 钉到固定时刻。
- 规范轴 2：handler 测试改用 `dbfx.InsertNoID`（两张表无 `id` 列）。
- 规范轴 3：身份检查一次取回账号行并返回已校验时区，overview 不再二次查库；抽出 `validTimezone` 统一校验。
- 规范轴 4 / 需求轴 3：`.env.example` 补 `INSTANCE_ADMIN_EMAILS`。
- 需求轴 2：补重复项用例。
- 需求轴 4：注释改写为「恰好 N 天，与 `parseExactSinceParamInTZ` 同口径」，并解释为何不能照搬 dashboard 的 N+1。
- 需求轴 5：`WorkspaceSummary` 文档写明口径（工作区行更新与 issue 更新的较晚者；心跳按账号统计）。
- 附带：修掉 `SUM(cost_usd_ticks)` 在整桶无报价时返回 NULL 会让整个接口扫描失败的问题；删除只有一个实现的 `Reader` 接口。

## 第 3 轮

### 规范轴

结论：需改（含判断题）。

1. 自写 `formatTimestamp` / `timestampOrNil` / `uuidOrNil`，而 `internal/util/pgx.go` 已有同义函数。
2. `instanceadmin.Querier` + nil store 的 503 回落被质疑为「未被要求的兜底」，且 503 分支无测试（判断题，见文末取舍）。

### 需求轴

结论：需改。

1. 成本只给 `cost_usd_ticks`（供应商自报价）对多数 provider 恒为 0；仓库既有口径是把「已报价成本」与「待按费率表估算的 token」分开给。
2. 「空库时各列表不为 null」仍未真正覆盖空 `usage` 的场景。
3. `usage` 用 `JOIN workspace`，而 `task_usage_hourly` 无外键，已删工作区的孤儿行被静默丢弃。
4. `agent_count`（排除归档/系统 agent）与 `runs`（全部任务）口径不同，未说明。
5. 注释称 `cost_usd_ticks` 可为 NULL，实际迁移 213 定义为 `NOT NULL DEFAULT 0`。

### 返工处置（第 3 轮）

- 规范轴 1：改用 `util.TimestampToString` / `TimestampToPtr` / `UUIDToPtr`。
- 规范轴 2：补 nil store 的 503 用例；保留该兜底（见取舍）。
- 需求轴 1、5：`UsageRow` / `Summary` 增加 `uncosted_*` 四列，与 dashboard 查询同口径；修正成本列可空性注释；文档写清两段语义。
- 需求轴 2：新增「空窗口返回数组」用例（`since` 取极远未来）。
- 需求轴 3：改 `LEFT JOIN`，孤儿行仍计入实例台账。
- 需求轴 4：`Summary` 注释写明两个数字统计的是不同人群。

## 第 4 轮（验证轮）

两轴均只剩一条硬项，且指向同一处：

- `open_issue_count` 用 `status NOT IN ('done','cancelled')`，只覆盖内置终态键。启用自定义终态（`issue_status.category` 为 `done` / `closed`）的工作区会把已关闭 issue 计成未关闭，`summary` 同源偏高。

### 返工处置（第 4 轮）

- 改用迁移 340 / 494 提供的 `issue_effective_status(i.workspace_id, i.status)`。
- `store_test` 补 `runs` / `failed_runs` 的窗口计数覆盖；`AT TIME ZONE $1::text` 显式转换。

### 判定

**通过**。修复后规范轴硬违规为 0、需求轴无缺失与实现错误；其余为判断题与已记录取舍，不阻断。

## 已接受的取舍（判断题，不阻断）

- **未加客户端 zod schema**：本切片没有 UI 消费者，接口由 CLI / PAT 直调；界面切片落地时按 `parseWithFallback` 约定补。
- **文档改动超出 brief §3 列出的三处**：仓库规范要求同 PR 更新所有受影响页面及其语言兄弟（6 个 locale），`.env.example` 是开关的规范清单，`SELF_HOSTING.md` 是同一开关的入口说明。
- **改动 `handler/runtime.go`**：为消除第 1 轮指出的重复窗口解析与两套时区校验；行为等价，既有 dashboard 测试通过。
- **保留 `instanceadmin.Querier` 与 nil store 的 503**：handler 的 DB executor 本身就是接口，store 依赖它而不引入第二个连接池；无库的 handler 构造在测试里存在，失败关闭优于 panic。
- **owner 三列扫描块在 `workspaces()` / `agents()` 各写一遍**：`rows.Scan` 的列顺序与 `ownerOrNil` 已共用，再抽象会牺牲可读性。
- **返工轮次**：第 3、4 轮只处理判断题级整理与一条 SQL 正确性问题，未触碰已批准方案，也无 spec 冲突或需人拍板项，因此未按 `needs-human` 升级。

## 验证

- `go build ./...`、`go vet ./internal/instanceadmin ./internal/handler ./cmd/server`：通过。
- `gofmt`：新增文件无待格式化项。
- `go test ./internal/handler ./internal/instanceadmin ./cmd/server -count=1`（完整挂载仓库、指向迁移到最新版本的测试库）：三个包全部通过。
