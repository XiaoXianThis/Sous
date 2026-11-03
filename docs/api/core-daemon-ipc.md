# core-daemon-ipc：daemon ↔ 本机客户端

- Provider: core（`sous daemon`）
- Consumers: core（`sous` 命令行的 status、chat、update 子命令）、desktop
- Status: 草案。desktop 第二期才开工，本契约在 M4 实现 `sous chat` 时定稿。

## 传输

- macOS / Linux：Unix socket，路径 `$XDG_RUNTIME_DIR/sous/daemon.sock`；没有这个变量时用 `~/.sous/daemon.sock`，权限 0600。
- Windows：命名管道 `\\.\pipe\sous-daemon-<用户名>`，只允许当前用户访问。
- 协议：按行分隔的 JSON-RPC 2.0。不需要额外认证，访问控制依赖操作系统的文件和管道权限。

## 方法

| 方法 | 参数 | 返回 |
|---|---|---|
| `status` | — | `{version, node_id, hub_connected, runs: [...], agents: [...]}` |
| `update.check` | — | `{current, latest, notes_url}` |
| `update.apply` | `{version}` | `{ok}`；完成后 daemon 自动重启 |
| `logs.tail` | `{lines}` | `{lines: [...]}` |
| `chat.send` | `{thread_id?, content}` | `{thread_id}`；由 daemon 用设备令牌代为调用 Hub |
| `events.subscribe` | `{since?}` | 返回订阅确认，之后服务端持续推送 `event` 通知，内容与 `/ws` 推送的事件相同 |

- 设备令牌只代表“这台机器”，代为聊天的权限范围限定在发消息、读线程、做审批。需要用 Passkey 登录的敏感操作（比如设置、吊销设备）不能通过 IPC 完成。
