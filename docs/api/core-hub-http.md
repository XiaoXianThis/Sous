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
| GET/POST | `/tasks` | 列出（可按状态、项目过滤）、创建任务 |
| GET/PATCH | `/tasks/{id}` | 读取、修改（标题、优先级、权限模式、指定机器或 agent） |
| POST | `/tasks/{id}/dispatch` | 派发 `{"node_id"?, "agent"?}` |
| POST | `/tasks/{id}/cancel` | 取消 |
| GET | `/runs/{id}` | Run 详情 |
| GET | `/runs/{id}/log?offset=` | 原始输出日志 |
| POST | `/runs/{id}/message` | 中途追加指令 |
| POST | `/runs/{id}/cancel` | 终止 |
| GET | `/approvals?status=pending` | 审批队列 |
| POST | `/approvals/{id}/decide` | `{"decision": "approve"\|"reject", "note"?, "patch"?}` |
| GET | `/nodes` | 机器列表 |
| POST | `/nodes/pairing-code` | 生成配对码 |
| PATCH/DELETE | `/nodes/{id}` | 改名、调整并发上限、吊销 |
| GET/POST | `/projects` | 列出、创建项目 |
| PATCH/DELETE | `/projects/{id}` | 修改项目；`nodes: [{node_id, path, priority}]` |
| GET | `/memories?q=&kind=&limit=` | 有 `q` 时按相关度搜索，否则按更新时间列出（可按 `kind` 过滤）；返回 `{memories, kinds}` |
| POST | `/memories` | 新建 `{kind, content}`，返回 201 和记忆 |
| PUT/DELETE | `/memories/{id}` | 修改 `{kind, content}` / 删除 |
| GET/POST/DELETE | `/schedules` `/schedules/{id}` | 定时任务 |
| GET | `/usage?days=` | 最近 N 天（默认 30）的 token 用量，按 provider、model、role 汇总 |
| GET/PATCH | `/settings` | 权限模式默认值、角色与模型的对应、通知开关、免打扰时段（不返回任何密钥） |
| POST/DELETE | `/push/subscriptions` | Web Push 订阅 |
| GET | `/push/vapid-public-key` | |
| GET | `/updates` | 各平台最新版本信息（来自 GitHub Releases 缓存） |

另有 `GET /healthz`（不在 `/api/v1` 下，不需要登录）返回 `{status, version}`。

**消息字段**：`{id, thread_id, role: "user"|"assistant", content, error?, reply_to?, kind?, seq, created_at}`。`reply_to` 是一条回复所回答的最新用户消息的 seq；回复期间新到的消息，会在下一轮一起回答。`kind` 为空表示普通消息，`notice` 表示主管主动发出的提醒（总在主线），`silent` 表示主管判断这条消息不需要回复（`content` 是原因）。

**记忆**：`{id, kind: "profile"|"project"|"lesson"|"note", content, created_at, updated_at}`。记忆变化时，`/ws` 推送 `memory.put` / `memory.deleted` 事件。

## WebSocket `/ws`

- 连接地址：`/ws?since=<seq>`。服务端先补发 `seq > since` 的事件，然后实时推送新事件。
- 服务端发给客户端的帧：`{"seq": 123, "id": "...", "ts": 0, "type": "task.status", "thread_id"?, "task_id"?, "run_id"?, "payload": {...}}`。事件类型见 `work/specs/sous-mvp/design.md` 第 4 节。
- `seq` 为 0 的是临时事件（目前只有 `message.assistant.delta`，payload `{message_id, delta, thinking?}`），只实时推送，不补发。回复结束后会推送同一个 `message_id` 的 `message.assistant`。
- 写操作一律走 REST；客户端不需要发任何帧。服务端用 WebSocket 协议层的 ping 保活（每 30 秒）。
- 客户端跟不上推送时，服务端会以 1013（Try Again Later）关闭连接；客户端带上最后收到的 seq 重连即可补齐。
