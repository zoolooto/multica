# NEPT-2 分诊结论：可实施

上游 Issue：https://github.com/multica-ai/multica/issues/10807
标题：Codex can silently truncate daemon-generated AGENTS.md before its delivery rules

## 结论

问题成立，可 AFK 修复。范围限定为「启动前检测并显式告警」，不改写用户配置，不改 Codex 默认上限，不调整 brief 段落顺序。

## 证据（源码 + 本次运行现场）

1. 注入路径无预算检查：`server/internal/daemon/execenv/runtime_config.go` 的 `InjectRuntimeConfig` / `writeRuntimeConfigFile` 只保证不覆盖用户内容、可重复注入、可回滚，没有任何字节预算比较。
2. 没有 Codex 指令上限相关代码：在 `server/` 全量搜索 `project_doc_max_bytes` 无命中，`server/pkg/agent/codex.go` 的 `buildCodexArgs` / `NormalizeCodexLaunchArgs` 也不会补默认上限。
3. 现场复现（本次 daemon 运行）：本任务工作目录的 `AGENTS.md` 为 51,560 字节；按 Codex 文档默认 `project_doc_max_bytes = 32768` 计，截断边界落在 `## Available Commands` 段中部（偏移 28,124–34,475），其后的 `## Output`（偏移 49,884）等交付指引全部不会被加载。这正是报告描述的失效形态。
4. 上下文回执 `server/internal/daemon/context_receipt.go` 只记录字节数与指纹，加载状态用运行时版本探针判定，不检查有效预算，因此现有诊断也不能暴露该问题。

外部主张核对：
- 「Codex 默认 32 KiB 项目指令预算」：依据 OpenAI 官方文档与报告中的离线复现（50,000 字节文件在默认配置下只加载 32,768 字节，尾部 sentinel 缺失）。本机无 Codex 可执行文件，未复跑该脚本 → 结论取信文档 + 报告数据，不视为已验证本机行为。
- 「Multica 不检查预算」：源码检查 confirmed。
- 「超出预算时静默截断」：由 32 KiB 默认值 + 无检查 + 现场 51,560 字节文件共同支撑，属高置信推断。

## 方案

在 `server/internal/daemon/execenv` 新增 Codex 项目指令预算检查（纯读，无副作用）：

- 有效上限解析顺序：启动参数中的 `-c/--config project_doc_max_bytes=N`（后者生效）> 每任务 `$CODEX_HOME/config.toml` 的 `project_doc_max_bytes` > 默认 32768。
- 计入来源：`$CODEX_HOME/AGENTS.md`（全局）、仓库根到工作目录逐级的 `AGENTS.md`（含注入后的工作目录文件，用户既有内容在内），按 Codex 的合并且工作目录文件在最后的顺序累计字节。
- 超限时产出结构化结果：有效上限、累计字节、各来源字节、以及按字节偏移计算出的「未被完整加载」的 brief 段落名（只报段名与字节，不报正文）。

daemon 在 `InjectRuntimeConfig` 成功后、启动 Codex 前调用该检查；超限时写运行记录 notice（用户可见）并落一条 `warn` 日志，说明有效上限、实际字节、受影响段落，以及提高上限或精简指令两条出路。不阻断启动（报告只要求「明确告警或拒绝」，告警风险更低且不改变现有行为）。

不做的事（记录取舍）：
- 不默认注入 `-c project_doc_max_bytes`：会静默改变用户上下文预算，与上游「尊重显式覆盖」的建议冲突。
- 不重排 brief 段落：属于产品级顺序变更，影响 prompt 缓存与大量既有测试，且在无检测时仍无法兜底；本票先补齐检测。

## 测试计划

`server/internal/daemon/execenv/codex_instruction_budget_test.go`：

- 低于 / 等于 / 超过上限三档边界；
- 启动参数 `-c project_doc_max_bytes=...` 覆盖默认；
- `$CODEX_HOME/config.toml` 覆盖默认，启动参数再覆盖 config.toml；
- 仓库既有 `AGENTS.md` 前缀字节计入；
- 祖先目录 `AGENTS.md` 计入，仓库根之上不计入；
- 多字节 UTF-8 按字节计；
- 报告不含指令正文，只含来源标签、字节数与段名；
- 重复注入后结果稳定（不因二次注入膨胀）。
