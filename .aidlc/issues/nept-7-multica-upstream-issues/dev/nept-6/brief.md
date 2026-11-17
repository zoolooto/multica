# NEPT-6 分诊结论：可实施（限定范围为「外部状态 + 工作区默认策略 + 三态覆盖」的后端与协议）

上游 Issue：https://github.com/multica-ai/multica/issues/10776
标题：Runtime-inherited skills should honor external disable state and support a workspace-level default policy

## 结论

可 AFK 实施，只做上游提案 1–3、5 这一层：

- 守护进程发现运行时本地技能时，顺带报告该技能在外部工具自身配置里是否被禁用（`external_enabled`）。
- 新增工作区级默认策略 `runtime_skills_default_policy`（`all_enabled` / `all_disabled` / `follow_external`，缺省 `follow_external`）。
- 每个 agent 的技能开关从「只能禁用」升级为三态（继承 / 强制开 / 强制关），既有禁用记录继续表示「强制关」，用户当前配置语义不变。
- 任务环境注入逻辑本身不改（`execenv/runtime_skill_policy.go` 一行不动），只是它的输入改为「按策略算出来的有效禁用集」。

界面（提案第 4 条：agent 技能页三态开关与批量「恢复继承」、工作区设置页的策略项）不在本票，理由见文末「不做的事」。

## 证据（源码核对，HEAD `5779c2277`）

1. **发现过程只看目录存在**：`server/internal/daemon/local_skills.go:592` 起 `enumerateLocalSkills` 只做 `os.Stat(<dir>/SKILL.md)` + 解析 frontmatter，全仓库（`server/`）搜索 `skillOverrides` 只命中「写」的一侧（`server/internal/daemon/execenv/runtime_skill_policy.go:67-81` 生成 `skillOverrides`），没有任何「读」用户配置的代码。上游所述「Multica 与运行时机器不一致」成立。
2. **Codex 侧的外部禁用是被主动抹掉的，不只是没读**：任务环境会复制用户的 `~/.codex/config.toml` 到任务级 `codex-home`，然后 `sanitizeCopiedCodexConfig`（`server/internal/daemon/execenv/codex_home.go:252`、`execenv/codex_skill_strip.go:48-84`）把**整个** `[[skills.config]]` 数组删掉——注释写明这是为绕开 Codex CLI 对插件条目的解构错误。结果是用户在 Codex 里禁用的技能，在 Multica 任务里反而被重新启用：这是比「列表不一致」更实质的错配，也是 `follow_external` 在 Codex 上真正修掉的东西。
3. **Claude 侧是「可能已经生效但两边说的不一样」**：任务里只额外传一个 `--settings <path>`（`server/pkg/agent/claude.go:1223-1236`），用户自己的 `~/.claude/settings.json` 仍是底层设置层，所以 `skillOverrides` 大概率在运行时仍生效；但 Multica 的列表与开关完全不知道这件事（第 1 条），用户看到的仍是「开」。本票让发现结果报告真实状态，并在任务环境里把同一个禁用显式补上（冗余但一致）。
4. **开关是「单向 + 每 agent」**：`agents.disabled_runtime_skills`（`server/migrations/206_agent_disabled_runtime_skills.up.sql`）只存「禁用项」数组，写入路径 `server/internal/handler/agent_runtime_skills.go` 的 `SetAgentRuntimeSkillEnabled` 只能加/删禁用项，没有任何「强制开」的表达方式，也没有工作区级默认值。上游所述 N×M 点击矩阵成立。
5. **工作区级设置的位置现成**：`workspace.settings` 是 JSONB（`server/internal/handler/workspace.go:104`、`UpdateWorkspace` 直接整包写入），且守护进程已经缓存并读取它（`server/internal/daemon/daemon.go:3442-3480` 的 `workspaceCoAuthoredByEnabled` 就是同一形状），`UpdateWorkspace` 在 settings 变更时还会唤醒所有成员的守护进程（`server/internal/handler/workspace.go:528-542`）。因此策略不需要新增迁移、也不需要新的下发通道。
6. **任务侧的禁用集只按 runtime+provider 过滤**：claim 时 `disabledRuntimeSkillsFor(agent.DisabledRuntimeSkills, runtimeID, runtime.Provider)`（`server/internal/handler/daemon.go:3135`）把「该 runtime 上该 provider 的禁用项」发给守护进程，`daemon.go:8311` 转成 `execenv.RuntimeSkillRefForEnv`。这是本票改「输入」的唯一接缝。
7. **协议是请求/响应，没有持久化**：运行时技能列表由服务端经守护进程心跳请求、守护进程回报（`server/internal/daemon/daemon.go:5016-5045`、`server/internal/handler/runtime_local_skills.go:204-210`），服务端不落库。所以 `follow_external` 的有效状态只能在守护进程侧计算——服务端在 claim 时拿不到「哪些技能被外部禁用了」。这一条决定了方案的分工。
8. **外部工具的禁用格式本仓库已有两套写入实现**：Claude `skillOverrides: {<invocation name>: "off"}`；Codex `[[skills.config]] path = "<SKILL.md>" enabled = false`（`runtime_skill_policy.go:71,143`）。读取按同一形状反过来解析，不引入第三方格式猜测；TOML 解析已依赖 `github.com/pelletier/go-toml/v2`（`server/go.mod:24`）。

外部主张核对：

- 「禁用 Claude / Codex 里的技能后 Multica 仍显示启用」：源码 confirmed（第 1 条），Codex 侧还额外 confirmed 了任务环境反向启用（第 2 条）。
- 「继承技能默认开启、状态存在 `agents.disabled_runtime_skills`」：confirmed（第 4 条）。
- 「提案里的任务环境注入不用改」：confirmed，`execenv/prepareClaudeSkillSettings` 与 `ensureCodexDisabledSkillsConfig` 的签名与语义都按「禁用集」设计（第 6 条）。
- 「其他 provider 也能逐步支持」：本仓库只有 codex / claude 有禁用能力（`local_skills.go:657` 的 `CanDisable` 与 handler 的 400 校验），本票不为其他 provider 编造语义。

## 方案

### 1. 发现结果报告外部状态（守护进程）

- `runtimeLocalSkillSummary` 增加 `ExternalEnabled *bool`（JSON `external_enabled,omitempty`），上报链路（daemon → `handler.RuntimeLocalSkillSummary` → 客户端）透传。
- 新文件 `server/internal/daemon/local_skills_external.go`：
  - Claude：读 `~/.claude/settings.json` 的 `skillOverrides`，值等于 `"off"`（大小写不敏感）或 JSON `false` 视为**外部禁用**；其他值（含 `"on"`）不视为禁用。文件缺失或解析失败 → 未知（nil），不猜测。
  - Codex：读 `$CODEX_HOME/config.toml`（缺省 `~/.codex/config.toml`）的 `[[skills.config]]`，`path` 指向本发现根的 SKILL.md 且 `enabled = false` 视为**外部禁用**。
  - 匹配键：Claude 用技能调用名（frontmatter `name`，缺省目录名，与写侧 `runtime_skill_policy.go:56-59` 一致）；Codex 用 SKILL.md 的绝对路径（写侧 `:115,124` 同形）。插件根（`root=plugin`）不读 `skillOverrides`（写侧注释已说明 Claude 对插件忽略该设置），报 nil。
  - 只有 codex / claude 参与，其他 provider 一律 nil（「该 provider 没有这个概念」）。
- 发现函数把外部状态一次性读入（每次发现读一次配置，不是每技能一次），沿用现有 `enumerateLocalSkills` 的遍历，不改键、不改排序、不改去重。

### 2. 工作区默认策略（服务端校验 + 守护进程读取）

- `workspace.settings.runtime_skills_default_policy`，取值 `all_enabled` / `all_disabled` / `follow_external`，缺省与非法回落均为 `follow_external`。
- `UpdateWorkspace` 在写入前校验：该键存在但不是上述三值之一 → 400（不静默改写调用方的 payload）。其他 settings 键的行为不变。
- 守护进程在 `runTask` 里按 `task.WorkspaceID` 读缓存的工作区设置（新增 `workspaceRuntimeSkillsDefaultPolicy`，与 `workspaceCoAuthoredByEnabled` 同形同锁），不新增 claim 字段、不新增查询。

### 3. 每 agent 三态覆盖

- 存储沿用 `agents.disabled_runtime_skills` 这一列（避免为一个 JSONB 数组的形状改动跑迁移与 sqlc 重生成），数组元素增加可选 `mode`：
  - 缺省 / `"off"` → 强制关（**既有记录的语义完全不变**，无需数据迁移）；
  - `"on"` → 强制开；
  - 元素不存在 → 继承（交给策略）。
- 写入接口 `SetAgentRuntimeSkillEnabled` 接受两种 payload：
  - 新：`mode: "on" | "off" | "inherit"`（`inherit` 删除该条）；
  - 旧：`enabled: true` 映射为 **删除该条（= 继承）**，`enabled: false` 映射为强制关——与今天完全一致，老客户端语义不漂移。
- 响应保持兼容：`disabled_runtime_skills` 仍只含强制关的条目；新增 `runtime_skill_overrides` 列出三态全量（含 `mode`），老客户端看不到也不受影响。

### 4. 有效状态计算（守护进程，唯一执行点）

- 新文件 `server/internal/daemon/runtime_skill_effective.go`：`resolveRuntimeSkillDisables(provider, policy, overrides, runtimeID)` → `[]execenv.RuntimeSkillRefForEnv`，替换 `daemon.go:8311` 的 `convertDisabledRuntimeSkillsForEnv`。
- 规则（`effective = override ?? policy`）：
  - 强制关 → 禁用；强制开 → 不禁用（策略不再参与）。
  - 继承 + `all_enabled` → 不禁用（维持今天的行为）。
  - 继承 + `all_disabled` → 禁用（走一次发现，拿到全部被继承技能的键与调用名）。
  - 继承 + `follow_external` → 外部禁用则禁用，否则不禁用（直接读外部配置，不做目录遍历）。
- provider 限制：只有 codex / claude 参与策略计算，其他 provider 退回「只有强制关」的今天行为；发现失败或外部配置不可读时记日志并退回同样行为，绝不因此让任务起不来。
- 传下去的结构与调用点不变：`execenv/runtime_skill_policy.go` 继续按禁用集写 `skillOverrides` / `[[skills.config]]`。

### 5. 文档

- `apps/docs/content/docs/skills.mdx:101` 及其 5 个语种兄弟（`skills.zh.mdx` / `zh-Hant` / `ja` / `ko` / `fr`）：说明继承技能默认跟随运行时工具自身的禁用配置（`skillOverrides`、`[[skills.config]]`），并指出工作区策略 `runtime_skills_default_policy` 的三个取值；写清它是快照（下次刷新继承技能时更新，无文件监听）。

## 不做的事（取舍记录）

- **不做界面**（提案第 4 条）。理由有三：一是本环境前端依赖安装失败（`.multica/state.json` 的 `deps.repos.multica.columns[0].state=failed`，`node_modules` 不存在），`pnpm typecheck / lint / test` 一条都跑不了，改界面等于交付无法验证的代码；二是「三态开关」在现有 `Switch` 组件里没有对应控件，怎么表达「继承 + 实际结果」是产品设计决策，不该由 AFK 运行硬猜；三是协议与默认值必须先落定，界面直接消费 `external_enabled` + `runtime_skill_overrides` + 策略值即可，不必二次改协议。界面作为下一张票。
- **不做批量开关 / 「恢复继承」按钮**：与界面同批，后端 `mode: "inherit"` 已经是它需要的接口。
- **不为其他 provider 提供外部状态**：本仓库只有 codex / claude 有禁用通道，其他 provider 报 nil 比编造语义诚实（上游自己也写「incrementally where a stable disable mechanism exists」）。
- **不改 `all_enabled` 下的「强制开」语义边界**：`all_enabled` 只表示「Multica 侧不额外禁用」，不去覆写工具自身配置把外部禁用的技能强行打开——那需要与各 CLI 的私有权重规则搏斗，且与「尊重用户已配置好的环境」直接冲突。
- **不做文件监听**：沿用上游已承认的限制，外部状态是发现时的快照。
- **不重命名 `disabled_runtime_skills` 列**：改名要跑迁移 + `make sqlc` 全量重生成，收益只是名字，成本是这票的风险面。

## 测试计划

- `server/internal/daemon/local_skills_external_test.go`：`skillOverrides` 的 `"off"` / `"on"` / 布尔 / 缺键 / 坏 JSON；`[[skills.config]]` 的 `enabled=false` / 缺 `enabled` / 非本发现根路径 / 插件条目 / 文件缺失；以及一次端到端发现（临时 HOME + `CODEX_HOME`）断言 `external_enabled` 的 true / false / nil 三态与「其它 provider 恒 nil」。
- `server/internal/daemon/runtime_skill_effective_test.go`：策略 × 外部状态 × 覆盖 的三维表驱动矩阵（含 `all_disabled` + 强制开、`follow_external` + 外部禁用、非法策略回落、强制关且未被发现也照样下发）；发现失败时的退化路径；非 codex / claude provider 不看策略。
- `server/internal/handler/agent_runtime_skills_test.go`：`mode` 解析（`on` / `off` / `inherit` / 非法值）、旧 `enabled` payload 的映射（true→继承、false→强制关）、响应里 `disabled_runtime_skills` 只含强制关而 `runtime_skill_overrides` 三态全量。
- `server/internal/handler/workspace_test.go`：策略三值通过、非法值 400、缺省回落 `follow_external`（纯校验函数级别，不需要数据库）。
- 工具链：本机无 Go，用仓库已有的 `docker-group.fzzixun.com/golang:1.27.4-alpine` 镜像（模块缓存卷）跑包级测试；前端只改 TS 类型声明，不跑前端测试并在结果里写明。
