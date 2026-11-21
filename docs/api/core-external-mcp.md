# core-external-mcp：托管工作区的外部项目工具

Provider 是常在线的 `sous hosted-hub`，不是 ACP 主管、旧 Node 的 `sous mcp` 或 Hub 配置页中用于接入其他工具服务器的 MCP。默认不装配；运维在 `hosted.yaml` 的 `external_mcp_clients` 注册公开客户端 `id`、显示 `name` 和唯一固定 HTTPS `redirect_uri`，才会在同一工作区 origin 启用。外部客户端和工作台直接访问同一份权威项目状态，不经过主管或 Relay。

## 授权

- MCP 资源为 `<工作区 origin>/mcp`。未带有效 Bearer 时返回 `401` 和 `WWW-Authenticate: Bearer resource_metadata="<origin>/.well-known/oauth-protected-resource/mcp"`。授权服务器元数据在 `<origin>/.well-known/oauth-authorization-server`。
- 已注册的公开客户端使用授权码和 **PKCE S256**，必须传确切 `redirect_uri`、`state`、`resource=<origin>/mcp`。只支持 `project.read` 与可选的 `drafts.write`，未请求的写范围不能获批。授权页面核对注册客户端和回调，工作区所有者用 Passkey 登录后，明确选一个有效、未归档且非杂项的项目；创建草稿默认不勾选，批准需要近期 Passkey。
- `POST /oauth/token` 使用表单数据：`authorization_code` 要求 `client_id`、`redirect_uri`、`resource`、`code`、`code_verifier`；`refresh_token` 要求 `client_id`、`resource`、`refresh_token`。未启用动态客户端注册、客户端密钥、浏览器跨域 API 或回环 HTTP 回调。回调地址不能带初始 query/fragment，不接受前缀或通配匹配。
- 授权码仅使用一次，2 分钟有效；访问凭据最多 15 分钟；授权在 7 天后失效，不因续期延长。刷新令牌轮换，旧令牌重放会撤销该授权的全部会话。Hub 持久化摘要，不保存原码或令牌。改变注册回调、移除客户端或归档项目时，现有凭据不能使用。
- `POST /oauth/revoke` 接收公共客户端 `client_id` 和已拥有的 `token`，撤销该授权。所有者可在「设置 → 外部连接」直接撤销，撤销不要求外部客户端配合。撤销已确认后不会出现迟到的新草稿；此前已经读取的数据不可追回。

## 接口

授权后的 `/mcp` 为 HTTPS Streamable HTTP，请求—响应 JSON，**无长连接或服务端会话状态**。每次请求需 Authorization Bearer；不接受 Cookie、浏览器 Origin 或 URL 中的凭据。服务端在同一数据库事务中核对草稿权限并登记事件。跨项目没有“指定项目 ID”参数；任务 ID 查询也受项目过滤。

| 工具 | 权限 / 输入 | 结果 |
|---|---|---|
| `project_get` | `project.read`，空对象 | 授权项目的 ID 与名称，不暴露仓库地址或宿主路径字段。 |
| `task_list` | `project.read`，可选 `after` | 分页返回项目内任务 ID、标题、状态；每页最多 30 项。 |
| `task_get` | `project.read`，`id` | 项目内单个任务的 ID、标题、状态和有长度边界的说明。其他项目与不存在的 ID 同样不可读。 |
| `task_create` | 仅批准了 `drafts.write` 才列出；`request_id`（小写 UUID）、`title`、可选 `detail` | 同一授权和 `request_id` 的相同输入幂等，创建 `inbox` 草稿。不能指派、执行、审批、打开目录或选择机器。 |

所有者 WebUI 另用以下同源接口：`GET /api/v1/external-mcp/preview?request=…`、`POST /api/v1/external-mcp/decision`、`GET /api/v1/external-mcp/grants`、`DELETE /api/v1/external-mcp/grants/{id}`。它们使用工作台登录/CSRF，批准另检查近期 Passkey；外部 Bearer 不可使用。授权 UI 提示要分享任务说明，禁止在需共享的任务中写入密钥等敏感资料。

[外部 MCP 部署与兼容说明](../guides/external-mcp.md)
