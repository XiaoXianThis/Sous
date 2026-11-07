# 任务

D1–D8 已拍板（见 `design.md` 开头）。

- [x] 0. 拍板 D1–D8
- [x] 1. P4.1 + P4.2：路由退回时带说明，派发结果和排队原因直接告诉主管；`router_test` 补用例
- [x] 2. P4.5：`task_create` 描述和系统提示的机器规则；`prompts` 的测试照常通过
- [x] 3. P1：任务完成、受阻、失败、待验收、永久排队时发 `trigger.fired`（kind `task`）；合并窗口、自动回合上限（不另计链条深度）；e2e 用例
- [x] 4. P2：工作 Run 的结果小节、Hub 解析（不加 `run.result` 字段）、Node 总结保留结尾、`task_result` 工具；报告消息用上结构化结果
- [x] 5. P4.3：`project_clone` 到设了工作空间的服务器（复用 workspaces，不加 deploy_root）；e2e 用例
- [x] 6. P4.4：审批卡片认出 ssh 的目标是已接入的机器时加提示（不拦）
- [x] 7. P5：返工、重试时恢复上一个工作 Run 的会话
- [x] 8. P3：`sous mcp` stdio 服务、`report_progress` / `ask_supervisor` / `report_result`；假 agent 的 `ask` 模式
- [ ] 8b. 真实 agent 实测 `sous mcp`（Claude Code、Codex、OpenCode、Kiro、Cursor 对 ACP `mcpServers` 的支持）
- [x] 9. 文档：`docs/api`、`docs/architecture`、决策 0025、0014 改动、`apps/core/AGENTS.md`
- [x] 10. 隔离浏览器端到端（真实 sous / sous mcp 二进制 + 假 agent 的 ask 模式：提问、在任务页回答、汇报的结果、【任务进展】）
- [ ] 10b. 真实服务器上“部署到已连接的服务器”（需要真机）
