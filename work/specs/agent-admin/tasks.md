# 远程管理机器上的 agent：任务

## A. 基础设施
- [x] A1 proto：`node.request` / `node.response` / `node.stream` / `node.job_ended`，`hello.caps`
- [x] A2 nodehub：pending 请求表、超时、断线处理、`unsupported`；`node.stream` 转 `/ws`，`node.job_ended` 存事件
- [x] A3 daemon：请求分发、`remote_admin` 开关、作业管理（伪终端、分块、超时、输入、取消）
- [x] A4 node.json：`agent_options`、`agent_env`、`remote_admin` 字段；读写屏蔽；热加载

## B. 检测与安装
- [x] B1 agents：`Recipe`、`Install` / `Upgrade` / `Login` / `Logout` / `Requires` / `Configs` / `KeyEnv`，`AugmentPath`
- [x] B2 `agents.detect`、`agents.install`（结束后重新检测）
- [ ] B3 在 macOS 上实测五个 agent 的安装命令（Kiro CLI 已实测；Claude、Codex、Cursor、OpenCode 未测）

## C. 登录
- [x] C1 `agents.login` / `agents.logout` / `job.input` / `job.resize`
- [ ] C2 实测每个 agent 的登录：Claude、OpenCode、Kiro CLI 已实测；Cursor、Codex 未测
- [x] C3 `agents.set_env`（API Key），`AgentInfo` 报告已设置的变量名

## D. 模型与思考强度
- [x] D1 acp：解析 `configOptions` 和旧的 `models`；`SetConfigOption`；假 agent 的 `FAKE_ACP_OPTIONS`
- [x] D2 `agents.probe`，`AgentInfo.options`
- [x] D3 Run 开始时套用 `agent_options`
- [ ] D4 实测选项：Claude、Cursor、OpenCode、Kiro CLI 已读到，Claude 的真实 Run 套用了默认值；Codex 未测
- [x] D5 Kiro 的 `autopilot` 按权限模式由 sous 设置（`AskOptions` / `OpenOptions`）

## E. 配置文件
- [x] E1 `config.read` / `config.write`：白名单、sha256 冲突、JSON / JSONC / TOML 校验、备份
- [ ] E2 实测各 agent 配置文件的路径（macOS 上都已确认，含 Kiro 的 `cli.json`、`permissions.yaml`；Windows 路径未确认）

## F. Hub HTTP
- [x] F1 §7 的接口和 `/ws` 事件，审计事件

## G. 页面
- [x] G1 `Machines.svelte` 显示全部 agent 和操作入口
- [x] G2 `AgentPanel.svelte`、`JobLog.svelte`、`ConfigEditor.svelte`
- [x] G3 `jobs.ts`：作业输出累积、链接 / 验证码识别，配测试

## H. 收尾
- [x] H1 测试全部通过：`wok test core`、`wok test web`、`wok run web check`
- [x] H2 更新契约、AGENTS.md，加决策记录 0012
