# docs

本目录存放 `sous` 的长期架构、决策、接口契约与使用指南。

- **项目简介、核心能力与快速开始**：请直接阅读仓库根目录的 [**README.md**](../README.md)。
- **仓库与代理工作规范**：见根目录 [**AGENTS.md**](../AGENTS.md) 与 [**apps/README.md**](../apps/README.md)。

## 文档索引

- **使用与运维指南（[guides/README.md](guides/README.md)）**
  - [部署 Hub](guides/deploy-hub.md)
  - [接入一台电脑或服务器](guides/connect-machine.md)
  - [macOS 签名与隐私权限](guides/macos-signing-and-permissions.md)
- **系统架构（[architecture/README.md](architecture/README.md)）**
  - [工作台：项目、预设、任务和机器](architecture/workbench.md)
  - [主管的上下文](architecture/supervisor-context.md)
- **架构决策记录（[decisions/README.md](decisions/README.md)）**
  - ADR `0001`–`0026`（常驻架构、Hub/Node 边界、ACP 子 agent、权限模式、自动审批分层、长上下文、MCP、预设与主管闭环等）
- **应用间接口契约（[api/README.md](api/README.md)）**
  - [core-hub-http.md](api/core-hub-http.md)
  - [core-hub-node.md](api/core-hub-node.md)
  - [core-daemon-ipc.md](api/core-daemon-ipc.md)

进行中或已完成的设计规格位于 `work/specs/`（如 `work/specs/sous-mvp/`、`work/specs/m5-workbench/`）；Super-agent 参考对照见 `work/agent/rules/super-agent-reference.md`。
