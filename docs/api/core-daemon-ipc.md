# core-daemon-ipc：daemon ↔ 本机客户端

- Provider: core（`sous daemon`，`internal/node/ipcserver.go`；协议在 `internal/ipc`）
- Consumers: core（`sous chat`、`sous status`、`sous update`）、desktop（第二期）
- Status: 已实现（M4）。

## 传输

- 所有平台都是 Unix socket（Windows 10 及以上支持），路径 `<sous home>/daemon.sock`（默认 `~/.sous/daemon.sock`）。路径超过 100 字节时（比如 `--home` 指向很深的目录），改用临时目录里的 `sous-<home 路径的哈希>.sock`。
- macOS、Linux 上 socket 文件权限 0600，所在目录 0700；Windows 上靠用户目录的访问控制。不另做认证。
- 一台机器同一个 home 只能有一个 daemon：socket 上已经有 daemon 在应答时，新的 daemon 拒绝启动；没人应答的旧 socket 文件会被删掉。
- 协议：按行分隔的 JSON-RPC 2.0。一条连接上可以并发多个请求；服务端的推送是不带 `id` 的通知。

## 方法

| 方法 | 参数 | 返回 |
|---|---|---|
| `status` | — | `{version, node_id, name, hub_url, hub_connected, pending_frames, runs: [{run_id, task_id?, agent, purpose?, workdir?, paused?}], started_at}` |
| `threads.list` | — | 同 Hub 的 `GET /threads` |
| `threads.create` | `{title}` | 新线程 |
| `messages.list` | `{thread_id, before?, limit?}` | 同 Hub 的 `GET /threads/{id}/messages`（含 `last_seq`） |
| `chat.send` | `{thread_id?, content}` | `{thread_id, message_id, seq}`；不给 `thread_id` 就发到主线 |
| `approvals.list` | — | 待审批的操作 `{approvals}` |
| `approvals.decide` | `{id, option_id, note?}` | `{ok}` |
| `events.subscribe` | `{since?}` | `{ok}`，之后在这条连接上持续推送通知：`event`（参数是一个事件，与 `/ws` 推送的相同，包括 seq 为 0 的流式片段）和 `hub`（`{connected}`，daemon 和 Hub 之间的事件流断开或恢复）。连接断开即退订 |
| `logs.tail` | `{lines}` | `{lines: [...]}`：daemon 最近的日志（最多 1000 行） |
| `update.check` | — | `{current, latest, notes_url, newer}` |
| `update.apply` | `{version?}` | `{ok, version}`；安装完成后 daemon 自动重启 |

- 对话、审批类的方法由 daemon 用设备令牌调用 Hub 的 `/api/v1/device/` 接口（见 `core-hub-http.md`）。设备令牌只代表“这台机器”，代办范围限于读线程、发消息、事件流和审批；设置、机器、任务管理等需要 Passkey 登录的操作不能通过 IPC 完成。
- 错误以 JSON-RPC 的 `error`（`code: -32000`，`message` 是中文说明）返回。
