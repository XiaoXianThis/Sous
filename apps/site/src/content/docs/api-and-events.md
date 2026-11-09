---
title: "HTTP 接口、事件类型与 Agent 模型矩阵"
description: "整理自 docs/api 的 REST API 方法与路径、WebSocket 事件流类型、Daemon IPC 方法及内置 Agent 与模型矩阵。"
group: "reference"
order: 2
sourceDoc: "docs/api/core-hub-http.md"
updatedAt: "2026-11-09"
---

## 为什么需要这份参考

当你要调试 Web 工作台、桌面端（`apps/desktop`）、终端对话（`sous chat`），或者查阅 Sous 支持哪些编码 Agent 与模型协议时，可以在本页查阅整理自 `docs/api/core-hub-http.md`、`docs/api/core-hub-node.md`、`docs/api/core-daemon-ipc.md` 与 `apps/core` 源码的完整契约。

---

## HTTP 接口约定

- **基础路径**：除 `GET /healthz`、`GET /node`（节点 WebSocket）外，所有客户端 REST 与 WebSocket 接口均位于 `/api/v1` 下。
- **会话与 CSRF**：浏览器通过 Passkey（WebAuthn，`userVerification: required`）登录后获得 `HttpOnly` 会话 Cookie；所有修改数据的请求需携带 `X-CSRF-Token` 请求头。
- **近期登录复验（`reauth`）**：打开网页终端、已登录状态下新增 Passkey、修改模型供应商地址或 API Key、配置外部 MCP 服务等敏感操作，要求当前会话在 **15 分钟**内完成过 Passkey 验证，否则返回 `401 {"error": {"code": "reauth", ...}}`。

---

## REST API 方法与路径总表

以下路径均相对于 `/api/v1`（`GET /healthz` 与 `GET /node` 除外）：

### 认证与全局状态

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/healthz`（根路径） | 无需登录，返回 `{status, version, commit, web?: {commit, dirty?, built_at}}` |
| `GET` | `/me` | 无需登录，返回 `{logged_in, has_passkey, dev_login}` |
| `POST` | `/auth/passkey/register/begin`、`/finish` | 注册 Passkey（首次通过一次性链接注册；后续需已登录且近期复验） |
| `POST` | `/auth/passkey/login/begin`、`/finish` | Passkey 登录（已登录时再次调用可刷新 15 分钟近期验证窗口） |
| `POST` | `/auth/dev-login` | 仅当 `hub.yaml` 开启 `dev: true` 且从本机回环地址访问时可用 |
| `POST` | `/auth/logout` | 退出当前登录会话 |
| `GET` | `/overview` | 工作台首屏总览聚合数据：`{projects, nodes, personas, tasks, runs, approvals, discovered}` |
| `GET` | `/inbox` | 首页「需要你处理」列表：`{approvals, tasks}` |

### 线程、消息、上下文与附件

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` / `POST` | `/threads` | 列出或创建对话线程（传 `project_id` 时创建或筛选项目专属对话） |
| `GET` / `PATCH` | `/threads/{id}` | 读取、重命名或归档指定线程 |
| `GET` | `/threads/{id}/messages?before=&limit=` | 分页读取历史消息，返回 `{messages, last_seq}` |
| `POST` | `/threads/{id}/messages` | 发送用户消息 `{content, context?: {project_id?, task_id?, mentions?}, attachments?}`，返回 `202 {message_id, seq}` |
| `GET` | `/threads/{id}/context` | 查看线程当前上下文分段摘要、Token 占用估算与 Prompt Cache 命中统计 |
| `POST` | `/threads/{id}/compact` | 立即将较早的消息整理进结构化摘要（完成后推送 `thread.compacted`） |
| `PUT` | `/threads/{id}/summary` | 手动改写当前线程的结构化摘要 |
| `POST` | `/attachments?name=<文件名>` | 上传原始二进制文件（最大 20 MB），按内容识别图片/文本/PDF，返回 `201 {id, name, mime, size}` |
| `GET` | `/attachments/{id}` | 获取附件（图片 `inline` 展示，其余強制 `attachment` 下载） |

### 任务、Run 与审批

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` / `POST` | `/tasks` | 按条件列出任务，或创建新任务（指定 `place`、`base_branch`、`review`、`persona_id` 等） |
| `GET` / `PATCH` | `/tasks/{id}` | 获取 `{task, runs, subtasks}` 详情，或修改任务字段、依赖关系与状态 |
| `POST` | `/tasks/{id}/dispatch` | 派发或重试任务 `{"node_id"?, "agent"?}` |
| `POST` | `/tasks/{id}/verdict` | 用户对等待人工验收（`review: "user"`）的任务给出结论 `{pass, reason?}` |
| `POST` | `/tasks/{id}/review` | 让已完成任务重新执行一次验收 `{by: "model"\|"agent"\|"user"}` |
| `POST` | `/tasks/{id}/cancel` | 取消任务，停止其正在运行的 Run 及未完成子任务 |
| `GET` / `POST` | `/runs` | 列出 Run，或直接向指定节点派发单次 Run |
| `GET` | `/runs/{id}` | 获取 `{run, events, approvals}` 完整记录 |
| `GET` | `/runs/{id}/log`、`/runs/{id}/diff` | 获取纯文本运行日志或相对于基线提交的纯文本 Diff |
| `POST` | `/runs/{id}/message` | 向正在运行或暂停的 Run 追加指令 `{text}` |
| `POST` | `/runs/{id}/pause`、`/resume`、`/cancel` | 暂停、恢复或取消指定 Run |
| `GET` | `/approvals?status=pending&run_id=` | 列出待审批或历史审批项（含 `facts`、`evidence` 与 AI 升级理由 `escalation`） |
| `POST` | `/approvals/{id}/decide` | 裁决审批项 `{option_id, note?}` |

### 机器节点、远程管理与版本更新

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/nodes` | 列出所有已配对机器、实时在线状态及上报的 `agents` 信息 |
| `POST` | `/nodes/pairing-code` | 生成 6 位一次性配对码 `{code, expires_at}` |
| `POST` | `/node/pair` | 节点调用此接口完成配对，换取 `{node_id, token}` |
| `GET` | `/node`（根路径） | 节点守护进程长连接 WebSocket 入口（`Authorization: Bearer <node_token>`） |
| `PATCH` / `DELETE` | `/nodes/{id}` | 修改机器名称，或删除机器并吊销其令牌 |
| `PUT` | `/nodes/{id}/role` | 将开发机改为服务器（`{"role": "server"}`；改回 `"dev"` 仅限本机 `node.json`） |
| `PUT` | `/nodes/{id}/workspaces` | 替换机器的工作空间目录列表并立即扫描 |
| `POST` | `/nodes/{id}/scan` | 立即重新扫描机器工作空间中的 Git 仓库 |
| `POST` | `/nodes/{id}/agents/detect` | 立即重新检测机器上的编码 Agent 状态 |
| `POST` | `/nodes/{id}/agents/{agent}/probe` | 短暂启动 Agent 会话以探测其支持的 `options`（模型、思考强度等） |
| `POST` | `/nodes/{id}/agents/{agent}/install`、`/login`、`/logout` | 在节点伪终端中执行内置的 Agent 安装/升级、登录或登出命令 |
| `PUT` | `/nodes/{id}/agents/{agent}/defaults`、`/env` | 设置该机器上指定 Agent 的默认会话选项或 API Key 环境变量 |
| `GET` / `PUT` | `/nodes/{id}/files/{agent}/{file}` | 读取或基于 `base_sha256` 乐观锁更新 Agent 配置文件或 `sous/node`（自动备份 `.sous-bak`） |
| `GET` / `POST` | `/nodes/{id}/jobs/{job}`、`/input`、`/resize`、`/cancel` | 读取远程终端/安装作业输出，或写入输入、调整窗口尺寸、终止作业 |
| `GET` | `/updates?refresh=1` | 检查 GitHub Releases 最新版本以及 Hub 与各节点的版本对比 |
| `POST` | `/nodes/{id}/update` | 让指定在线节点自动下载安装新版本并重启 |

### 项目、仓库、终端、预设、模型、MCP、记忆与定时

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` / `POST` | `/projects` | 列出或创建项目（绑定 `repo_url`、节点路径优先级及服务器 `deploy` 目标） |
| `PATCH` / `DELETE` | `/projects/{id}` | 修改或删除项目 |
| `POST` | `/projects/{id}/archive`、`/activate` | 归档或重新激活项目 |
| `POST` | `/projects/new`、`/projects/{id}/clone` | 在开发机工作空间中初始化新仓库或克隆已有项目 |
| `POST` | `/projects/{id}/deploy/check` | 立即触发项目所在机器 `git fetch` 并检查部署分支是否有新提交 |
| `POST` | `/projects/{id}/locations/{node}/sync` | 将指定机器上的仓库 checkout 与上游快进对齐（`dirty` 或分叉时不改动） |
| `POST` | `/projects/{id}/locations/{node}/wip` | 将指定机器上未提交和未推送的改动打包推送到 `origin` 的 `sous/wip/<机器>-<时间>` 分支，不改动本地工作区 |
| `GET` / `POST` | `/projects/{id}/shells` | 列出或在指定机器的项目目录下打开交互式网页终端（需 `reauth`） |
| `GET` / `PUT` | `/projects/{id}/tree`、`/projects/{id}/file` | 在线浏览项目目录树、读取文件（含图片预览）或保存文件修改 |
| `GET` | `/repos` | 列出所有机器在工作空间中扫描到的 Git 仓库 |
| `GET` / `POST` / `PATCH` / `DELETE` | `/personas`、`/personas/{id}` | 列出、创建、修改或删除预设（岗位） |
| `GET` / `PUT` | `/models`、`/models/roles` | 获取模型配置，或为 5 个内部角色分配 `provider_id`、`model`、`effort`、`context_window` |
| `POST` / `PATCH` / `DELETE` | `/models/providers`、`/models/providers/{id}` | 添加、修改或删除模型供应商（需 `reauth`），支持 `/discover` 探测模型列表与 `/models/test` 连通性测试 |
| `GET` / `POST` / `PATCH` / `DELETE` | `/mcp`、`/mcp/servers`、`/mcp/servers/{id}` | 管理主管连接的外部 MCP 服务（支持 `/approve` 批准主管提议的服务与 `/reconnect` 重连） |
| `GET` / `POST` / `PUT` / `DELETE` | `/memories`、`/memories/{id}` | 搜索、创建、修改或删除长期记忆（`profile` / `project` / `lesson` / `note`） |
| `GET` / `POST` / `PATCH` / `DELETE` | `/schedules`、`/schedules/{id}`、`/schedules/{id}/run` | 管理 Cron 定时任务或立即手动触发一次 |
| `GET` | `/findings`、`/usage` | 查看巡检发现的问题列表，或按天/按项目/按角色统计 Token 用量 |
| `GET` / `PATCH` | `/settings` | 读取或更新全局默认权限模式、默认 Agent、通知开关与免打扰时段 |
| `GET` / `POST` / `DELETE` | `/push/vapid-public-key`、`/push/subscriptions`、`/push/test` | Web Push 浏览器推送订阅与测试 |
| `GET` / `POST` | `/device/*` | 供已配对节点上的 `sous chat` 与桌面端通过 Bearer 设备令牌访问对话与审批接口 |

---

## WebSocket 事件类型（`/api/v1/ws?since=<seq>`）

Hub 采用事件溯源架构（源码见 `apps/core/internal/event/`）。客户端连接 `/api/v1/ws?since=<seq>` 后，服务端先补发 `seq > since` 的持久化事件，随后实时推送新事件。

### 持久化事件（`seq > 0`）

| 领域 | 事件类型 (`type`) | 说明 |
|---|---|---|
| **对话与上下文** | `thread.created`、`thread.updated`、`thread.compacted`、`message.user`、`message.assistant`、`message.tools` | 线程变更、长上下文分段摘要完成、用户与主管消息落库、主管工具调用列表更新 |
| **任务与交付** | `task.created`、`task.updated`、`task.status`、`task.reviewed`、`task.git`、`consult.closed` | 任务创建、属性更新、状态流转、验收完成、受控 Git 操作结果、只读顾问会话关闭 |
| **Run 与巡检** | `run.queued`、`run.started`、`run.update`、`run.turn`、`run.paused`、`run.resumed`、`run.message`、`run.message.delivered`、`run.ended`、`run.archived`、`inspector.finding` | Run 生命周期、ACP 内部工具/计划更新、顾问单轮回答、暂停与恢复、追加指令送达、巡检告警 |
| **权限审批** | `approval.requested`、`approval.evaluated`、`approval.checked`、`approval.model_called`、`approval.escalated`、`approval.decided` | 权限请求发起、Medium 候选评估、审批员只读取证审计、模型预算记录、转交人工与最终裁决 |
| **机器与远程作业** | `node.paired`、`node.online`、`node.offline`、`node.updated`、`node.deleted`、`node.role`、`node.repos`、`node.config_changed`、`node.update_requested`、`node.update_result`、`agent.job.started`、`agent.job.ended` | 节点上下线、仓库与 Agent 扫描结果、远程配置变更、远程升级结果、伪终端/安装作业生命周期 |
| **项目、预设与系统** | `project.put`、`project.deleted`、`project.archived`、`project.deployed`、`project.repo_op`、`persona.put`、`persona.deleted`、`memory.put`、`memory.deleted`、`schedule.put`、`schedule.deleted`、`trigger.fired`、`models.changed`、`notify.sent` | 项目与部署状态更新、多机同步/存 WIP 记录、预设与记忆变更、定时与自动跟进触发、模型设置更新 |

### 临时流式事件（`seq: 0`，仅实时推送不落库）

根据 `apps/core/internal/event/event.go` 中的 `Ephemeral()` 定义，共有 **3 类**高频流式事件以 `seq: 0` 实时广播：

1. `message.assistant.delta`：主管回复的流式文本与思考增量（`{message_id, delta, thinking?}`）。
2. `run.output`：子 Agent 执行期间的实时文本与思考输出流（`{kind: "text"|"thought", text}`）。
3. `agent.job.output`：节点上伪终端（网页终端或远程安装/登录 Agent 作业）的实时字节流（`{node_id, job_id, offset, data}`）。

---

## 本机 Daemon IPC 方法（`~/.sous/daemon.sock`）

本机 `sous` CLI 通过本地 Unix Socket（Windows 为当前用户 SID 命名管道）向 `sous daemon` 发起 JSON-RPC 调用（详见 `docs/api/core-daemon-ipc.md`）：

- `status`：获取守护进程状态、Hub 连接状态、活跃 Run 与待发队列长度。
- `update.check` / `update.apply`：检查或由守护进程执行版本升级并自重启。
- `chat.send` / `chat.stream`：供 `sous chat` 终端对话收发消息与裁决审批。

---

## 内置编码 Agent 与模型矩阵

### 节点端内置编码 Agent（`apps/core/internal/agents/agents.go`）

| 标识符 (`Name`) | 名称 (`Title`) | 探测二进制 (`Binary`) | ACP 启动命令 (`Launch`) | 登录状态检测 (`LoginArgs`) | 配置文件 (`Configs`) 与环境变量 (`KeyEnv`) |
|---|---|---|---|---|---|
| `claude` | Claude Code | `claude` | `npx -y @agentclientprotocol/claude-agent-acp@0.100.0` | `claude auth status`（需 `npx`） | `~/.claude/settings.json`；支持 `CLAUDE_CODE_OAUTH_TOKEN`、`ANTHROPIC_API_KEY` |
| `codex` | Codex | `codex` | `npx -y @zed-industries/codex-acp@2.5.0` | `codex login status` 或读取 `~/.codex/config.toml`（需 `npx`） | `~/.codex/config.toml`；支持 `OPENAI_API_KEY` |
| `cursor` | Cursor | `cursor-agent` | `cursor-agent acp` | `cursor-agent status` | `~/.cursor/cli-config.json`；支持 `CURSOR_API_KEY`，会话固定为 `agent` 模式 |
| `opencode` | OpenCode | `opencode` | `opencode acp` | 多服务商共用，不预先判定登录态 | `~/.config/opencode/opencode.json`；自动注入 `OPENCODE_CONFIG_CONTENT` 使 `edit`/`bash`/`webfetch` 走审批 |
| `kiro` | Kiro | `kiro-cli` | `kiro-cli acp` | `kiro-cli whoami` | `~/.kiro/settings/permissions.yaml`、`cli.json`、`mcp.json`；非 `open` 模式下自动设 `autopilot: off` |
| 自定义 (`custom_agents`) | 用户自定义 | `command[0]` | `command` 数组 | 不预先判定 | 在本机 `~/.sous/node.json` 的 `custom_agents` 中登记（如 Antigravity 等兼容 ACP 协议的 CLI，具体参数**需确认**） |

### Hub 端模型供应商协议（`apps/core/internal/config/config.go`）

| 协议标识 (`kind`) | 默认地址 (`base_url`) | 适用服务 |
|---|---|---|
| `anthropic-messages` | `https://api.anthropic.com` | Anthropic 官方 Claude 系列模型及兼容 Anthropic Messages API 的代理服务 |
| `openai-responses` | `https://api.openai.com` | OpenAI Responses API（GPT-5 / o3 / o4-mini 等支持推理强度 `effort` 的模型） |
| `openai-chat` | `https://api.openai.com` | 标准 OpenAI Chat Completions 兼容接口（DeepSeek、Qwen、OpenRouter、本地 Ollama / vLLM 等） |

---

## 下一步

- 了解仓库结构、编译测试与发版签名：[开发指南：仓库结构、构建测试、扩展与发布签名](/docs/development-guide)
- 深入了解系统架构与 SQLite 表结构：[系统架构：Hub/Daemon、事件溯源、隔离与存储](/docs/architecture-overview)
