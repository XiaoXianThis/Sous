# sous-envelope：密钥、签名文档和加密通道

- Provider: core（`internal/trust`）
- Consumers: core（tunnel、node、brain、relay）、web（`src/trust/`）
- Status: 已实现（0.3.0-alpha1，决策 0030）。两端逐字节兼容，测试向量在 `work/tests/fixtures/trust-vectors.json`（Go 生成）和 `trust-vectors-web.json`（页面生成），双方的测试互相验证对方的向量。

## 编码与算法

- 二进制字段一律用 base64url，不带填充。
- 签名：ECDSA P-256，用 SHA-256 对“上下文 + `\0` + 消息”做摘要，签名格式为 r‖s（64 字节）。公钥是未压缩点（65 字节）。
- 密钥封装：HPKE（RFC 9180，base 模式），套件为 KEM X-Wing（MLKEM768-X25519，`0x647a`）、HKDF-SHA256、AES-256-GCM。私钥是 32 字节种子，公钥 1216 字节。
- KeyID = SHA-256(公钥) 的前 12 字节。机器 id = `n` + base32 小写（SHA-256(`sous/v1/node-id\0` + 签名公钥) 的前 15 字节）。
- 地址：`node:<id>`、`sess:<sid>`，其中 sid 是会话签名公钥的 KeyID。

## 签名文档

格式为 `{"p": payload, "k": KeyID, "s": sig}`。签名直接覆盖 payload 的原始字节，签名上下文是 `sous/v1/doc/<t>`，payload 的 `t` 字段必须和类型一致。验证时先验字节，再解析 JSON，所以不需要规范化 JSON。

| t | 签名者 | 内容 |
|---|---|---|
| `roster` | 身份 | `v`、`prev`、`user{sign_pub, kem_pub, passkeys[]}`、`rp_id`、`origins`、`relay`、`nodes[{id, name, sign_pub, kem_pub, key_storage, os, role, brain{priority}?, frozen?, console?}]`。`console` 为真时，这台机器的终端（`sous chat`）能读全部对话、处理所有审批；从无到有需要断言 |
| `session` | 身份 | `sid`、`sign_pub`、`kem_pub`、`iat`、`exp`（有效期最长 24 小时） |
| `scopes` | 身份，并附断言 | `v`、`default`、`projects{项目键: Scope}`。Scope 包括 `nodes`、`max_mode`、`allow_merge`、`allow_ops`、`allow_open` |
| `pause` | 身份 | `v`、`paused` |
| `domain` | 身份 | `v`、`epochs[{epoch, wraps{成员: HPKE 密文}}]`。HPKE 的 info 为 `sous/v1/domain\0<epoch>\0<member>` |
| `lease` | 大脑机器 | `term`、`holder`、`iat`、`exp` |
| `snapshot` | 持有租约的大脑 | `holder`、`term`、`seq`、`epoch`、`size`、`chunks[每块密文的 SHA-256]`。每块用 AES-GCM 加密，AAD 为 `sous/v1/snapshot\0<seq>\0<i>` |
| `release` | 发布密钥 | `tag`、`checksums_sha256`（对应 `checksums.txt.sig`） |
| `audit` | 机器 | 机器的冻结警报：`node`、`frozen`、`reason` |

需要断言的修改，提交时是 `{"doc": 签名文档, "assertion": 断言}` 的形式。

## 用户断言（关键操作）

- challenge = SHA-256(`sous/v1/critical\0` + 被批准的字节)。被批准的字节可以是名册或范围的 payload、许可（permit）的原始字节，或者意图（intent）的原始字节。
- 验证方检查以下几项：`cred_id` 在名册里；`rpIdHash` = SHA-256(rp_id)；`clientDataJSON` 的 type 为 `webauthn.get`，challenge 与上面的值一致，origin 在名册的 `origins` 里；UP 和 UV 两个标志位都已置位；签名（ASN.1 DER）用名册里的公钥能验证通过；这个 challenge 没有用过。

## 许可与意图

- **许可（permit）**：`{t:"permit", id, sid, node, action, match, critical, iat, exp}`，由会话直接发给目标机器，消息为 `{k:"permit", id, raw, assertion?}`。
  - `action` 取值：`admin`（match 里至少有 `op`）、`update`（`version`）、`run`（`task_id`）、`unfreeze`。
  - 关键操作的许可必须附带断言。许可有效 5 分钟，用一次就失效。
  - `admin` 许可必须写明它批准的内容，机器按请求重新算出同样的字段，逐项核对（`node.RequestMatch`）：

    | op | match 必须包含 |
    |---|---|
    | `agents.install` | `agent`、`upgrade`（`"true"`/`"false"`） |
    | `agents.login`、`agents.logout` | `agent` |
    | `agents.set_env` | `agent`、`name`、`value_sha256` |
    | `config.write` | `agent`、`file`、`content_sha256` |
    | `node.set_role` | `role` |
    | `node.set_workspaces` | `workspaces_sha256`（各路径用 NUL 连接后的 SHA-256） |
    | `shell.open` | 无（终端只接受打开它的会话输入） |

    `*_sha256` 都是 UTF-8 字节的 SHA-256，base64url 编码。
- **意图（intent）**：`{t:"intent", sid, method, path, body_sha256, nonce, iat}`。只对大脑本身的关键请求使用（模型供应商、MCP），放在 RPC 请求头 `X-Sous-Intent` 和 `X-Sous-Assertion` 里。

## 通道

- **开通道**：发送方生成 `ch`（16 字节随机数），用 `hpke.NewSender(收件人 KEM 公钥, info="sous/v1/chan\0ch\0from\0to")` 得到 `enc`，再从中导出 `key = Export("sous/v1/chan-key", 32)`。然后用自己的签名密钥签一份 `{t:"chan", ch, from, to, to_key, enc, iat}`。
- **Wire 消息**：`{open?, cert?, ch, seq, ct}`。`seq` 从 1 开始递增；`nonce` 为 4 个零字节加 8 字节大端序的 seq；`aad` 为 `sous/v1/msg\0ch\0` 加 8 字节 seq。
- **接收方检查**：
  - 签名者在名册里且没有被冻结；如果是会话，看它附带的证书。
  - 通道确实是发给自己、自己的 KEM 密钥。
  - 开通道时间在 10 分钟以内，并且这个通道 id 以前没见过（重启后也记得）。
  - seq 必须递增；出现跳号要上报。
- **换钥**：发送方每小时或每 10 万条消息换一条新通道（新的 HPKE 封装、新的密钥）。接收方继续读旧通道 10 分钟，之后丢掉更早的通道和它们的密钥。
- **收到不认识的通道**：如果消息里没有 `open`，接收方回复 `{reset: ch}`，发送方换一条新通道重发。
- **通道里的载荷**是 JSON：`{k:"link"...}` 是机器和大脑之间的帧流（附带方向标记 `dir`），其余 `k` 见 `core-brain-rpc.md`。

## 配对串

`sous1-` 加上 base32 小写编码的：版本（1 字节）、中继地址 SHA-256 的前 8 字节、身份指纹（32 字节）、秘密（16 字节）、过期时间（4 字节）。配对房间号和两端的 HMAC（分别为 `sous/v1/pair-node` 和 `sous/v1/pair-app`）都由这个秘密派生。
