# core-hub-http：Hub ↔ 客户端（PWA、desktop）

- Provider: core（`sous-hub`）
- Consumers: web、desktop
- Status: 草案，随 sous-mvp 评审定稿。定稿后补充 OpenAPI 描述，以描述文件为准。

## 约定

- 基础地址：`https://agent.wxian.cc`，所有接口都在 `/api/v1` 下面。
- 认证：Passkey 登录后下发 HttpOnly 会话 Cookie；会改动数据的请求要带 `X-CSRF-Token` 头。
- 数据格式：JSON；时间用 Unix 毫秒；ID 用 ULID 字符串。
- 错误格式：`{"error": {"code": "not_found", "message": "..."}}`。

## REST

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/auth/passkey/register/begin` `/finish` | 注册 Passkey（首次使用一次性链接；之后要求已登录） |
| POST | `/auth/passkey/login/begin` `/finish` | 登录 |
| POST | `/auth/logout` | 退出 |
| POST | `/auth/dev-login` | 仅当 `dev: true` 且请求直接来自本机回环地址时可用，否则 404 |
| GET | `/me` | `{logged_in, has_passkey, dev_login}`，不需要登录 |
| GET/POST | `/threads` | 列出、创建线程 |
| GET/PATCH | `/threads/{id}` | 读取、改名或归档 |
| GET | `/threads/{id}/messages?before=&limit=` | 分页读取历史消息（按 seq 从旧到新）；返回 `{messages, last_seq}`，`last_seq` 是全局最新的事件序号，客户端用它作为 `/ws?since=` 的起点 |
| POST | `/threads/{id}/messages` | 发送消息 `{"content": "..."}`，返回 202 `{message_id, seq}`；回复通过 `/ws` 推送 |
| GET | `/tasks?status=a,b&project_id=&thread_id=&parent_id=` | 列出任务（优先级高的在前，同级按创建时间） |
| POST | `/tasks` | 创建 `{title, detail?, kind?, project_id?, priority?, agent_pref?, node_pref?, permission_mode?, parent_id?, depends_on?, thread_id?, dispatch?}`，返回 201。`kind` 缺省时有项目就是 `code`；用户创建且不派发的进 `inbox` |
| GET | `/tasks/{id}` | `{task, runs, subtasks}` |
| PATCH | `/tasks/{id}` | 修改字段（只改出现的字段），`depends_on` 整体替换（成环时 400）；`dismissed: true` 表示用户看过了（从首页移除）；`status` 只能手动改成 `inbox`/`planned`/`blocked`/`done`/`cancelled`，执行中的任务要先取消（409） |
| POST | `/tasks/{id}/dispatch` | 派发或重试 `{"node_id"?, "agent"?}`（会记到任务上）。有空闲机器就开始，否则排队（`queued`）；已交付 PR 的任务返回 409 |
| POST | `/tasks/{id}/cancel` | 取消任务：停止它的 Run，并取消未完成的子任务 |
| GET | `/inbox` | 首页“需要你处理”：`{approvals, tasks}`，待审批的操作，以及受阻、失败、等机器、待评审 PR 且没被 `dismissed` 的任务 |
| GET | `/runs?thread_id=&node_id=&status=a,b&limit=` | 列出 Run（按创建时间从旧到新） |
| POST | `/runs` | 派发 `{thread_id?, node_id?, agent, prompt, repo_path?}`；不填 `node_id` 时选第一台在线、装了该 agent 且没有显示未登录的机器。没有合适机器时返回 409 |
| GET | `/runs/{id}` | `{run, events, approvals}`：Run、它存储下来的全部事件、它的审批 |
| GET | `/runs/{id}/log` | 纯文本运行日志（agent 输出、工具调用、计划） |
| GET | `/runs/{id}/diff` | 纯文本 diff（相对基线提交） |
| POST | `/runs/{id}/message` | 追加指令 `{text}`，只能发给正在运行的 Run |
| POST | `/runs/{id}/cancel` | 取消：排队中的直接结束；运行中的通知机器停下；机器离线时直接标记为已取消 |
| GET | `/approvals?status=pending&run_id=` | 审批列表。`review` 为 `ai` 的正在由审批 agent 判断；`escalation` 是它转给用户时的理由 |
| POST | `/approvals/{id}/decide` | `{option_id, note?}`；`option_id` 必须是该审批提供的选项之一，为空表示取消。已处理或已过期时返回 409 |
| GET | `/nodes` | 机器列表（`online` 是实时状态）；`agents` 是机器上报的 AgentInfo 数组 |
| POST | `/nodes/pairing-code` | 生成配对码 `{code, expires_at}` |
| PATCH | `/nodes/{id}` | 改名 `{name}` |
| DELETE | `/nodes/{id}` | 删除机器：作废令牌并断开连接 |
| GET/POST | `/projects` | 列出、创建项目 `{name, repo_url?, default_agent?, default_mode?, nodes: [{node_id, path}]}`；`nodes` 的顺序就是路由优先级（服务端按顺序重写 `priority`）。重名返回 409 |
| PATCH/DELETE | `/projects/{id}` | 整体替换项目；还有未关闭任务的项目不能删除（409） |
| GET | `/memories?q=&kind=&limit=` | 有 `q` 时按相关度搜索，否则按更新时间列出（可按 `kind` 过滤）；返回 `{memories, kinds}` |
| POST | `/memories` | 新建 `{kind, content}`，返回 201 和记忆 |
| PUT/DELETE | `/memories/{id}` | 修改 `{kind, content}` / 删除 |
| GET/POST/DELETE | `/schedules` `/schedules/{id}` | 定时任务 |
| GET | `/usage?days=` | 最近 N 天（默认 30）的 token 用量，按 provider、model、role 汇总 |
| GET/PATCH | `/settings` | `{default_mode, default_agent, notify: {kind: bool}, quiet: {enabled, start, end, tz}, roles, push_devices}`；`roles` 只读，不返回任何密钥。PATCH 只改出现的字段 |
| POST/DELETE | `/push/subscriptions` | 浏览器的 `PushSubscription.toJSON()`（`{endpoint, keys: {p256dh, auth}}`）；删除时 `{endpoint}` |
| GET | `/push/vapid-public-key` | `{key}`：base64url 的 VAPID 公钥（`applicationServerKey`） |
| POST | `/push/test` | 给所有已订阅的设备发一条测试通知，返回 `{devices, sent, errors}` |
| GET | `/updates` | 各平台最新版本信息（来自 GitHub Releases 缓存） |

另有 `GET /healthz`（不在 `/api/v1` 下，不需要登录）返回 `{status, version}`。机器用的 `POST /api/v1/node/pair` 和 `GET /node` 见 `core-hub-node.md`。

上表里列了但 `internal/` 里还没有实现的接口（定时任务、更新）属于 M4，实现时再定稿。

**消息字段**：`{id, thread_id, role: "user"|"assistant", content, error?, reply_to?, kind?, seq, created_at}`。`reply_to` 是一条回复所回答的最新用户消息的 seq；回复期间新到的消息，会在下一轮一起回答。`kind` 为空表示普通消息，`notice` 表示主管主动发出的提醒（总在主线），`silent` 表示主管判断这条消息不需要回复（`content` 是原因）。

**Run 和审批**：`/ws` 会推送 `run.queued`、`run.started`、`run.update`、`run.ended`、`approval.requested`、`approval.decided`，以及临时事件 `run.output`（seq 为 0，payload `{kind: "text"|"thought", text}`，不补发；完整文字在 `/runs/{id}/log` 里）。机器变化时推送 `node.*` 事件。

**任务**：`{id, parent_id, origin_thread_id, kind: "code"|"general", title, detail, status, status_reason, priority, project_id, agent_pref, node_pref, permission_mode, branch, base_branch, pr_url, review_note, next, dismissed, depends_on, created_at, updated_at}`。`status` 取值 `inbox planned queued running waiting_approval blocked review done failed cancelled`；`queued` 表示等依赖或等机器（原因在 `status_reason`）。`/ws` 推送 `task.created`、`task.updated`、`task.status`、`task.reviewed`、`project.put`、`project.deleted`、`approval.escalated`、`notify.sent`。任务的结果由引擎以 `kind: "report"` 的助手消息发到发起它的线程。

**Run**（M3 起）：多了 `task_id, purpose: "work"|"rebase"|"deliver", permission_mode, base_branch, pr_url, conflict, title, force`。`run.update` 的 `kind` 为 `permission` 时，表示机器上的策略自己做的决定（`category`、`decision: allow|deny`）。

**推送**：通知点开后打开 PWA 的哈希路由：`/#approvals`、`/#task=<id>`、`/#thread=<id>`。

**记忆**：`{id, kind: "profile"|"project"|"lesson"|"note", content, created_at, updated_at}`。记忆变化时，`/ws` 推送 `memory.put` / `memory.deleted` 事件。

## WebSocket `/ws`

- 连接地址：`/ws?since=<seq>`。服务端先补发 `seq > since` 的事件，然后实时推送新事件。
- 服务端发给客户端的帧：`{"seq": 123, "id": "...", "ts": 0, "type": "task.status", "thread_id"?, "task_id"?, "run_id"?, "payload": {...}}`。事件类型见 `work/specs/sous-mvp/design.md` 第 4 节。
- `seq` 为 0 的是临时事件（目前只有 `message.assistant.delta`，payload `{message_id, delta, thinking?}`），只实时推送，不补发。回复结束后会推送同一个 `message_id` 的 `message.assistant`。
- 写操作一律走 REST；客户端不需要发任何帧。服务端用 WebSocket 协议层的 ping 保活（每 30 秒）。
- 客户端跟不上推送时，服务端会以 1013（Try Again Later）关闭连接；客户端带上最后收到的 seq 重连即可补齐。
