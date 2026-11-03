# core-hub-node：Hub ↔ Node（daemon）

- Provider: core（`sous-hub`，`internal/nodehub`）
- Consumers: core（`sous daemon`，`internal/node`）
- Status: 已实现（M2，M3 增加任务、策略和交付相关的字段）。两端在同一个 Go module 里，共用 `internal/proto` 的类型，以代码为准；本文给人看，也为以后用其他语言实现 Node 留下依据。

## 配对

1. 用户在 PWA 的“机器”面板生成配对码：`POST /api/v1/nodes/pairing-code`（需要登录），返回 `{code, expires_at}`。配对码是 6 位数字，10 分钟内有效，只能用一次，只存在 Hub 内存里。
2. 在电脑上执行 `sous pair <hub-url> <code> [--name 名字]`，它会调用 `POST /api/v1/node/pair`（不需要登录）：

   ```json
   {"code": "123456", "name": "这台 Mac", "os": "darwin", "arch": "arm64", "hostname": "mbp"}
   ```

   返回 `{"node_id": "...", "token": "..."}`。配对码无效时返回 403。
3. Hub 只保存设备令牌的 SHA-256（`node_tokens` 表），令牌不进事件日志。Node 把令牌保存在 `~/.sous/node.json`（权限 0600）。
4. 在“机器”面板删除机器（`DELETE /api/v1/nodes/{id}`）会作废令牌并立即断开连接。之后它再连，Hub 会返回 401，daemon 随即退出并提示重新配对。

## 连接

- Node 主动连接 `GET /node`（WebSocket；`https` 的 Hub 用 `wss`），请求头带 `Authorization: Bearer <设备令牌>`。令牌无效时返回 401。
- 每个 WebSocket 文本消息是一个帧：`{"t": "<类型>", "id": "<ULID>", "seq"?: <Node 本地序号>, "body": {...}}`。
- Node 连上后先发 `hello`，Hub 回 `welcome`，然后双方开始正常收发。Hub 每 20 秒发一次 WebSocket ping。
- 断线后 Node 按 1、2、4 秒……最长 30 秒的间隔重连。

## 帧类型

| 方向 | 类型 | body | 需要确认 |
|---|---|---|---|
| N→H | `hello` | `{version, os, arch, hostname, last_acked_seq}` | — |
| H→N | `welcome` | `{node_id, resume_runs: [run_id]}` | — |
| N→H | `inventory` | `{agents: [AgentInfo], concurrency}` | ✅ |
| H→N | `run.start` | `{run_id, agent, kind: "code"\|"general", repo_path?, prompt, permission_mode: "default"\|"auto"\|"open", recovery_note?, task_id?, purpose?: "work"\|"rebase"\|"deliver", branch?, base_branch?, title?, force?}` | — |
| H→N | `run.message` | `{run_id, text}`：追加指令，在 agent 当前这一轮结束后交给它 | — |
| H→N | `run.cancel` | `{run_id, reason?}` | — |
| N→H | `run.started` | `{run_id, workdir, branch?, base_branch?}` | ✅ |
| N→H | `run.update` | `{run_id, kind: "tool"\|"plan"\|"status"\|"permission", tool_call_id?, title?, status?, tool_kind?, text?, plan?: [{content, priority, status}], category?}`；`permission` 记录策略自己放行（`status: allow`）或拒绝（`deny`）的操作 | ✅ |
| N→H | `run.output` | 同 `run.update`，`kind` 为 `text` 或 `thought`：agent 输出的文字片段 | ❌ 只转发，断线就丢 |
| N→H | `run.diff` | `{run_id, offset, chunk, final}`：相对基线提交的完整 diff，每块最大 256 KB，不会切断 UTF-8 字符 | ✅ |
| N→H | `run.ended` | `{run_id, status: "succeeded"\|"failed"\|"cancelled"\|"interrupted", stop_reason?, summary?, diffstat?, error?, pr_url?, conflict?}` | ✅ |
| N→H | `approval.request` | `{approval_id, run_id, title, tool_kind?, detail?, options: [{option_id, name, kind}], category?, review?: "user"\|"ai"}` | ✅ |
| H→N | `approval.decide` | `{approval_id, run_id, option_id, note?}`：`option_id` 为空表示取消这个请求 | — |
| H→N | `ack` | `{seq}` | — |
| H→N | `workspace.remove` | `{task_id, repo_path}`：任务关闭后删除它的 worktree（保留分支）；这个任务还有 Run 在跑时忽略 | — |

`AgentInfo`：`{name, title, installed, version?, transport: "acp", logged_in?: bool, note?}`。`logged_in` 缺省表示无法判断。

## 可靠性

- Node 发出的“需要确认”的帧都带本地递增的 `seq`，在收到 Hub 对应的 `ack` 之前一直留在发件箱里。重连后，所有未确认的帧会按原顺序重发。Hub 按顺序处理同一连接上的帧，所以 `ack{seq}` 表示这个序号之前的帧都已存储。
- Hub 把帧的 `id` 用作事件 id，重发的帧会因为事件 id 重复被识别出来，只存一次，但照样回 `ack`。
- 发件箱只在内存里：daemon 进程重启后，未确认的帧会丢失（M4 改为持久化）。
- `welcome.resume_runs` 列出 Hub 认为还在这台机器上运行的 Run：
  - Node 上已经没有的（比如 daemon 重启过），Node 回 `run.ended`，状态为 `interrupted`。
  - Node 上有、但 Hub 没列出的，说明机器离线期间已在 Hub 上取消，Node 会停掉它（如果 `run.started` 还没被确认，则保留）。
- 重连后，Hub 会把 resume_runs 里已经做出的审批决定重发一遍，Node 忽略不认识的审批。
- Hub 收到已结束的 Run 的 `run.ended` 时直接忽略，比如离线期间已经取消的 Run。

## Run 的执行（Node 侧）

1. 准备工作目录：代码 Run 在 `~/.sous/worktrees/<仓库名>/<短 id>` 新建 worktree，短 id 取自 `task_id`（没有时取 `run_id`），所以同一个任务的所有 Run 共用一个 worktree 和分支。分支由 `branch` 指定；第一次时由 Node 起名 `sous/<标题 slug>-<短 id>`，基线是仓库当前的 `HEAD`，基线提交和基线分支记在仓库的 git config（`branch.<分支>.sousbase` / `sousbaseref`）里。一般 Run 用 `~/.sous/scratch/<短 id>`。然后上报 `run.started`（带 `base_branch`）。
2. 启动 agent 的 ACP 服务（见 `internal/agents`），依次调用 `initialize` 和 `session/new`；agent 要求认证时，先调用 `authenticate` 再重试。如果 agent 提供“每步询问”的模式（`default`、`ask`、`manual`），就切换过去。
3. 用 `session/prompt` 发送任务。agent 的 `session/update` 转成 `run.update` / `run.output`。`session/request_permission` 先经本地策略（`internal/policy`）分类，再按 `permission_mode` 查表：放行或拒绝时直接答复 agent，并发一条 `run.update`（`kind: permission`）记录；需要审批时发 `approval.request`（`review` 为 `user`，或自动审批模式下的 `ai`），等决定回来再答复 agent。底线规则在任何模式下都拒绝，配置在 `node.json` 的 `policy`（`floor_disabled`、`floor`、`extra_floor`）。
4. 这一轮结束后，如果期间收到过 `run.message`，就把这些追加指令作为下一轮发给 agent；否则执行 `git add -A`，计算相对基线的 diff，发送 `run.diff`，最后发送 `run.ended`。
5. `purpose: deliver` 的 Run 不启动 agent：在任务的 worktree 里提交剩下的改动，按策略审批 `git push`（类别 git-remote），然后 `git push -u origin <分支>`（`force` 时用 `--force-with-lease`），再用 `gh pr create --head <分支> --base <base_branch>` 创建 PR（已有打开的 PR 就复用），用 `gh pr view` 读 `mergeable`。结束时 `run.ended` 带上 `pr_url` 和 `conflict`。
6. 收到 `run.cancel` 时，向 agent 发送 `session/cancel`；10 秒内没有停下，就结束 agent 的进程组。
