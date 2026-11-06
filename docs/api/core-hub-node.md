# core-hub-node：Hub ↔ Node（daemon）

- Provider: core（`sous-hub`，`internal/nodehub`）
- Consumers: core（`sous daemon`，`internal/node`）
- Status: 已实现（M2，M3 增加任务、策略和交付相关的字段，M4 增加暂停、追加消息的送达记录、更新和持久化的发件箱，M5 增加机器角色、工作空间、岗位的会话选项、运维 Run 和新建 / clone 项目）。两端在同一个 Go module 里，共用 `internal/proto` 的类型，以代码为准；本文给人看，也为以后用其他语言实现 Node 留下依据。

## 配对

1. 用户在 PWA 的“机器”面板生成配对码：`POST /api/v1/nodes/pairing-code`（需要登录），返回 `{code, expires_at}`。配对码是 6 位数字，10 分钟内有效，只能用一次，只存在 Hub 内存里。连续 5 次填错配对码，Hub 作废当时所有有效的配对码（防止猜中六位数），要重新生成。
2. 在电脑上执行 `sous pair <hub-url> <code> [--name 名字] [--role server] [--workspace 目录,目录]`（角色和工作空间只写进本机的 node.json），它会调用 `POST /api/v1/node/pair`（不需要登录）：

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
| N→H | `inventory` | `{agents: [AgentInfo], concurrency, role?: "dev"\|"server", workspaces?: [绝对路径], wok?: 版本}` | ✅ |
| N→H | `node.repos` | `{workspaces, repos: [{path, remote?, branch?, dirty?, wokspace?, git?: {head, upstream, ahead, behind, changes, untracked, fetched_at}, heads?: {分支名: 提交}}]}`：工作空间里找到的全部 git 仓库（往下最多两层，不进仓库、隐藏目录、符号链接和依赖目录）。连上后、工作空间变了、新建或 clone 项目后、每 10 分钟扫描一次，结果没变就不发。`git` 来自 `git status --porcelain=v2 --branch`（不联网），`fetched_at` 是 `FETCH_HEAD` 的修改时间。daemon 每 10 分钟在后台对有 remote 的仓库 `git fetch`（只更新远端跟踪分支，不动工作区；node.json 的 `auto_fetch: false` 关掉），之后重新扫描。`remote` 是 origin（没有就取第一个 remote）配置的地址，不经过 insteadOf 改写。`heads` 是 origin 的远端跟踪分支和它们的提交（最多 50 个，按提交时间），`head_times` 是这些提交的提交时间（unix 秒）；Hub 用它们判断部署分支有没有新提交，按提交时间取最新的那个，不按扫描先后（fetch 落后的机器不会让部署退回旧提交） | ✅ |
| H→N | `run.start` | `{run_id, agent, kind: "code"\|"general"\|"ops"\|"in_place", repo_path?, prompt, permission_mode: "default"\|"auto"\|"open", recovery_note?, task_id?, purpose?: "work"\|"review"\|"git"\|"consult"\|"clone"\|"create", branch?, base_branch?, base_from?: "local"\|"remote", title?, options?: {选项 id: 值}, persona?, setup?: {name, remote?, layout?, owner?, private?}, git?: GitOp, resume_session?}`。`rebase` / `deliver` 是决策 0022 之前的用途，Hub 不再发；Node 收到时直接以失败结束 | — |
| H→N | `run.message` | `{run_id, text, message_id?}`：追加指令，在 agent 当前这一轮结束后交给它；Run 暂停时立刻让它带着这条指令继续。Node 按 `message_id` 去掉重复 | — |
| H→N | `run.pause` | `{run_id, reason?}`：向 agent 发 `session/cancel` 停下当前这一轮，取消它未答复的权限请求，Run 留在原地等待 | — |
| H→N | `run.resume` | `{run_id, text?}`：继续暂停的 Run；没有追加指令时让 agent “接着做” | — |
| N→H | `run.delivered` | `{run_id, message_ids}`：这些追加指令正在送进 agent 的会话（在发出 `session/prompt` 之前上报） | ✅ |
| H→N | `node.update` | `{version, repo}`：从 GitHub Releases 安装这个版本并重启 | — |
| N→H | `node.update_result` | `{version?, error?}`：安装的结果，成功时 Node 随后重启 | ✅ |
| H→N | `run.cancel` | `{run_id, reason?}` | — |
| N→H | `run.started` | `{run_id, workdir, branch?, base_branch?, base_commit?}`：`base_commit` 是任务改动的起点：分支开始的提交，或就地任务开始前的快照 | ✅ |
| N→H | `run.update` | `{run_id, kind: "tool"\|"plan"\|"status"\|"permission"\|"stuck", tool_call_id?, title?, status?, tool_kind?, text?, plan?: [{content, priority, status}], category?, output?}`；`permission` 记录策略自己放行（`status: allow`）或拒绝（`deny`）的操作；`output` 是结束的工具调用的输出（最多 2000 字，密钥打码），只写进 Run 的日志，不进事件表；`stuck` 表示 agent 同一个工具调用连续失败了 4 次（`text` 说明是哪个），Hub 收到后暂停这个 Run 并在主线对话里提醒 | ✅ |
| N→H | `run.output` | 同 `run.update`，`kind` 为 `text` 或 `thought`：agent 输出的文字片段 | ❌ 只转发，断线就丢 |
| N→H | `run.diff` | `{run_id, offset, chunk, final}`：相对基线提交的完整 diff，每块最大 256 KB，不会切断 UTF-8 字符 | ✅ |
| N→H | `run.turn` | `{run_id, text, session_id?}`：顾问 Run 一轮回答结束：完整的回答和 agent 的会话 id（决策 0022） | ✅ |
| N→H | `run.ended` | `{run_id, status: "succeeded"\|"failed"\|"cancelled"\|"interrupted", stop_reason?, summary?, diffstat?, error?, pr_url?, conflict?, git?: GitResult, session_id?, outcome?, error_kind?, refused?: [文本]}`：git Run 带 `git`；顾问 Run 空闲结束时 `stop_reason` 是 `idle`，agent 能恢复会话时带 `session_id`。工作 Run 正常结束、但这一轮没有任何工具调用、没有改动、只说了几句话（不超过 300 字）时：话里是额度、限流、未登录就报 `failed` 加 `error_kind`，否则仍是 `succeeded` 但带 `outcome: no_action`（Hub 不把它算作完成）。`error_kind` 取 `agent_quota`、`agent_rate_limit`、`agent_not_logged_in`、`agent_unavailable`。`refused` 是这个 Run 里被拒绝的操作（用户、审批 agent 或策略），带理由 | ✅ |
| N→H | `approval.request` | `{approval_id, run_id, title, tool_kind?, detail?, options: [{option_id, name, kind}], category?, review?: "user"\|"ai"}` | ✅ |
| H→N | `approval.decide` | `{approval_id, run_id, option_id, note?}`：`option_id` 为空表示取消这个请求 | — |
| H→N | `ack` | `{seq}` | — |
| H→N | `workspace.remove` | `{task_id, repo_path}`：任务关闭后删除它的 worktree（保留分支）；这个任务还有 Run 在跑时忽略 | — |
| H→N | `node.request` | `{op, args}`：远程管理请求，帧 `id` 就是请求 id（见下文“远程管理”） | — |
| N→H | `node.response` | `{req_id, ok, result?, error?: {code, message}}` | ❌ 只转给等待的调用方 |
| N→H | `node.stream` | `{job_id, offset, data}`：作业的终端输出，`offset` 是这块之前的字节数，不会切断 UTF-8 字符 | ❌ 只转发 |
| N→H | `node.job_ended` | `{job_id, kind, agent, exit_code, error?}` | ✅ |

`GitOp`：`{op: "commit"\|"merge"\|"push"\|"pr"\|"undo"\|"discard"\|"restore"\|"health", into?, base?, branch?, remote?, squash?, message?, title?, body?, base_commit?, in_place?, command?}`。`GitResult`：`{op, branch?, before?, after?, commits?, conflicts?: [文件], pr_url?, remote?, task_branch?, task_tip?, message}`，`message` 是给用户的一句话，`task_tip` 是合并前任务分支的提交（撤回合并时任务分支恢复到这里，而不是 rebase 后的副本）。

`AgentInfo`：`{name, title, installed, version?, transport: "acp", logged_in?: bool, note?, install?, upgrade?, login?, logout?, missing?, key_env?, env_set?, options?, options_at?, defaults?, configs?, launch_override?, job?}`。`logged_in` 缺省表示无法判断；其余字段见 `core-hub-http.md` 的“远程管理 agent”。`hello` 多了 `caps: ["admin"]`（支持 `node.request`）和 `admin`（node.json 没有关闭远程管理）。

## 远程管理

- Hub 发 `node.request`，等 `node.response`。请求和应答不进发件箱：断线时 Hub 让等待中的调用返回 `offline`。旧版 daemon 没有 `admin` 能力，Hub 直接返回 `unsupported`。
- `op`：`repos.scan`、`node.set_workspaces {workspaces}`（目录必须存在；返回扫描结果）、`node.set_role {role}`（只能 dev → server）、`gh.pr_state {url}`（只接受 GitHub PR 地址，用 `gh pr view` 读 `{state, merged_at}`）、`agents.detect`、`agents.probe {agent}`、`agents.install {agent, upgrade?, cols?, rows?}`、`agents.login`、`agents.logout`、`agents.set_defaults {agent, options}`、`agents.set_env {agent, name, value}`、`job.input {job_id, data}`、`job.resize {job_id, cols, rows}`、`job.cancel {job_id}`、`job.tail {job_id}`、`config.read {agent, file}`、`config.write {agent, file, content, base_sha256}`、`project.ls {root, rel}`、`project.read {root, rel}`、`project.write {root, rel, content, base_sha256}`（浏览和编辑项目文件：`root` 必须在这台机器的工作空间里、本身是 git 仓库（有 `.git`），`rel` 不能超出 `root`（符号链接也不行）也不能进 `.git`，文件不超过 1 MB）、`shell.open {root, owner, cols?, rows?}` → `JobStarted`（`kind: shell`）、`shell.list {root, owner}` → `[{job_id, root, command, started_at, running, exit_code?}]`、`project.grep {root, rel?, pattern}` → `{matches: [{rel, line, text}], truncated?, files}`（Go 正则，最多 300 个字符；只搜不超过 1 MB 的文本文件，跳过 `.git`、`node_modules`、`vendor`、`dist`、`build`、`target`、`.venv` 等目录和符号链接；最多 200 处、5000 个文件）、`git.status {root}` → `{root, branch?, detached?, head?, upstream?, ahead, behind, changes, untracked, remote_default?, remotes?, branches?: [{name, upstream?, ahead, behind, when, subject?, worktree?}], fetched_at?}`（不联网；`remote_default` 只供参考，不是默认值）、`repo.sync {root}`、`repo.wip {root}`、`repo.fetch {root}`（只 fetch，然后重新扫描：“立即检查部署分支”） → `{outcome, message, branch?, commit?, git?}`（目录规则同 `project.*`；命令写死在 Node 上：fetch + 只快进，或者用临时索引建提交再推到 `sous/wip/`；访问远端时不弹凭据提示；之后重新扫描）。错误码：`disabled`、`conflict`、`invalid`、`not_found`、`busy`、`failed`。
- 请求里只有 agent 名和文件 id：要运行的命令和能读写的文件都写死在 `internal/agents`，Node 不执行请求里带来的命令，也不碰列表以外的路径。node.json 的 `remote_admin: false` 让 Node 拒绝所有请求，这个开关只能在机器本地改。
- 安装、升级、登录、退出登录作为作业在伪终端里运行（macOS / Linux 用 pty，Windows 用 ConPTY），工作目录是用户主目录，环境里加上 agent 的 `agent_env` 和 `TERM=xterm-256color`。同一个 agent 同时只有一个作业，整台机器最多 3 个；超时：安装和升级 15 分钟，登录 10 分钟，退出 2 分钟。作业结束后 Node 重新检测并上报 `inventory`。结束的作业保留 30 分钟，可以 `job.tail`。
- 终端（`shell.open`）：在项目目录（规则同 `project.*`）里用伪终端启动本机的 shell：macOS / Linux 是 `$SHELL -l`（没有时 zsh、bash、sh），Windows 是 pwsh、powershell、cmd 里第一个找得到的。环境是 daemon 的环境加 `SOUS_SHELL=1`。它也是作业，输入、改大小、结束、读尾部都用 `job.*`；最多同时 8 个，与安装作业分开计数，开着超过 24 小时自动结束。`owner` 是开它的登录会话的不透明 id（Hub 从会话 Cookie 推导，不是令牌）：`shell.list` 只列这个 owner 的，`job.*` 的 `owner` 和终端的不一致时按“不存在”回答（安装等作业没有 owner，不检查）。终端里的命令是用户自己敲的，不经过权限策略。node.json 的 `remote_shell: false` 关闭终端（只能本地改，远程编辑 node.json 改不了它）。
- 会话选项：Node 每次打开 agent 的会话（Run 或 `agents.probe`），都记下它在 `session/new` 里报告的 `configOptions`（只记 `model`、`thought_level`、`model_config` 等，不记 `mode`），存在 `~/.sous/agent-options.json`，随 `inventory` 上报。只报告旧版 `models` 字段的 agent，当成一个 id 为 `_model` 的选项，用 `session/set_model` 切换。
- node.json 的 `agent_options`（`{agent: {选项 id: 值}}`）在每个新会话里、切换权限模式之前，用 `session/set_config_option` 逐个设置，模型最先；值已经不在可选列表里就跳过，并发一条 `status` 提醒。`agent_env`（`{agent: {变量: 值}}`）加进 agent 的进程环境，`inventory` 只报告变量名。
- 有些 agent 的会话选项决定它会不会先请求权限（Kiro 的 `autopilot`）。这类选项由 sous 按 Run 的权限模式自己设置（`internal/agents` 的 `AskOptions` / `OpenOptions`），优先于用户的默认值，不出现在 `options` 里，也不能设为默认值；设置失败时 Run 直接失败，不在 agent 不询问的状态下执行。
- 改 node.json（直接编辑，或者 `set_defaults` / `set_env`）后立即生效：启动命令、环境、选项和底线规则用于之后的 Run，并发数要重启 daemon。直接编辑时 `hub_url`、`node_id`、`token`、`remote_admin`、`remote_shell` 保持磁盘上的值，显示成 `***` 的值保持原值，有未知字段或底线规则写错时不保存。安全相关的设置远程只能收紧：服务器的 `role` 不变，`policy.floor_disabled` 和 `policy.floor` 保持磁盘上的值，`policy.extra_floor` 只能增加不能删除；放宽要在机器本地改。

## 可靠性

- Node 发出的“需要确认”的帧都带本地递增的 `seq`，在收到 Hub 对应的 `ack` 之前一直留在发件箱里。重连后，所有未确认的帧会按原顺序重发。Hub 按顺序处理同一连接上的帧，所以 `ack{seq}` 表示这个序号之前的帧都已存储。
- Hub 把帧的 `id` 用作事件 id，重发的帧会因为事件 id 重复被识别出来，只存一次，但照样回 `ack`。
- 发件箱持久化在 `~/.sous/outbox.jsonl`：每个需要确认的帧和每次确认都追加一行，启动时回放，确认累积到 500 次就重写一次文件。daemon 崩溃或重启后，未确认的帧照样重发；`seq` 跨重启继续递增。
- `welcome.resume_runs` 列出 Hub 认为还在这台机器上运行的 Run：
  - Node 上已经没有的（daemon 重启过）：如果 `~/.sous/runs/<run_id>.json` 里存着它的状态，并且 agent 支持 `session/load`，Node 重新启动 agent、加载原来的会话（加载时回放的历史不再上报），先发一段“sous 重启过，先检查做到了哪一步”的说明，再接着执行，Run 不变；否则回 `run.ended`，状态为 `interrupted`（Hub 会新建一个带恢复说明的 Run，见 design.md 第 9 节）。
  - daemon 正常退出（停止服务、更新）时不上报 `run.ended`，留给下一个 daemon 按上面的方式处理。
  - Node 上有、但 Hub 没列出的，说明机器离线期间已在 Hub 上取消，Node 会停掉它（如果 `run.started` 还没被确认，则保留）。
- 重连后，Hub 会把 resume_runs 里已经做出的审批决定、还没送达 agent 的追加指令（没有 `run.delivered` 的 `run.message`）和暂停状态重发一遍。Node 忽略不认识的审批，按 `message_id` 去掉已经收到的指令。
- Hub 收到已结束的 Run 的 `run.ended` 时直接忽略，比如离线期间已经取消的 Run。

## Run 的执行（Node 侧）

1. 准备工作目录：`repo_path` 不为空的 Run 在 `~/.sous/worktrees/<仓库名>/<短 id>` 新建 worktree，短 id 取自 `task_id`（没有时取 `run_id`），所以同一个任务的所有 Run 共用一个 worktree 和分支。分支由 `branch` 指定；第一次时由 Node 起名 `sous/<标题 slug>-<短 id>`，起点必须由 `base_branch` 和 `base_from` 给出，Node 不自己挑分支（决策 0022）：`local` 从本地分支开始（含没推送的提交，Run 里会说明有几个），`remote` 先 `git fetch --prune origin`（30 秒超时，不弹凭据提示）再从 `origin/<base_branch>` 开始（本地有没推送的提交时说明它们不在任务里）；缺起点、分支不存在、fetch 失败或仓库还没有提交时直接失败，不退回别的分支。不跟踪上游，不动用户的 checkout。基线提交和基线分支记在仓库的 git config（`branch.<分支>.sousbase` / `sousbaseref`）里，diff 以它为准。`kind: in_place`（和旧的 `ops`）的 Run 不建 worktree，在 `repo_path` 里就地执行：第一次先在影子仓库（`~/.sous/shadow/<目录哈希>`，以项目目录为工作区，排除 `node_modules`、`.venv` 等）里拍快照，记在 `refs/sous/<短 id>`；diff 相对快照计算，用户的仓库（索引、分支、stash）完全不动，没有 git 的目录也一样。一般 Run 用 `~/.sous/scratch/<短 id>`。然后上报 `run.started`（带 `base_branch` 和 `base_commit`）。
2. 启动 agent 的 ACP 服务（见 `internal/agents`），依次调用 `initialize` 和 `session/new`；agent 要求认证时，先调用 `authenticate` 再重试。按 node.json 的 `agent_options` 设置会话选项（见“远程管理”）。如果 agent 提供“每步询问”的模式（`default`、`ask`、`manual`），就切换过去。
3. 用 `session/prompt` 发送任务。agent 的 `session/update` 转成 `run.update` / `run.output`。`session/request_permission` 先经本地策略（`internal/policy`）分类，再按 `permission_mode` 查表，并按风险评估调整（`policy.Assess` / `policy.Judge`，见 `docs/decisions/0019`：有高风险信号的请求一律交给用户；自动模式下常规操作由 Node 直接放行）：放行或拒绝时直接答复 agent，并发一条 `run.update`（`kind: permission`）记录；需要审批时发 `approval.request`（`review` 为 `user`，或自动审批模式下的 `ai`），等决定回来再答复 agent。底线规则在任何模式下都拒绝，配置在 `node.json` 的 `policy`（`floor_disabled`、`floor`、`extra_floor`，以及只能收紧的 `extra_hard`、`routine_disabled`）。
4. 这一轮结束后，如果期间收到过 `run.message`，就把这些追加指令作为下一轮发给 agent；否则执行 `git add -A`，计算相对基线的 diff，发送 `run.diff`，最后发送 `run.ended`。
5. `purpose: git` 的 Run 不启动 agent（决策 0022），按 `git.op` 执行写死的命令，每一步改动都像 agent 的请求一样经过本地策略和审批：
   - `commit`：在任务的 worktree 里 `git add -A` + commit；`squash` 时把起点之后的提交压成一个。
   - `merge`：先检查 `into` 是本地分支、检出它的 checkout 没有未提交的改动；提交剩下的改动（`squash` 时压成一个），在 worktree 里 `git rebase <into>`；有冲突就 `rebase --abort`、什么都不改，`git.conflicts` 列出冲突的文件；否则只快进 `into`：它检出在某个 checkout 里时在那里 `git merge --ff-only`（类别 outside-worktree），否则 `git update-ref`。之后删掉任务的 worktree 和分支（结果里保留分支的提交，撤回时恢复）。
   - `push`：`git push <remote> <branch>`，从不 force；`remote` 为空时用分支的上游远端，只有一个远端时用它，有多个又没有上游时失败。
   - `pr`：提交剩下的改动，`git push -u origin <任务分支>`，`gh pr create --head <任务分支> --base <base>`。
   - `undo`：撤回 sous 在这个仓库（`branch` 上）最近一次没撤回的提交或合并：结果已经在任何远端分支上（推送过）就失败；分支在那之后又变过也失败；否则分支回到之前的提交（检出时 `git reset --keep`，提交用 `--soft`），被合并删掉的任务分支恢复。
   - `discard`：删除任务的 worktree 和分支（`git branch -D`，类别 irreversible）。
   - `restore`：撤销就地任务（类别 delete）：只动任务改过的文件，即开始快照和结束快照（每个工作 Run 结束时 Node 在影子仓库里记的 `refs/sous/<任务>-end`）之间不同的文件——改过的恢复成开始时的样子，任务新建的删除；用户自己新建或改过的文件不动，任务结束后又被改过的文件也不动，并在结果里列出。
   - `commit`（就地任务）：只提交任务改过的文件，内容取结束快照里的版本；用临时索引建提交，分支没被别人动过才移动它。用户在工作区里的改动（包括任务结束后对同一文件的改动）和已暂存的文件都不受影响。仓库还没有提交时做成第一个提交。
   - `health`：在部署目录里用用户的 shell 执行项目设置的健康检查命令（`command`，经过策略审批，2 分钟超时），退出码 0 才成功。
   移动分支的操作记在 `~/.sous/gitops.jsonl`，供 `undo` 使用。之后重新扫描（推送会更新远端跟踪分支，Hub 由此发现部署分支的新提交）。`run.ended` 带 `git` 结果。
5a. `purpose: review` 的 Run 是审查岗位的验收：在任务的工作目录里运行，Node 把权限模式强制改成 `verify`：只放行读和安全命令（测试、构建），其余直接拒绝、不问任何人，拒绝的理由告诉 agent。
git Run 和等用户审批的 Run 都不占机器的并发槽位：前者不启动 agent，后者在等待期间让出槽位、审批回来后再取回（Hub 计算机器忙不忙时同样不算它们）。同一个任务或同一个 checkout 同时只有一个 git Run（Hub 拒绝第二个）。

5b. `purpose: consult` 的 Run 是项目的顾问：在 `repo_path` 里运行（不拍快照），权限模式强制为 `readonly`（只放行读）。每轮回答结束后发 `run.turn`，然后等下一个 `run.message`；空闲超过 node.json 的 `consult_idle_minutes`（默认 30）就结束 agent，`run.ended` 的 `stop_reason` 为 `idle`、带 `session_id`。顾问不占机器的并发槽位。daemon 重启时，存着的顾问 Run 不恢复，而是以 `idle` 结束（带会话 id）。`resume_session` 不为空时（顾问的下一个问题，或者从讨论转成的任务），agent 支持 `session/load` 就加载那个会话，否则开新会话。
6. 收到 `run.cancel` 时，向 agent 发送 `session/cancel`；10 秒内没有停下，就结束 agent 的进程组。
7. 启动 agent 或打开会话遇到临时性故障（网络错误、408、429、5xx 之类）时，间隔 2 秒、5 秒再试两次；未登录和其他错误不重试。
8. ACP 的通知（`session/update`）按到达顺序处理。
9. `options` 是岗位的会话选项：打开会话后和 node.json 的 `agent_options` 一起设置，优先于后者，但不覆盖 sous 为权限模式强制的选项。
10. `kind: in_place` / `ops` 的 Run 不建 worktree，直接在 `repo_path` 里执行（见第 1 步的快照），结束时上传相对快照的 diff。
11. `purpose: clone` / `create` 的 Run 不启动 agent，在第一个工作空间里执行写死的命令，每条命令和 agent 的请求一样经过本地策略：clone 是 `git clone -- <remote> <name>`；create 在 `layout: wokspace` 时是 `wok init <name> --name <name>` 加 `wok check`，`layout` 为空时只 `git init`（Hub 要求调用方说明结构，没有默认值），`owner` 不为空时再提交并 `gh repo create <owner>/<name> --private|--public --source . --push`（git-remote，要审批）。`run.started` 的 `workdir` 是新仓库的目录。目录已存在、名字不是 kebab-case、没有工作空间、create wokspace 时没装 wok，都直接失败。
12. 机器是服务器（node.json 的 `role: server`）时：`permission_mode: open` 降级为 `default`；改文件、删除、启停服务（`service` 类）和不可逆操作一律请用户审批（`review: user`），不经审批 agent。
