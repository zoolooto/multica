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
