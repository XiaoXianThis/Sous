# decisions

Architecture decision records: what was decided, why, and which options were rejected.

## Rules

- File names are `NNNN-slug.md`, numbered from `0001`, such as `0001-use-postgres.md`.
- Start from a copy of `0000-template.md`.
- Never delete a record or rewrite its decision. When a decision is reversed, add a new record and set the old one's status to "Superseded by NNNN".
- No subdirectories.

## Records

| record | title | status |
|---|---|---|
| [0001](0001-super-agent-architecture.md) | 参考 Super-agent 的常驻架构，用 Go 自研 | Accepted |
| [0002](0002-hub-thinks-node-acts.md) | Hub 只动脑，Node 才动手 | Accepted |
| [0003](0003-apps-split.md) | 拆成 core、web、desktop 三个 app | Accepted |
| [0004](0004-acp-for-sub-agents.md) | 子 agent 统一走 ACP | Accepted |
| [0005](0005-permission-modes.md) | 三档权限模式，加一条底线 | Accepted |
| [0006](0006-inspector-instead-of-budget.md) | 不设花费上限，由巡检 agent 监督 | Accepted |
| [0007](0007-storage-and-models.md) | 存储用 SQLite，模型多协议、默认 DeepSeek | Accepted |
| [0008](0008-manual-update-unsigned.md) | 手动更新，不做代码签名 | Accepted |
| [0009](0009-deliver-by-pull-request.md) | 代码任务以 PR 交付，sous 永不合并主分支 | Superseded by 0022 |
| [0010](0010-policy-is-a-safety-net.md) | 权限策略是安全网，执行隔离放到第二期 | Accepted |
| [0011](0011-supervisor-owns-tasks.md) | 第一期由主管自己维护任务，不单独设置 extractor | Accepted |
| [0012](0012-remote-agent-admin.md) | 从页面远程管理机器上的 agent，只跑内置的命令和文件 | Accepted |
| [0013](0013-personas.md) | 人员就是岗位档案，不是有记忆的拟人员工 | Accepted |
| [0014](0014-server-role-and-ops-tasks.md) | 服务器作为机器接入，部署和维护走运维任务 | Accepted（部署触发被 0022 取代） |
| [0015](0015-project-shells-and-hardening.md) | 页面上的项目终端，以及配套的安全收紧 | Accepted |
| [0016](0016-models-configured-on-the-page.md) | 模型在页面上配置，存在 Hub 的数据库里 | Accepted |
| [0017](0017-web-search-and-fetch.md) | 主管可以联网：web_search 和 web_fetch | Accepted |
| [0018](0018-phone-liquid-glass.md) | 手机端按原生 App 做：WebView 行为 + Liquid Glass 外观 | Accepted |
| [0019](0019-auto-approval-layers.md) | 自动审批分层：确定性规则、审批 agent、硬上限 | Accepted |
| [0020](0020-long-thread-context.md) | 长对话的上下文：分段、结构化摘要、查回原文 | Accepted |
| [0021](0021-mcp-servers.md) | 主管支持 MCP：页面上管理，agent 只能提议、用户批准 | Accepted |
| [0022](0022-flexible-not-preset.md) | 灵活优先：去掉固定流程，交给主管判断 | Accepted |
