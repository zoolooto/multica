# NEPT-4 代码评审

固定点：`6162961a7`（`origin/feature/nept-7-multica-upstream-issues` 上的合入点）
评审范围：`6162961a7..b26042c46`（实现 4 个 commit）
评审输入：`brief.md`（分诊结论与方案）、`AGENTS.md`（仓库规范）
方式：规范轴、需求轴各由一名只读评审者独立进行，先出结论再判定

---

## 第 1 轮

### 规范轴

结论：需改。

硬违规：

1. `server/internal/handler/issue_wakeup_test.go` 把「详情回显的指令就是登记时那条」从精确比较改成 `strings.HasPrefix`。为了绕开新幂等而给 5 条规则改用不同指令是合理的，但放宽断言后，行与行之间串号、指令被截断或追加都不会被发现，这条回归的唯一守门处就失效了。
2. `apps/docs/content/docs/cli.mdx`、`cli.zh.mdx` 写的是「重复登记同一条规则会返回既有那条」，而实现只对 `kind=event` 生效；`at` / `every` / `cron` 规则重复登记仍会新增。同页上一句又刚提到 `--until-issue`，读者会自然以为时间类规则同样去重。同时 `issue-wakeups.mdx`（讲「一条规则怎么算一条」的页面）没有记录去重语义。
3. 去重查询漏了 `system_rule IS NULL`。仓库里按智能体遍历其订阅的既有查询（`wakeup.sql` 的 `:71`、`:120`、`:307`）都显式排除系统规则；这里没有，平台系统规则与调用方请求同签名时会被当成调用方的订阅原样返回。
4. 注释宣称「issue 行锁让等值调用串行化」，但三个测试全是串行调用，没有任何测试覆盖并发——而被修复的失败模式正是「重试后出现重复规则」。

异味（不挡）：先 `SELECT id` 再逐行 `GetIssueWakeup` 的 N+1；SQL 谓词与 Go 判等两层过滤同一批字段，容易漂移；`--wake-me-on` 已是 pflag `StringSlice`，函数内再 `strings.Split` 属于冗余路径；`wakeMeOnInstruction` 的 `default` 把未知状态静默当 `in_review`；新文件 import 分组与同目录 `cmd_issue.go` 不一致；容量测试把循环上界 32 硬编码进指令串；`platform_only_reason` 那句「Cross-issue subscriptions are not supported」是顺带修正的范围外改动，PR 描述里要写明。

做对的地方：`LockWakeupIssue` 前提成立；事件集合排序比较、`pgtype.Timestamptz` 按值比较两处细节正确；CLI 对「部分注册失败」明确写出「任务已创建、不要重试创建」的方向正确；`issue-wakeups.mdx` 的 `blocked` 修正与服务端条件实现一致，语言兄弟文件已同步。

### 需求轴

结论：需改。

缺失或实现错误：

1. **（阻断）`--wake-me-on` 生成的 body 缺 `kind`。** `wakeMeOnBody` 只发 `mode` / `instruction` 和 `condition` 或 `event_types`，而服务端 `WakeupInput.Kind` 没有默认值：条件形态先撞「conditions use kind event」，事件形态落进 `kind must be event, at, every or cron`。三个状态对真实服务端 100% 注册失败，功能等于没实现。CLI 测试用假服务器接受任意 body，正好掩盖了它。
2. 半成功的「补救命令」不可用：错误里给的 `multica issue wakeup create <key>` 不带任何 flag，会以「instruction must contain 1–12000 bytes」被拒；句尾 `before retrying` 读起来像「修好再去重试 create」，而重试会建出第二张票。
3. 任意注册失败都被断言成「服务端不接受 agent 身份」，网络中断、500、容量上限、以及上一条的 400 都被误导成身份问题。
4. 并发只落一行没有测试；「命中幂等时不记 created 活动、`revision` 不变」也没有测试。
5. CLI 测试没有断言 `kind`，是上一条能通过自测的原因。
6. 其余语言（fr / ja / ko / zh-Hant）仍是旧的错误措辞：`done` 重复、缺 `blocked`。brief 只要求 en/zh，不判缺失，但上游被误导的正是这句。
7. `--wake-me-on` 帮助文案写「woken once」，与规则实际是 `mode=continuous`、文档写「每次状态变化唤醒一次」不一致。

范围外行为：未发现越界改动——默认唤醒行为、`child_done` 系统规则、squad 祖先归属、`other_issue` 状态集合都没动。

需要人拍板（不挡本轮，记录在此）：

- 幂等签名不含 `created_by`，另一位成员同签名时 `POST /wakeups` 会返回别人的规则并回 201，随后该调用者 disable / update 会被 403。
- 被平台暂停（`enabled=false`）的规则不参与签名匹配，再登记一次会新起一条 enabled 规则。这是改动前就有的行为，但与文档并读会被误读成「暂停也能恢复」。

### 判定

`fix`。两侧都是可在本票范围内修掉的硬违规与实现错误，无 spec 冲突、无需人拍板的必改项。第 1 轮返工处置见下。

### 返工处置（第 1 轮）

- 规范轴 1：`issue_wakeup_test.go` 改回逐行集合判等，并额外守住「不缺、不重复」。
- 规范轴 2：`cli.mdx` / `cli.zh.mdx` 把去重限定为事件或条件规则；`issue-wakeups.mdx` 及全部语言兄弟补一条「重复登记」说明；`cli` 其余语言兄弟补齐 `--wake-me-on` 段落与 flag 表条目。
- 规范轴 3：去重查询加 `system_rule IS NULL`；同时把 `created_by` 纳入匹配，避免返回别人的规则后又被 403，并在注释里写明这条约束和「更新路径没有去重对象」的不对称。
- 规范轴 4、需求轴 4：加并发用例（4 个调用同时登记同一签名，断言只落一条且返回同一 id）与「不重复记 `wakeup_created`、`revision` 不变」用例。
- 需求轴 1、5：body 补 `kind=event`；CLI 测试断言 `kind`。
- 需求轴 2、3：错误按状态逐条给出可直接复制的 `wakeup create` 命令，明说不要再建一次；只有 403 才归因身份。
- 需求轴 6、7：其余语言补 `blocked`；帮助文案改为「每次进入 / 变为该状态」。
- 需求轴「需要人拍板」两条：本轮不改实现，记录在案，收尾说明里保留。

---

## 第 2 轮

评审范围：`6162961a7..5ac2ae43d`（返工提交 `5ac2ae43d`）

### 规范轴

结论：通过。

第 1 轮 4 条硬违规逐条复核：

1. `issue_wakeup_test.go` 改回集合判等：5 条指令收进 `served`，逐行「必须在集合内、命中即删」，结束时集合为空。缺一条与重复一条都会失败，串号、截断、追加都抓得到。
2. 去重范围：`cli.mdx` / `cli.zh.mdx` 限定为「事件或条件规则」，`issue-wakeups` 六种语言新增「重复登记」条目，与实现 `in.Kind == "event"` 一致。
3. 去重查询加上 `system_rule IS NULL` 与 `created_by`，与仓库既有按智能体遍历订阅的查询一致；`created_by` 在进入该分支前已通过非空校验，不会出现 `= NULL` 永不命中。
4. 并发与活动记录用例已补，计数限定在自己的 fixture 上，符合共享数据库的断言规则。

返工未引入新的硬违规。异味（不挡）：去重仍先 `SELECT id` 再逐行取，属 N+1；SQL 谓词与 Go 判等两层过滤同一批字段；并发用例没有启动栅栏，未必真的压到行锁；`wakeMeOnInstruction` 的兜底与 `wakeMeOnCommand` 的 `strconv.Quote` 都依赖当前三条固定文案；fr / ja / ko 的示例仍用英文标题。

### 需求轴

结论：需改。

第 1 轮 7 条全部核实修好：`kind=event` 全链路可被服务端接受（带条件的 `kind=event` 不再要求 `event_types`，`task.failed` 在可订阅事件内）；补救命令五个 flag 都存在且语义一致，`--instruction` 用 Go 字面量加引号，当前文案可安全粘贴；只有 403 才归因身份；并发与活动用例已补；CLI 测试断言了 `kind`；六种语言文档齐平；帮助文案不再说「只唤醒一次」。

新发现（须改，本票范围内）：`sameWakeupSignature` 先比 `expiry_seconds`、随后又比 `expires_at` 的绝对时刻，而相对期限是按「当前时间 + N」算出来的，同一个 `--expires-in 72h` 两次调用必然落到不同时刻，等值判定必然失败。后果是文档推荐的最常用设限期写法在「请求成功但响应丢失」后重试仍会落下第二条 enabled 规则——正是本票要消灭的「事件到达各起一次 run」，也与本轮刚写进六种语言的「重试是安全的」直接矛盾。

### 判定

`fix`。第 2 轮返工处置：

- `sameWakeupDeadline` 改为接收既有规则与新建参数：两者都带相对期限时只比秒数，否则才比绝对时刻（`--expires-at` 仍按值比较，`--expires-at` 与 `--expires-in` 混用判为不同规则）。
- 补 `TestIssueWakeupCreateMatchesARelativeDeadline`：同一 `expires_in` 两次登记只落一行；不同秒数、绝对期限都不许并进第一条。
- `registerCreateWakes` 增加未知状态防护，避免 `wakeMeOnInstruction` 的空字符串兜底变成一条空指令规则。
- `cli.mdx` / `cli.zh.mdx` 补一句：该开关需要智能体运行的身份，终端里先建任务再 `wakeup create --agent-id`。
- 需求轴的非阻断观察（错误文本句号后接小写、命令表 `update` 行、假服务器集成度、N+1）本轮不改，记录在案。

---

## 第 3 轮

评审范围：`6162961a7..728e2f03d`（返工提交 `728e2f03d`）

### 规范轴

结论：需改（1 项，文档）。

`728e2f03d` 的代码部分没发现问题：新用例对旧实现会失败，是真正的回归守卫；注释写清了「相对期限两次登记必然落到不同时刻，所以时长才是签名」；`sameWakeupDeadline` 的改动正是第 2 轮需求轴指出的正解。

唯一硬违规：`728e2f03d` 顺带在 `cli.mdx` / `cli.zh.mdx` 里加的那句给了跑不通的命令（缺 `--instruction` 与触发条件、`--agent-id` 占位符写成名字），而且这句与同页 :243 已有的说明重复，只同步了两种语言。评审者建议删除或补全并同步六语言。

异味（不挡）：`registerCreateWakes` 新增的状态校验不可达，落点更该是删掉 `wakeMeOnInstruction` 的空字符串兜底；`sameWakeupDeadline` 的注释说「双方都带」而代码是「任一方带」（因前面已强制相等，行为等价）；过期未扫描的规则仍参与匹配；文档没写「重复登记不会延长期限」。

### 需求轴

结论：通过。

第 2 轮阻断项已修好：相对期限命中、不同秒数不命中、绝对期限仍按时刻比较三条语义都成立，其中前两条有测试守住；未知状态防护堵住了空指令路径；`cli.mdx` / `cli.zh.mdx` 的补充说明核心事实正确（缺 `agent_id` 时服务端 400、终端可先建任务再为指定 agent 登记）。

新发现（非阻断）：绝对期限的合并 / 分开没有测试守门（第三条断言被前面的秒数不等短路）；按秒数去重的副作用（重试不刷新期限，要延长应走 `update`）没写进文档；注释措辞与代码「任一方带」不一致；语言兄弟不齐。

### 返工处置（第 2 轮之后，评审结论出具后立刻执行）

- 删除 `cli.mdx` / `cli.zh.mdx` 里那句跑不通的说明，恢复成 :243 已覆盖的六语言齐平状态（`c6ce9ad0e`）。原文要求「删除或补全并同步六语言」，删除消除了重复与语言漂移。
- 同一评审者复核时指出同页 :154 的跨 issue 示例同样缺 `--instruction`（来自 `b26042c46`，前两轮漏检），六种语言同步补上（`4a04af729`）。复核确认：该条为此前遗漏，补上后六语言一致。

### 判定

`pass`。两轴在返工后的发现全部关闭：

- 规范轴：唯一硬违规按评审者建议删除已解除，复核者据此判定「只看本轮返工项的话，通过」；复核新提的 :154 示例缺参也已在同一轮修掉，并核对过本票新增的全部文档命令都带 `--instruction`。
- 需求轴：第 2 轮阻断项关闭，整票已判通过。

本轮之后未再起新一轮评审：返工两次 + 一次文档删除，剩余项是文档措辞与不挡的异味，按规则不再进入返工循环。仍未解决、留给人的两条见第 1 轮「需要人拍板」，另记：绝对期限分支缺测试守门、按秒数去重不延长既有期限（应走 `wakeup update`），建议后续单开小票补文档与用例。


