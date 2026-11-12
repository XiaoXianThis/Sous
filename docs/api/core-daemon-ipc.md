# core-daemon-ipc：daemon ↔ 本机客户端

- Provider: core（`sous daemon`，`internal/node/ipcserver.go`；协议在 `internal/ipc`）
- Consumers: core（`sous chat`、`sous status`、`sous update`）、desktop（托盘状态、更新）
- Status: 已实现（M4）。

## 传输

- 所有平台都是 Unix socket（Windows 10 及以上支持），路径 `<sous home>/daemon.sock`（默认 `~/.sous/daemon.sock`）。路径超过 100 字节时（比如 `--home` 指向很深的目录），改用临时目录里的 `sous-<home 路径的哈希>.sock`。
- macOS、Linux 上 socket 文件权限 0600，所在目录 0700；Windows 上靠用户目录的访问控制。不另做认证。
- 一台机器同一个 home 只能有一个 daemon：socket 上已经有 daemon 在应答时，新的 daemon 拒绝启动；没人应答的旧 socket 文件会被删掉。
- 协议：按行分隔的 JSON-RPC 2.0。一条连接上可以并发多个请求；服务端的推送是不带 `id` 的通知。

## 方法

| 方法 | 参数 | 返回 |
|---|---|---|
| `status` | — | `{version, node_id, name, hub_url, hub_connected, relay_url?, page_url?, brain?, pending_frames, runs: [{run_id, task_id?, agent, purpose?, workdir?, paused?}], started_at}`；`hub_connected` 在 0.3 表示和当前大脑的帧流是否连通，`page_url` 是名册里的第一个页面地址，`brain` 是 `leading`（本机持有租约，正在当大脑）、`standby`（名册里是大脑机器，没有持有租约）或不出现 |
| `threads.list` | — | 同 Hub 的 `GET /threads` |
| `threads.create` | `{title}` | 新线程 |
| `messages.list` | `{thread_id, before?, limit?}` | 同 Hub 的 `GET /threads/{id}/messages`（含 `last_seq`） |
| `note.add` | `{text}` | `{note_id}`：新建记事本笔记 |
| `chat.send` | `{thread_id?, content}` | `{thread_id, message_id, seq}`；不给 `thread_id` 就发到主线 |
| `approvals.list` | — | 待审批的操作 `{approvals}` |
| `approvals.decide` | `{id, option_id, note?}` | `{ok}` |
| `events.subscribe` | `{since?}` | `{ok}`，之后在这条连接上持续推送通知：`event`（参数是一个事件，与 `/ws` 推送的相同，包括 seq 为 0 的流式片段）和 `hub`（`{connected}`，daemon 和 Hub 之间的事件流断开或恢复）。连接断开即退订 |
| `logs.tail` | `{lines}` | `{lines: [...]}`：daemon 最近的日志（最多 1000 行） |
| `update.check` | — | `{current, latest, notes_url, newer}` |
| `update.apply` | `{version?}` | `{ok, version}`；安装完成后 daemon 自动重启 |
| `run.progress` | `{run_id, token, text}` | `{text}`：`sous mcp` 的 `report_progress`，在 Run 上记一条“进度：…”状态 |
| `run.ask` | `{run_id, token, text}` | `{text}`：`ask_supervisor`。发 `run.ask` 帧给 Hub，等下一条追加指令作为回答（最多 20 分钟，等不到时返回“按你的判断继续”）；一个 Run 最多问 3 次，同时只能有一个问题 |
| `run.result` | `{run_id, token, done, changed?, verified, left?, decide?}` | `{text}`：`report_result`。Run 的总结里没有“## 结果”一节时，结束时按它补上 |

- 对话、审批类的方法由 daemon 用设备令牌调用 Hub 的 `/api/v1/device/` 接口（见 `core-hub-http.md`）。设备令牌只代表“这台机器”，代办范围限于读线程、发消息、事件流和审批；设置、机器、任务管理等需要 Passkey 登录的操作不能通过 IPC 完成。
- `run.*` 三个方法给 `sous mcp` 用（决策 0025）：daemon 给每个工作 Run 的 agent 会话配一个 stdio MCP 服务 `sous mcp --home <home> --run <id> --token <随机令牌>`（node.json 的 `sous_mcp` 可以换启动命令，`["off"]` 关掉；这个字段只能在本机改）。令牌只在这个 Run 存在时有效，错了或 Run 已结束都返回“这个任务的执行已经结束了”。
- 错误以 JSON-RPC 的 `error`（`code: -32000`，`message` 是中文说明）返回。
