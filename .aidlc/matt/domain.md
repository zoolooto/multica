# 项目文档与 AIDLC 仓库

## Resolve the aidlc-repo first

本项目只有一个代码和文档仓库：`https://github.com/zoolooto/multica`。项目描述的 **AIDLC anchors** 指向它；从任务 workdir 根调用 `multica repo checkout` 后，仓库目录为 `multica/`，因此 aidlcRepoPath 为 `multica`。不要在已有同仓 checkout 内再嵌套 checkout。

任务产物路径以 Issue 的 `mt_aidlc_path` 为准；在 Git 仓库内提交时去掉开头的 `multica/`。本项目按实际存在的代码和文档工作；缺失的术语表或 ADR 不要臆造。
