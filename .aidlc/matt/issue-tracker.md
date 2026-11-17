# Issue tracker: Multica

本项目使用当前 Multica 工作区的 Issue。GitHub Issue 是需求来源；原文和导入时评论保存在各子 Issue 描述中，需结合当前代码验证。

- 读取：`multica issue get <key> --output json`
- 子票：`multica issue children <parent> --output json`
- 元数据：`multica issue metadata list <key> --output json`，写入用 `multica issue metadata set`
- 指派：`multica issue assign <key> --to "Matt Team" --no-start`
- 状态：`multica issue status <key> backlog|todo|in_progress|in_review|done|blocked|cancelled`
- 评论：`multica issue comment add <key> --content-file <path>`；队长评论首行使用 `/note`。

Backlog 不启动运行；将已分配的小队 Issue 从 Backlog 切到 Todo 会触发队长。子票的 stage 表示执行顺序；遇到缺口应按队长指令阻塞并等待人工处理。代码变更提交到工作分支并合回集成分支；集成分支进入 main 由人处理。
