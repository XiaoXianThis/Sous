# core-relay：中继协议

- Provider: core（`sous-relay`，`internal/relay`）
- Consumers: core（`sous` 的 `internal/tunnel`）、web（`src/trust/net.ts`）
- Status: 已实现（0.3.0-alpha1，决策 0030）。以代码为准。

中继是一个“盲邮局”：转发和存放密文、存放签名文档，不持有任何有用的密钥。它做的校验只是为了让诚实的中继保持整洁；**每个接收方都会自己重新校验**，中继说的话一律不信（格式见 `sous-envelope.md`）。

## 连接

- `GET /v1/ws`：WebSocket，文本帧，每帧一个 JSON 对象 `{"t": "<类型>", ...}`。带 `rid` 的请求，应答会带上同一个 `rid`；出错时应答是 `{"t":"err","rid","error"}`。
- `GET /healthz`：`{ok, claimed, version}`，允许跨域。
- 页面是浏览器时，`Origin` 必须在 `sous-relay serve -origin` 列出的范围内。

### 握手

1. 中继发送 `{"t":"challenge","nonce":<32 字节，base64url>}`。
2. 客户端二选一：
   - **认证**：`{"t":"auth","addr","pub","sig","cert"?}`。其中 `sig` 是对 nonce 的签名（上下文 `sous/v1/relay-auth`）。
     - `addr` 为 `node:<id>` 时，这台机器必须在名册里，且公钥一致。
     - `addr` 为 `sess:<sid>` 时，必须带上身份签发、仍在有效期内的会话证书 `cert`，证书里的公钥要和 `pub` 一致。
   - **匿名**：`{"t":"anon"}`。匿名连接只能做三件事：认领中继（`claim`）、读取身份包（`get identity`）、进出配对房间（`room.join` / `room.send`）。
3. 中继应答 `{"t":"welcome","addr"?}`。机器连上后，中继会按顺序推送它信箱里积压的消息。

## 请求

| t | 字段 | 说明 |
|---|---|---|
| `claim` | `token`、`body: {roster, bundle}` | 认领中继，只在首次设置时用一次。`sous-relay init` 打印的认领码只能用一次；认领后固定身份公钥 |
| `send` | `to`、`data` | 把一条消息（一般是加密的 Wire）转给 `to`。收件人是机器时，消息先进信箱（保留 7 天），对方确认后删除；收件人是会话时，只投递给在线的连接 |
| `ack` | `id` | 机器确认收到信箱里的第 `id` 条 |
| `get` | `name` | 读文档：`roster`、`roster@<v>`（历史版本）、`scopes`、`pause`、`domain`、`lease`、`snapshot`、`identity`、`alert/<node>` |
| `put` | `name`、`body` | 写文档，见下表 |
| `blob.put` / `blob.get` | `name`、`blob`（base64url） | 大块数据（快照分块 `snapshot/<槽>/<i>`）。只有持有租约的大脑能写快照 |
| `room.open` | `room` | 已认证的会话打开配对房间（有效 10 分钟） |
| `room.join` / `room.send` | `room`、`data` | 机器加入房间，双方互发消息。房间编号由配对串里的秘密派生，中继不知道秘密本身 |
| `presence` | — | 返回 `{online: [地址]}`。中继说谁在线并不可信，只用来参考 |
| `ping` | — | 返回 `pong` |

## 中继推送

`{"t":"msg","id"?,"from","data"}`（转发的消息）、`{"t":"doc","name","body"}`（文档变了）、`{"t":"presence","online"}`、`{"t":"room","room","data"}`。

## 写文档时中继做的检查

| 文档 | 谁能写 | 检查 |
|---|---|---|
| `roster` | 会话 | 身份签名，版本只增不减；让权限变大的修改必须附带用户断言（`AcceptRoster`）。每个版本另存为 `roster@<v>`。写入后，中继会断开不在名册里的机器 |
| `scopes` | 会话 | 身份签名，版本只增不减，必须附带用户断言 |
| `pause` / `domain` | 会话 | 身份签名，版本只增不减 |
| `identity` | 会话 | 身份包里的签名公钥必须是这个中继主人的 |
| `lease` | 大脑机器本身 | 签名者是名册里的大脑机器，并且就是持有者本人；任期只增不减；同一任期不能被别的机器抢走 |
| `snapshot` | 持有租约的大脑 | — |
| `alert/<node>` | 那台机器自己 | — |
