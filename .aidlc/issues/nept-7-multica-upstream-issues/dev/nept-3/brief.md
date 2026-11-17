# NEPT-3 分诊结论：可实施（限定范围为客户端自愈与可见性）

上游 Issue：https://github.com/multica-ai/multica/issues/10805
标题：Chat 会话状态更新延迟及自动完成失败 — 手动中止后结果批量显示 (v0.10.0 Desktop Mac)

## 结论

问题成立，但不能靠源码单方面定位「事件为什么没到客户端」——那需要报告人的 WS 事件日志、任务 ID 与服务端任务状态（维护者已在 Issue 里点名索取）。本票据此把范围收敛到源码可确认的两个缺口：

1. 聊天视图没有任何**兜底对账**：一次进行中的会话，其状态与内容只靠 WS 事件推进；事件没到就只能等用户点「中止」或刷新。
2. 客户端没有任何**实时连接健康度**的用户可见表达：socket 掉了、或连着但对端不再投递，界面上与正常状态完全一样，用户无法判断该等还是该重连。

两者叠加正好复现报告的三段症状：先看不出在等什么（无流式、无思考过程）→ 状态不自动结束 → 手动中止触发一次权威刷新后结果「一次性全部显示」。

## 证据（源码）

1. 聊天缓存全部是事件驱动、`staleTime: Infinity`：
   - `packages/core/chat/queries.ts` 的 `pendingChatTaskOptions`、`chatMessagesPageOptions`、`taskMessagesOptions` 都设 `staleTime: Infinity`；
   - 只有 `packages/core/realtime/use-realtime-sync.ts` 里 `chat:message` / `chat:done` / `task:completed` / `task:failed` / `task:cancelled` 这几个 handler 会 invalidate 或直接写缓存；聚合 pending 还额外有 750ms 去抖。
   - 结论：终态事件（`chat:done` / `task:completed`）是清除 pending 的唯一自动路径。它没到，UI 就一直「进行中」。
2. 连接断开有自愈，但只在「检测到断开」时生效：`packages/core/api/ws-client.ts` 仅在 `onclose` 上 `scheduleReconnect`，重连成功后 `invalidateWorkspaceScopedQueries` 会 invalidate `chatKeys.*`。也就是说，报告里那种**卡到手动中止**的状态，恰恰是「客户端不认为自己断线」或「连着的一瞬间漏掉了终态帧」——这条路径没有任何兜底。
3. 没有兜底对账：`use-chat-controller.ts` 的 pending 只来自 query 缓存，没有任何轮询；`chat-window.tsx` 也没有「连接异常时重新拉一次」的逻辑。
4. 用户看不到实时连接状态：`packages/views/chat/components/offline-banner.tsx` 讲的是 **agent 可用性**（agent 离线/不稳定），与 WS 健康度无关；`ws-client.ts` 自己的注释写明「the web/desktop UI does not yet expose a visible disconnected state or manual retry action」。
5. 流式链路本身没有明显缺陷（供交叉验证，非本次改动）：`task:message` 帧由 `server/internal/handler/daemon.go` 落库后发布，按 `ChatSessionOwnerID` 投递到 `workspace_member` scope；客户端 `isTaskMessageTimelineHeld` 只丢弃「本次会话从未打开过」的任务帧。所以事件到达时流式显示是正常的——报告缺的是「事件没到」之后的处理。

外部主张核对：
- 「0.9.x 正常、0.10.0 起异常」：未能核实。0.9.0→0.10.0 之间 `packages/core/chat` 无改动，`packages/views/chat` 仅有一处 `data-mc` 属性与 jump-to-live-end 阈值调整，`chat:done` 的广播路径也无相关变更 → 结论 insufficient（不排除环境、配置或 daemon 版本差异）。
- 「服务端 0.10.0 / 桌面端 0.10.0 / 内网 IP」：取自报告环境表，未独立验证。

> 因此本票**不声称**已定位根因。交付的是「即使事件丢失也不再卡死、并且用户能看见实时连接异常」这一层。

## 方案

新增一个聊天实时看门狗（客户端）：

- `packages/views/chat/lib/realtime-watchdog.ts`：纯逻辑常量与判定（`CHAT_WATCHDOG_POLL_MS`、`shouldWatchChat`）。
- `packages/views/chat/components/use-chat-realtime-watchdog.ts`：hook。
  - 触发条件：`useWSStatus()` 为 `reconnecting`（存在 WS 客户端但没连上；`offline` 表示未登录/未选工作区，不算异常）且当前有打开的会话。
  - 行为：进入异常时立即对账一次，之后按固定间隔（10s）invalidate `chatKeys.pendingTask(sessionId)`、`chatKeys.messagesPage(sessionId)`、`chatKeys.taskMessages(taskId)`（有 task id 时）以及聚合 `pendingTasks(wsId)`；恢复正常或卸载即停止。
  - 对账走既有 query key，复用服务端权威接口，不做乐观写。
- `packages/views/chat/components/realtime-degraded-banner.tsx`：在聊天输入区上方显示「实时连接不可用，内容可能延迟，正在重连」；连接恢复即消失。
- `chat-window.tsx` 接入 banner 与 hook；`chat.json` 8 个语言包补 key（parity 测试要求）。

## 测试计划

`packages/views/chat/components/use-chat-realtime-watchdog.test.tsx`（QueryClient + renderHook，假定时器）：

- `connected`：不立即对账、不起定时器；
- `reconnecting`：立即对账一次，之后按间隔重复，恢复 `connected` 后停止；
- `offline`（未登录/未选工作区）：不做任何对账；
- 无打开会话：不做任何对账；
- 有 task id 时对账包含 `taskMessages`；
- 卸载后定时器清空。

`packages/views/chat/components/realtime-degraded-banner.test.tsx`：异常显示、正常不显示、文案走 i18n。

## 不做的事（取舍记录）

- 不去猜根因（daemon 没产出流 / 服务端没投递 / 客户端丢帧）：三者需要报告人的日志与任务 ID 才能区分；改错层的风险大于收益。
- 不改服务端广播与重连语义：现有「断开→重连→全量 invalidate」是对的，本次只补「没检测到断开」的盲区。
- 不引入 WebSocket 心跳超时判死：`ws-client` 已有 ping/pong 与服务端写超时，另加超时阈值属于协议级改动，超出本票。
