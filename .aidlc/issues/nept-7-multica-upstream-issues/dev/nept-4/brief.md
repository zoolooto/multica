# NEPT-4 分诊结论：可实施（限定范围）

上游 Issue：https://github.com/multica-ai/multica/issues/10790
标题：Wake a squad leader or agent when an issue it created is delivered, blocked, or fails, and give blocked decisions an addressee

## 结论

可 AFK 实施，但只做「让已有的订阅能力可用、可发现、可重复调用而不会重复消费 run」这一层。
不上调任何默认唤醒行为，不做需要产品决策的收件人语义。

## 证据（源码核对，HEAD `6162961a7`）

1. **跨 issue 订阅其实已经支持**：`server/internal/service/issue_wakeup_condition.go:194-216` 的 `other_issue` 条件已接受 `done` / `ended` / `in_review` / `blocked` 四态，`server/cmd/multica/cmd_issue_wakeup.go:85-86` 也有 `--until-issue` / `--until-issue-state`。
   但 `cmd_issue_wakeup.go:24` 的 `platform_only_reason` 仍写着「Cross-issue subscriptions are not supported」，与上面两处直接矛盾 —— 报告作者正是被这句误导（上游讨论串已确认）。而 `conditions` 同一行又列出了 `--until-issue`，所以这条自相矛盾的文案在同一个 JSON 里同时出现两种说法。
2. **文档同样漏了 blocked**：`apps/docs/content/docs/issue-wakeups.mdx:60` 与 `apps/docs/content/docs/issue-wakeups.zh.mdx:60` 的「Linked / Another issue's status」一格写成「done, done or cancelled, or moves to In Review」/「完成、完成或取消，或进入审核中」：`done` 重复、`blocked` 缺失，而代码实际支持 blocked。按文档配置的读者不会知道能等 blocked。
3. **创建接口没有幂等保护**：`server/internal/service/issue_wakeup.go:519-640` 的 `save()` 创建分支直接 `CreateIssueWakeup`，没有任何「同签名规则已存在」的比较；重复注册即多一条 enabled 规则，事件到达时各起一次 run（付费 run 重复）。
4. **创建方拿不到「投递/阻塞/失败」的通知**：`issue create`（`server/cmd/multica/cmd_issue.go:1713` 起）只发一次 POST `/api/issues`，没有任何订阅动作；而 `task.failed` 本来就属于可唤醒事件（`server/pkg/eventcontract/eventcontract.go:34-46`），`in_review` / `blocked` 也能用 `issue_field` status 条件表达。也就是说缺的不是平台能力，而是「一次调用把它挂上」的入口。
5. **失败场景没有条件态**：`other_issue` 只判状态，run 失败后子票停在 `in_progress`，状态条件永远不成立 —— 报告所述「失败无人知晓」成立。这条本票用「创建方在本票上订阅 `task.failed` 事件」覆盖，不需要给 `other_issue` 加新语义。

## 方案

1. **`multica issue create --wake-me-on <states>`**（只改 CLI，`server/cmd/multica/cmd_issue.go`）
   - 取值 `in_review` / `blocked` / `failed`，逗号分隔或重复 flag。
   - 语义 = 「issue 创建成功后，把结果通知创建它的这个 agent 一次」。issue 建完后在该新 issue 上注册：
     - `in_review` / `blocked` → `condition={"type":"issue_field","field":"status","value":…}`，`mode=continuous`（每次进入该状态都唤醒一次，覆盖二次评审）；
     - `failed` → `event_types=["task.failed"]`，`mode=continuous`（默认 `max_fires` 20 兜底，沿用现有 runaway 保护）。
   - `agent_id` 不显式传，交给服务端按调用方身份默认（`server/internal/handler/issue_wakeup.go:213`）；非 agent 身份调用时服务端会拒绝，CLI 按「issue 已创建、订阅未登记」+ 可直接复制的补救命令报错，沿用 `verifyIssueCreateProperties` 已有的半成功措辞。
   - 指令文案由 CLI 生成，说明「你创建的任务已交付/阻塞/失败，去读最新进展并决定下一步」，让 run 无需猜上下文。
2. **`wakeup create` 幂等**（`server/internal/service/issue_wakeup.go`）
   - 创建分支已持有 issue 行的 `FOR NO KEY UPDATE`（`LockWakeupIssue`，`server/pkg/db/queries/wakeup.sql:60`），因此同 issue 的并发创建天然串行，无需新索引或迁移。
   - 在该锁内、`CreateIssueWakeup` 之前：仅对 `kind=event` 的规则，取本 issue 同 agent 的 enabled event 规则，按「条件结构 + 事件集合（排序后）+ mode + 筛选器 + parent + 指令」比较；完全一致则直接返回既有规则（不新建、不记 created 活动、不动 revision）。
   - 比较用结构体而非 jsonb 字节：jsonb 会重排键，`jsonb` 文本与 `json.Marshal` 输出不等价。
3. **修帮助文案**（`server/cmd/multica/cmd_issue_wakeup.go`）
   - `platform_only_reason` 改为只描述 lifecycle 事件（`issue.created` / `issue.deleted`）为何不能自订阅，并点明跨 issue 条件由 `--until-issue` 提供。
4. **文档**（en + zh）
   - `issue-wakeups.mdx` / `issue-wakeups.zh.mdx` 第 60 行：`done` 去重、补 `blocked`。
   - `cli.mdx`：`issue create` 段落加 `--wake-me-on` 用法（含「在自己的任务上挂 `--until-issue` 观察无父票的独立任务」这一现有手法）。

## 不做的事（记录取舍）

- **不改 `child_done` 默认、不默认在 `blocked` 唤醒 agent**：上游维护者明确担心全工作区付费 run 成本，本票只提供 opt-in 入口。
- **不做 `--waiting-on`**：`blocked` 的收件人语义需要产品决策（通知渠道是 inbox / 平台代发 mention 评论 / 直接起付费 run；默认取 creator 还是 requester），属 HITL，不在本票硬猜。
- **不改 wakeup 的 squad 归属到祖先 issue**：上游维护者明确拒绝把 squad 权限继承到成员所属 issue；子票场景已由「在 squad 所属父票上挂 `--until-issue <子票>`」覆盖（报告作者本人也确认）。
- **不动 `other_issue` 的状态集合**：失败场景用 `task.failed` 事件覆盖，比给条件加「run 失败」新语义面更小。

## 测试计划

- `server/cmd/multica`：`--wake-me-on` 的取值解析（非法值报错、去重）、创建后按状态 fan-out 出正确 body、非 agent 身份的半成功报错；沿用同包既有 httptest 风格。
- `server/internal/service`：幂等命中（同签名返回同一 id、不新增行）、签名不同（指令/筛选器/事件集合不同）仍然新建、并发两次创建只落一行（依赖 issue 行锁）。
- 文档：`git diff --check`；en/zh 两处表格一致。
- 工具链：本机无 Go，使用仓库自带的 `docker-group.fzzixun.com/golang:1.27.4-alpine` 镜像编译与跑包级测试。
