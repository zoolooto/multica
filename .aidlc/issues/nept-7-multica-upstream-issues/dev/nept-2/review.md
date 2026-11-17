# NEPT-2 评审记录

固定点：`origin/feature/nept-7-multica-upstream-issues..HEAD`
需求来源：本票描述（GitHub Issue #10807）+ `multica/.aidlc/issues/nept-7-multica-upstream-issues/dev/nept-2/brief.md`

## 第 1 轮

评审方式：降级自审——本运行环境没有可在本轮前台取回结果的 Agent 工具，未使用 subagent 并行评审，改为按规范轴、需求轴顺序各看一遍 diff 与对应输入。

### 规范轴：通过

- 变更只包含一个新模块 `server/internal/daemon/execenv/codex_instruction_budget.go`、对应测试，以及 `daemon.go` 两处接线；没有顺手改动无关代码。
- 诊断是纯读操作：不写文件、不改注入/清理语义、不新增配置写路径、不引入兼容 shim 或双写。
- 注释为英文且只解释非显然的原因（为什么 brief 在末尾、为什么按加载顺序累计、为什么尊重大小写覆盖）；代码标识符与既有 `execenv` 风格一致。
- 测试与实现同目录，覆盖：低于/等于/超过上限三档边界、`-c` 与 `--config` 两种写法与后值优先、`config.toml` 覆盖及启动参数再覆盖、仓库既有前缀字节、仓库根到工作目录的祖先链（含仓库根之上不计入）、全局 `$CODEX_HOME/AGENTS.md`、多字节 UTF-8 按字节计、重复注入后测量值稳定、notice 不含指令正文。
- `gofmt` 与 `go vet ./internal/daemon/execenv/ ./internal/daemon/` 通过；`go build ./internal/daemon/` 通过。
- 自审发现并已修：一条测试曾断言「生成后的文件不超过默认预算」，brief 一旦增长就会误报；改为显式 1 MiB 上限只断言字节计数。另清理了一处冗余循环写法与未使用字段。

### 需求轴：通过（含范围说明）

- 「启动前明确告知」：daemon 在 `InjectRuntimeConfig` 成功后、启动 Codex 之前测量并记录，超限时写运行记录 notice（用户可见）并落 warn 日志，失败或超限都不阻断启动。
- 「给出有效上限与受影响段落」：notice 含有效上限及其来源、累计字节、超出字节、各 `AGENTS.md` 来源字节，以及按字节偏移算出的、不会被完整加载的 brief 段名；不含任何指令正文。
- 「尊重显式配置」：上限解析顺序为启动参数 > 每任务 `config.toml` > 文档默认 32768，与 Codex 自身优先级一致；不会把用户已配置过的情形误报为超限。
- 范围说明（不视为需改）：报告建议的第 2、3 条（把交付指引放到大段可配置内容之前、给 `project_doc_max_bytes` 加运行时默认值）本票未实施。二者分别改动 brief 全局顺序与用户上下文预算，属于产品级决策，分诊时已约定本票只做检测；因此「交付指引一定在预算内」仍是后续事项，本票承诺的是「不再静默」。

结论：pass。
