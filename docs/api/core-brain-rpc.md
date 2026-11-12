# core-brain-rpc：页面和机器 ↔ 大脑

- Provider: core（`internal/brain`，运行在持有租约的大脑机器上）
- Consumers: web（`src/trust/rpc.ts`、`src/lib/api.ts`）、core（daemon 的设备调用：`sous chat`、`sous mcp`）
- Status: 已实现（0.3.0-alpha1，决策 0030、0031）

0.2 的 Hub HTTP 接口（`core-hub-http.md`）和 Node 帧协议（`core-hub-node.md`）**语义不变**，但不再暴露在任何网络端口上。现在它们装在加密通道里（`sous-envelope.md`），发往当前持有租约的大脑。

## 消息（通道载荷）

| k | 方向 | 字段 |
|---|---|---|
| `rpc` | 调用方 → 大脑 | `id`、`method`、`path`、`header`、`body`（base64url） |
| `rpc.res` | 大脑 → 调用方 | `id`、`status`、`header`、`body`；`error` 表示“这台机器现在不是大脑”，收到后重新查看租约再重试 |
| `ws.open` / `ws.ok` / `ws.msg` / `ws.close` | 双向 | WebSocket 形式的事件流（`/ws`、`/api/v1/system/stream`、`/api/v1/device/ws`）。`ws.msg` 的 `body` 是一条事件 |
| `link` | 机器 ↔ 大脑 | 帧流（`core-hub-node.md` 里的帧）：`op` 为 open/ok/frame/close，`ep` 标识这条帧流，`dir` 标明方向 |

大脑把请求转进内存里的 Hub，并在请求的 context 里注明来源（`auth.Tunnel`）：会话的 `SID`、机器的 `Device`，以及 `Fresh`（这次请求已由用户本人批准）。

## 规则

- **只转发安全的请求头**：`Content-Type`、`Accept`、`If-Match`、`If-None-Match`、`Last-Event-ID`。不使用 Cookie，也不做 CSRF 校验。
- **已经去掉的路由**，访问时返回 410：配对码、`DELETE /nodes/{id}`、`/node/pair`、`/auth/*`。机器的加入和移除改走名册。
- **机器只能调用设备接口** `/api/v1/device/*`，访问其他路径返回 403。设备身份就是那台机器本身。
- **大脑本身的关键请求**（模型供应商、MCP 等）：大脑先返回 401 `reauth`，页面随后带上意图和断言重发同一个请求，大脑核对两者都和这次请求对得上才执行。
- **由机器自己把关的关键操作**：页面先把许可直接发给机器，再调用大脑。机器上的守卫检查许可，大脑无法替用户伪造。这类操作包括：打开终端、安装 / 登录 / 退出 agent、写 API Key、写配置文件、修改角色或工作空间、更新。终端输入（`job.input`）由会话直接发给机器，大脑转发的输入一律拒绝。
- **机器直接回答会话的消息**：`permit.res`、`job.input.res`、`job.resize.res`、`audit`（`{k:"audit.get", since}` 的应答，带哈希链）、`node.pong`。
