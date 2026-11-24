# NEPT-6 代码评审记录

固定点：`5779c2277`（`feature/nept-7-multica-upstream-issues` 的 tip）。
评审方式：两轴并行只读 subagent（规范轴、需求轴），各自独立读取 diff、brief 与仓库规范，不共享实现取舍。

## 第 1 轮

### 规范轴

硬项：

1. **混合版本契约反转**（`handler/daemon.go` claim、`daemon/types.go`）。claim 把 `mode:"on"` 条目也放进 `disabled_runtime_skills`，旧守护进程不认 `Mode`，会把「强制开」当成「禁用」——方向相反。
2. **注释挂错函数**（`handler/agent_runtime_skills.go`）。`runtimeSkillMode` 的说明粘在 `resolveRuntimeSkillToggleMode` 上方，真正的目标函数没有注释。

判断题：

- `enabledFor` 的 `key` 参数全程未使用。
- 「只有 claude / codex 能禁用」的判断分散在四处。
- 三段同形的过滤循环与相近命名（`normalize…` / `…For` / `disabled…Overrides`）。
- `Mode` 是裸字符串，`skillpolicy.Valid` 与 `Normalize` 的宽容度不一致。
- `runtimeSkillRef` 逐字段镜像 `execenv.RuntimeSkillRefForEnv`。
- `validateRuntimeSkillsDefaultPolicy` 同一 `fmt.Errorf` 写两遍。
- `all_enabled` 下仍读外部配置文件，结果无人使用。

### 需求轴

1. 缺少「发现失败时的退化路径」测试（测试计划明列）。
2. 「非法值 400」只验到纯校验函数，`UpdateWorkspace` 的响应链路没有测试。
3. 文档六语种写了「每个智能体可强制开 / 强制关 / 继承」，但本票不做界面，现有 Skills 页发不出强制开——文档描述了一个界面上不可达的能力。
4. `follow_external` 下「强制开」对 Claude 不总成立：外部禁用项以调用名作 key，覆盖记录用发现键，identity 不相等时强制开压不住外部禁用。
5. 外部禁用项不区分根，插件技能名会被当成 provider 根下发 `skillOverrides: off`。
6. claim 的混合版本语义反转（与规范轴第 1 条同一处）。

### 返工处置（第 1 轮）

- 规范轴 1 / 需求轴 6：claim 拆成两个字段——`disabled_runtime_skills` 只放 off（旧守护进程看到的语义不变），新增 `runtime_skill_overrides` 放三态全量；新守护进程优先读新字段，缺失时回落旧字段。新增 `TestRuntimeSkillDisablesForTask_PrefersTriStateOverrides`。
- 规范轴 2：注释各归其位。
- 规范轴判断题：删除 `enabledFor` 未用参数；删掉 `runtimeSkillRef` 镜像类型，直接使用 `execenv.RuntimeSkillRefForEnv`；`Valid` 改成与 `Normalize` 相同的归一化，测试同步；`all_enabled` / `all_disabled` 下不再读外部配置；合并重复的 `fmt.Errorf`；顺手修掉 Claude 解析里重复的 `disabled[name] = true`。
- 需求轴 1：新增 `TestRuntimeSkillDisablesForTask_DiscoveryFailureFallsBackToOverrides`（让 provider 根 stat 返回非「不存在」错误，断言回落到显式覆盖）。
- 需求轴 2：新增 `TestUpdateWorkspace_RejectsInvalidRuntimeSkillsDefaultPolicy`（走 handler，断言 400 且 settings 未变）。
- 需求轴 3：六语种文档删去三态开关那句，只讲「继承跟随工具自身配置 + 工作区策略 + 快照」。
- 需求轴 4：除 identity 外再按调用名匹配覆盖，强制开可压住外部禁用项；新增 key 与调用名不同的用例。
- 需求轴 5：不新增目录遍历。`follow_external` 仍只读外部配置，因此无法区分同名插件技能与 provider 技能：外部配置里列出的名字一律按 provider 根下发，只会「更严格」，不会重新启用用户已禁用的东西。作为取舍记录。

### 判定

**需改**。规范轴两条硬项、需求轴四处缺失/错误，均已在本轮修复；第 5 条按取舍处理。
