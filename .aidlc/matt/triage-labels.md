# Triage roles

分诊状态写在 Issue metadata 的 `mt_triage_role`：

- `needs-triage`：待核验。
- `ready-for-agent`：核验后可由 Agent 实施。
- `ready-for-human`：需要人处理或补充信息，转为 blocked。
- `wontfix`：确认已实现或决定不实施。

分类可用 `mt_triage_category`，取 `bug` 或 `enhancement`。按真实代码和证据判定，不把 GitHub 原始描述自动当作已验证的规格。
