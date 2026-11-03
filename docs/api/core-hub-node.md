# core-hub-node：Hub ↔ Node（daemon）

- Provider: core（`sous-hub`）
- Consumers: core（`sous daemon`）
- Status: 草案。两端在同一个 Go module 里，共用 `internal/proto` 的类型；本文是给人看的说明，也为以后用其他语言实现 Node 留下依据。

## 连接

- Node 主动连接 `wss://agent.wxian.cc/node`，请求头带 `Authorization: Bearer <设备令牌>`。
- 设备令牌通过配对获得：`POST /api/v1/node/pair {"code": "123456", "name": "...", "os": "...", "arch": "..."}` 返回 `{"node_id", "token"}`。
- 每一帧都是一个 JSON 对象：`{"t": "<类型>", "id": "<ULID>", "ref"?: "<回应的帧 id>", "seq"?: <Node 本地序号>, "body": {...}}`。

## 帧类型

| 方向 | 类型 | body |
|---|---|---|
| N→H | `hello` | `{version, os, arch, last_acked_seq}` |
| H→N | `welcome` | `{node_id, policy_overrides, resume_runs: [run_id]}` |
| N→H | `inventory` | `{agents: [{name, version, transport: "acp"\|"jsonstream"\|"pty", logged_in: bool}], concurrency}` |
| H→N | `run.start` | `{run_id, task: {id, kind: "code"\|"general", title, detail}, project?: {path, base_branch}, agent, permission_mode, branch?, recovery_note?, pending_inputs?: [text]}` |
| H→N | `run.deliver` | `{run_id, branch, title, body}`：push 分支并创建 PR（按 git-remote 审批） |
| H→N | `run.message` | `{run_id, text}` |
| H→N | `run.cancel` | `{run_id, reason}` |
| N→H | `run.update` | `{run_id, kind: "progress"\|"tool"\|"output"\|"plan"\|"input_read", summary, log_offset}`（`input_read` 表示追加消息已送进 agent 会话） |
| N→H | `run.log` | `{run_id, offset, chunk}`（原始输出，分块上传） |
| N→H | `run.diff` | `{run_id, offset, chunk, final: bool}`：Run 结束时分块上传相对基线分支的完整 diff |
| N→H | `run.ended` | `{run_id, status: "succeeded"\|"failed"\|"cancelled"\|"interrupted", report: "ready"\|"waiting"\|"blocked"\|"failed"\|"dropped", summary, branch?, diffstat?, question?}` |
| N→H | `run.delivered` | `{run_id, pr_url?, error?}` |
| N→H | `approval.request` | `{approval_id, run_id, category, detail, command?, paths?}` |
| H→N | `approval.decide` | `{approval_id, decision, note?, patch?}` |
| 双向 | `ack` | `{seq}`：确认已持久化，对方可以丢弃本地缓存 |
| 双向 | `ping` / `pong` | 每 20 秒一次，连续 3 次没有回应就视为断线 |

## 可靠性

- Node 上报的每一帧都带本地递增的 `seq`；Hub 写入数据库后回 `ack`。断线期间 Node 先缓存在本地，重连后从 `last_acked_seq` 开始补发。
- Hub 按帧 `id` 去重。
- `welcome.resume_runs` 列出 Hub 认为还在这台机器上运行的 Run。Node 逐一核对：还在运行的继续上报；已经消失的回 `run.ended`，状态为 `interrupted`。
- Node 收到 `approval.decide` 后，仍要按本地策略再校验一遍，底线规则命中时一律拒绝。
