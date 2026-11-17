# NEPT-5 分诊结论：可实施（限定范围为实例级只读视图与身份闸门）

上游 Issue：https://github.com/multica-ai/multica/issues/10779
标题：Instance-level admin console for self-hosted deployments (all workspaces, users, agents + statistics)

## 结论

可 AFK 实施，但只做「实例级只读视图 + 由配置授予的实例管理员身份」这一层：

- 一个由 `INSTANCE_ADMIN_EMAILS` 授予的实例管理员身份（默认关闭，仅自托管有意义）。
- 一组实例级只读接口，一次回答上游列出的四个问题：有哪些工作区 / 有哪些用户、各自属于哪些工作区 / 跨工作区的 agent 与 runtime 及其在线状态 / 实例级用量（runs、失败、token、成本），无需查库。
- 不做界面、不做任何写操作（转移所有权、停用登录、归档/删除工作区）、不做读取工作区内容（issue / comment / chat）、不做审计日志。

界面与写操作留给后续切片，理由见文末「不做的事」。

## 证据（源码核对，HEAD `e3813b08d`）

1. **没有实例级角色**：角色只存在于工作区成员关系上（`server/migrations/001_init.up.sql` 的 `member.role CHECK (role IN ('owner','admin','member'))`），没有实例级角色表或字段；`server/` 全量搜索 `INSTANCE_ADMIN` / 实例管理员无命中。`server/internal/handler/workspace.go` 的每个管理接口都经 `RequireWorkspaceRoleFromURL(queries, "id", "owner", "admin")`，即「对该工作区的」owner/admin。
2. **没有跨工作区视图入口**：`GET /api/workspaces`（`server/internal/handler/workspace.go:161`）走 `ListWorkspaces`（`server/pkg/db/queries/workspace.sql:1-8`），SQL 是 `FROM member m JOIN workspace w ... WHERE m.user_id = $1`，只返回调用者自己加入的工作区。其余列表（成员、agent、runtime、issue、usage）都需要 URL 里的 workspace id 并经成员/角色校验。
3. **现有的「运营商」配置只有开关，不是角色**：`server/cmd/server/router.go:435-441` 只把 `ALLOW_SIGNUP` / `ALLOWED_EMAILS` / `ALLOWED_EMAIL_DOMAINS` / `DISABLE_WORKSPACE_CREATION` / `WORKSPACE_CREATION_ALLOWED_EMAILS` 读进 `handler.Config`。`SELF_HOSTING_ADVANCED.md:108` 明确写着：`WORKSPACE_CREATION_ALLOWED_EMAILS`「only controls who can create a workspace. It is not an instance-admin role: listed users get no visibility into, or management rights over, workspaces they are not a member of.」——上游 Issue 指出的正是这个缺口。
4. **统计只有工作区维度**：`GET /api/dashboard/*`（`server/cmd/server/router.go:2532`）的六个序列全部按 `workspace_id` 过滤（`server/internal/handler/dashboard.go` 顶部注释与 `server/pkg/db/queries/task_usage.sql` 的 `ListDashboardUsageDaily` 等）。实例级聚合所需的底层数据已经存在且带 `workspace_id`：`task_usage_hourly`（`server/migrations/101_task_usage_hourly_schema.up.sql`）与权威成本列（`server/migrations/213_task_usage_authoritative_cost.up.sql` 的 `cost_usd_ticks`）、`agent_task_queue`（`server/migrations/001_init.up.sql:127`）、`agent_runtime.status` / `last_seen_at`、`client_usage_daily.last_active_at`（`server/migrations/207_client_usage_daily.up.sql`）。缺的是读取入口，不是数据。
5. **没有任何 `/admin` 前端路由**：`apps/web/app/` 下只有 `[workspaceSlug]`、登录/邀请、落地页等；搜索 `admin` 无命中。

外部主张核对：
- 「自托管的运营商只能靠环境变量 + 直连 SQL」：源码 confirmed（第 1–3 条）。
- 「无法跨工作区看到 agent / runtime / 用量」：源码 confirmed（第 2、4 条）。
- 「`INSTANCE_ADMIN_EMAILS` 类似 `SUPER_ADMIN_EMAILS`（#4118）」：本仓库无 `SUPER_ADMIN_EMAILS` 相关实现，无法交叉验证；本票不依赖该前提，只借用「按邮箱授予、默认关闭」这一形状。

## 方案

### 1. 身份：`INSTANCE_ADMIN_EMAILS`

- 新环境变量，逗号分隔的精确邮箱（trim + 小写比较，空项忽略）。默认空 = 功能关闭。
- 只读 `server/cmd/server/router.go` 的一处，写入 `handler.Config.InstanceAdminEmails`；`handler.New` 里编译成集合，挂在 `Handler.InstanceAdmins`。
- 判定输入是**数据库里的账号邮箱**（`GetUser` 按 `X-User-ID` 取），不用 `X-User-Email` 头：该头只在 JWT 分支设置，PAT 调用没有，按头判定会让 CLI/PAT 永远拿不到身份。
- 机器凭据（`mat_` 任务令牌、`mcn_` 云 PAT，即 `isMachineCredentialActor` 为真）一律拒绝：实例级视图是运营商的人读面，不应被提示词注入的 agent 借其 owner 的身份拉走全实例数据。
- 未配置或邮箱不在列表 → 403，文案不区分二者，不泄露「存在这个功能但你没被授权」。

### 2. 只读接口

挂在已认证的用户级路由组下（`server/cmd/server/router.go` 的 `r.Group` 内，`middleware.Auth` 之后）：

- `GET /api/instance/access` → `{"is_instance_admin": bool}`。这是给未来 UI 判断入口用的探针；本身不做管理员校验，只回身份。
- `GET /api/instance/overview?days=N&tz=<IANA>` → 一次返回整份实例视图。`days` 默认 30、上限 365（与既有 dashboard 一致），`tz` 缺省按 `resolveViewingTZ`（`?tz=` → 用户存储时区 → UTC）。响应分为：
  - `summary`：工作区数、用户数、agent 数、runtime 数 / 在线数、未关闭 issue 数，以及窗口内的 runs / failed_runs / token / `cost_usd_ticks`。
  - `workspaces`：每个工作区的 owner（最早加入的 owner）、成员数、agent 数、runtime 数 / 在线数、未关闭 issue 数、最后活动时间。
  - `users`：每个账号的邮箱、创建时间、所属工作区与各自角色、最后活动时间（`client_usage_daily.last_active_at` 的最大值，无则 `null`）。
  - `agents`：跨工作区的 agent（排除归档与 `kind='system'` 载体），带所属工作区、owner、runtime provider / 在线状态 / `last_seen_at`、`model`、最后一次 run 时间。
  - `usage`：窗口内按（日期、工作区、provider、model）分组的 token / 成本 / 任务数序列，日期按 `tz` 切日。
- 数据取自 `workspace` / `member` / `"user"` / `agent` / `agent_runtime` / `issue` / `agent_task_queue` / `task_usage_hourly` / `client_usage_daily`，全部只读、不带任何写路径。
- 不新增迁移、不新增 sqlc 查询：SQL 放在新包 `server/internal/instanceadmin`，用 handler 已有的 `h.DB` 执行（与 `issue_table_rows.go` 同类做法一致，避免为一张新报表重跑 sqlc 代码生成）。

### 3. 文档

- `SELF_HOSTING_ADVANCED.md`：新增「Instance administrator (read-only)」小节，写清 `INSTANCE_ADMIN_EMAILS` 的语义、两个接口、以及**没有**授予什么（不是工作区 owner/admin、看不到工作区内容、没有写操作）。
- `docker-compose.selfhost.yml`：透传 `INSTANCE_ADMIN_EMAILS`。
- `deploy/helm/multica/values.yaml` + `templates/configmap.yaml`：与 `workspaceCreationAllowedEmails` 同形，加 `instanceAdminEmails`。

## 不做的事（取舍记录）

- **不做前端界面**。上游想要的 `/admin` 是一个完整的信息架构（导航入口、四个页签、分页、图表、写操作的确认流程），而本票的写操作与界面都需要产品决策：只读视图是否单独成页、放哪个入口、是否给非 owner 成员可见。先把权威只读模型与身份闸门落定，界面直接消费 `/api/instance/overview` 即可，不必二次改协议。
- **不做任何写操作**：转移所有权（#3430）、停用/启用登录（#1688）、重命名（#4118）、归档/删除工作区。这些都会改动他人工作区与账号，必须有确认流程与审计，属于独立切片。
- **不做审计日志**：没有写操作就没有要审计的动作；等写操作切片一起设计。
- **不开放工作区内容读取**：上游自己也把它列为「deliberate, separate decision」。本票只给元数据与统计。
- **不把实例管理员自动当成每个工作区的 owner/admin**：那会污染成员列表并让工作区权限判定失去意义（上游 Alternatives 已否定）。
- **不加 `SUPER_ADMIN_EMAILS` / 用户管理写接口**：`#4118` 是独立 Issue，且无现有实现可复用。

## 测试计划

- `server/internal/instanceadmin/admins_test.go`：空配置全拒；大小写与首尾空白归一；重复项无害；未命中拒绝；`Configured()` 语义。
- `server/internal/handler/instance_admin_test.go`（需要数据库的集成测试，与同包其余测试同一套 fixture）：
  - 未配置 → 403；配置了但邮箱不在列表 → 403；命中 → 200 且 `is_instance_admin=true`；
  - 任务令牌（`X-Actor-Source: task_token`）即使是管理员邮箱 → 403；
  - `days=0/-1/400/abc` 回落默认 30，`days=7` 生效；
  - `tz=Asia/Shanghai` 与 UTC 的切日差异体现在 `usage[].date`；
  - 空库（只有测试工作区）时各列表不为 `null`，`summary` 自洽（工作区数 = `workspaces` 长度等）。
- `server/internal/instanceadmin/store_test.go`：SQL 聚合正确性（跨两个工作区各造一行 `task_usage_hourly` 与 `agent_task_queue`，断言窗口过滤、按工作区分组、成本与 token 求和、归档 agent 与 `kind='system'` 被排除）。
- 工具链：本机无 Go，使用仓库已有的 `docker-group.fzzixun.com/golang:1.27.4-alpine` 镜像编译与跑包级测试；数据库测试用 `pgvector/pgvector:pg17` 起临时库并跑 `cmd/migrate`。
