# core-hub-http：Hub ↔ 客户端（PWA、desktop）

- Provider: core（`sous-hub`）
- Consumers: web、desktop
- Status: 草案，随 sous-mvp 评审定稿。定稿后补充 OpenAPI 描述，以描述文件为准。

## 约定

- 基础地址：`https://agent.wxian.cc`，所有接口都在 `/api/v1` 下面。
- 认证：Passkey 登录后下发 HttpOnly 会话 Cookie；会改动数据的请求要带 `X-CSRF-Token` 头。
- 数据格式：JSON；时间用 Unix 毫秒；ID 用 ULID 字符串。
- 错误格式：`{"error": {"code": "not_found", "message": "..."}}`。
- 近期登录：少数危险操作（打开终端、已登录时再加一个 Passkey）要求会话在 15 分钟内用 Passkey 验证过，否则返回 401 `reauth`。页面重新走一遍 `/auth/passkey/login/*`（开发模式下是 `/auth/dev-login`）后重试：已登录时再次验证只刷新当前会话的验证时间，会话和属于它的终端不变。
- Passkey 要求用户验证（`userVerification: required`：指纹、面容或 PIN），只碰一下不算。
- 安全响应头：每个回答都带 `Content-Security-Policy`（只允许本站的脚本、连接和 Worker，禁止内联脚本和被嵌入 iframe）、`X-Frame-Options: DENY`、`X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`；`public_url` 是 https 时加 HSTS。请求体最大 4 MB。

## REST

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/auth/passkey/register/begin` `/finish` | 注册 Passkey（首次使用一次性链接；之后要求已登录） |
| POST | `/auth/passkey/login/begin` `/finish` | 登录 |
| POST | `/auth/logout` | 退出 |
| POST | `/auth/dev-login` | 仅当 `dev: true` 且请求直接来自本机回环地址时可用，否则 404 |
| GET | `/me` | `{logged_in, has_passkey, dev_login}`，不需要登录 |
| GET/POST | `/threads` | 列出、创建线程。`GET /threads` 只列一般线程（主线和普通对话）；`GET /threads?project_id=<id>` 列这个项目的项目对话。`POST /threads {"title", "project_id"?}` 带 `project_id` 时建项目对话（`kind: project`，项目不存在返回 404）。线程对象带 `project_id`（只在项目对话上有） |
| GET/PATCH | `/threads/{id}` | 读取、改名或归档 |
| GET | `/threads/{id}/messages?before=&limit=` | 分页读取历史消息（按 seq 从旧到新；用户消息带 `attachments: [{id, name, mime, size}]`）；返回 `{messages, last_seq}`，`last_seq` 是全局最新的事件序号，客户端用它作为 `/ws?since=` 的起点 |
| GET | `/threads/{id}/context` | 对话的上下文（见下文“上下文”）：`{segment, covers_up_to, summary, profile, reason?, compacted_at?, used, window, soft, hard, cache: {calls, input_tokens, cached_tokens, last_input}}`。`segment` 为 0 表示还没有第一段；`used` 是下一轮输入 token 的估算；`cache` 汇总主管在这条对话里最近 20 次调用 |
| POST | `/threads/{id}/compact` | 立即把较早的消息整理进摘要（原因 `manual`），返回 202 `{queued: true}`；在这条对话正在进行的回合结束后执行，完成时 `/ws` 推送 `thread.compacted` |
| PUT | `/threads/{id}/summary` | 用户改写摘要 `{"summary": "..."}`（最多 20000 字），返回 202；开始新的一段（原因 `edit`），覆盖的消息不变 |
| POST | `/threads/{id}/messages` | 发送消息 `{"content": "...", "context"?: {project_id?, task_id?, mentions?: [{project_id, name}]}, "attachments"?: [附件 id]}`（只有附件、`content` 为空也可以；附件 id 必须是 `POST /attachments` 返回的，最多 8 个，否则 400 `bad_attachment`），返回 202 `{message_id, seq}`；回复通过 `/ws` 推送。`context` 是用户写这条消息时在工作台上看的项目或任务，主管据此理解“这个”指什么。`context.mentions` 是用户在正文里用 `@名字` 引用的项目（`name` 是正文 @ 后面的文字）：Hub 去掉没有 `project_id` 的和重复的，最多留 16 个，原样存进消息的 `context`，并在给主管的这条用户消息前注明每个 @ 对应的项目和 id；只有 `mentions` 时不算“正在看”的项目。项目对话里主管的作用域限定在这个项目：环境只列这个项目和它的任务，`task_create` 不填 `project` 时归到这个项目，动别的项目或别的项目的任务会被拒绝 |
| POST | `/attachments?name=<文件名>` | 上传一个文件：请求体是文件本身的字节（不是 JSON），最多 20 MB。返回 201 `{id, name, mime, size}`。类型由 Hub 按内容判断，不信客户端：PNG / JPEG / GIF / WebP 是图片（`image/*`），UTF-8 文本是 `text/plain`，PDF 是 `application/pdf`，其余（包括 SVG、HTML）一律 `application/octet-stream`。文件名里的路径分隔符会被换成 `_` |
| GET | `/attachments/{id}` | 取回附件。图片 `inline` 显示，其余带 `Content-Disposition: attachment` 下载，永远不会当页面运行 |
| GET | `/tasks?status=a,b&project_id=&thread_id=&parent_id=` | 列出任务（优先级高的在前，同级按创建时间） |
| POST | `/tasks` | 创建 `{title, detail?, kind?, project_id?, persona_id?, priority?, agent_pref?, node_pref?, permission_mode?, parent_id?, depends_on?, thread_id?, dispatch?, place?, review?, on_fail?, base_branch?, base_from?}`，返回 201。决策 0022：`place`（`worktree` / `in_place` / `scratch`）、`review`（`none` / `model` / `agent` / `user`）、`on_fail`（`rework` / `ask`，`review` 不是 `none` 时需要）、`base_branch` + `base_from`（`local` / `remote`，`place: worktree` 时需要）都没有默认值：可以先不填，但派发前必须定下（否则 400）。`kind` 只是标签（`code` / `general` / `ops`），缺省时 `scratch` 是 `general`、其余是 `code`。`worktree` 和 `in_place` 必须指定项目；没有项目的任务归到内置项目 `misc`（杂项）。用户创建且不派发的进 `inbox` |
| GET | `/tasks/{id}` | `{task, runs, subtasks}` |
| PATCH | `/tasks/{id}` | 修改字段（只改出现的字段），`depends_on` 整体替换（成环时 400）；`dismissed: true` 表示用户看过了（从首页移除）；`status` 只能手动改成 `inbox`/`planned`/`blocked`/`done`/`cancelled`，执行中的任务要先取消（409） |
| POST | `/tasks/{id}/dispatch` | 派发或重试 `{"node_id"?, "agent"?}`（会记到任务上）。有空闲机器就开始，否则排队（`queued`）；缺少上面那些属性时 400；分支已经合并、丢弃或开了 PR 的任务 409 |
| POST | `/tasks/{id}/verdict` | 用户对等验收任务的结论 `{pass, reason?}`：通过就结束，不通过（要写 `reason`）就照着返工；任务不在等验收时 409 |
| POST | `/tasks/{id}/review` | 让做完的任务（再）验收一次 `{by: "model"\|"agent"\|"user"}` |
| POST | `/tasks/{id}/cancel` | 取消任务：停止它的 Run，并取消未完成的子任务 |
| GET | `/inbox` | 首页“需要你处理”：`{approvals, tasks}`，待审批的操作，以及受阻、失败、等机器、等你验收且没被 `dismissed` 的任务。分支没合并不算要处理：合并、推送由用户在对话里说了才做 |
| GET | `/runs?thread_id=&node_id=&status=a,b&limit=` | 列出 Run（按创建时间从旧到新） |
| POST | `/runs` | 派发 `{thread_id?, node_id?, agent, prompt, repo_path?}`；不填 `node_id` 时选第一台在线、装了该 agent 且没有显示未登录的机器。没有合适机器时返回 409 |
| GET | `/runs/{id}` | `{run, events, approvals}`：Run、它存储下来的全部事件、它的审批 |
| GET | `/runs/{id}/log` | 纯文本运行日志（agent 输出、工具调用、计划） |
| GET | `/runs/{id}/diff` | 纯文本 diff（相对基线提交） |
| POST | `/runs/{id}/message` | 追加指令 `{text}`，只能发给正在运行的 Run |
| POST | `/runs/{id}/cancel` | 取消：排队中的直接结束；运行中的通知机器停下；机器离线时直接标记为已取消 |
| POST | `/runs/{id}/pause` | 暂停：agent 停下当前这一步，Run 保留现场等待处理（它的待审批随之过期）。只能暂停运行中的 Run |
| POST | `/runs/{id}/resume` | 继续一个暂停的 Run，`{text?}`；带 `text` 等于发一条追加指令。给暂停的 Run 发 `/message` 也会让它继续 |
| GET | `/approvals?status=pending&run_id=` | 审批列表。`review` 为 `ai` 的正在由审批 agent 判断；`escalation` 是它转给用户时的理由 |
| POST | `/approvals/{id}/decide` | `{option_id, note?}`；`option_id` 必须是该审批提供的选项之一，为空表示取消。已处理或已过期时返回 409 |
| GET | `/nodes` | 机器列表（`online` 是实时状态）；`agents` 是机器上报的 AgentInfo 数组 |
| POST | `/nodes/pairing-code` | 生成配对码 `{code, expires_at}` |
| PATCH | `/nodes/{id}` | 改名 `{name}` |
| DELETE | `/nodes/{id}` | 删除机器：作废令牌并断开连接 |
| GET/POST | `/projects` | 列出、创建项目 `{name, repo_url?, default_agent?, default_mode?, default_persona?, notes?, layout?, nodes?: [{node_id, path, source?}], deploy?: {node_id, path, command, health?, branch?, auto_on_merge?}}`；`nodes` 的顺序就是路由优先级（服务端按顺序重写 `priority`）。重名或仓库（`remote_key`）已登记返回 409。部署目标必须是服务器。`deploy.branch` 是部署分支；`auto_on_merge` 表示部署分支（origin 上）有新提交时自动建运维任务部署（要有 `branch`，否则 400）。项目多了 `deployed_commit`：已经部署（或开启自动部署时记下的）提交 |
| PATCH/DELETE | `/projects/{id}` | 只改出现的字段（`deploy: null` 去掉部署目标）；还有未关闭任务的项目不能删除（409），内置的 `misc` 不能删除 |
| POST | `/projects/{id}/archive`、`/projects/{id}/activate` | 归档项目或重新激活，返回项目。归档的项目 `archived_at > 0`，不出现在工作台上，主管平时也不管它；内置的 `misc` 和还有排队、执行中、等审批任务的项目不能归档（409）。在已归档的项目里建任务会让它自动激活。`/ws` 推送 `project.archived` `{project_id, archived}` |
| POST | `/projects/new` | 新建项目 `{name, node_id?, layout?: "wokspace"\|"plain", owner?, private?, thread_id?}`：登记项目，并在一台开发机（不填就选第一台在线、设置了工作空间、wokspace 时还要装了 wok 的）的第一个工作空间里创建仓库。返回 201 `{project, run}`；完成后项目自动多一个位置，结果以汇报发到 `thread_id`（缺省主线）。`name` 必须是 kebab-case |
| POST | `/projects/{id}/clone` | `{node_id?, thread_id?}`：把项目 clone 到一台开发机的第一个工作空间，返回 201 `{run}` |
| GET | `/repos` | 机器在工作空间里找到的仓库：`{repos: [{node_id, path, remote, remote_key, branch, dirty, wokspace, seen_at, project_id}]}`，`project_id` 为空表示还没登记 |
| GET/POST | `/personas` | 岗位（见 decisions/0013）：列出 `{personas}`；创建 `{name, glyph?, color?, agent?, options?: {选项 id: 值}, brief?, default_mode?, kinds?: ["code"\|"general"\|"ops"]}`，返回 201 |
| PATCH/DELETE | `/personas/{id}` | 修改（整体替换）/ 删除；还有未关闭任务用它时 409 |
| GET | `/overview` | 工作台总览一次需要的数据：`{projects, nodes, personas, tasks, runs, approvals, discovered}`。`tasks` 是未关闭的任务加上 24 小时内完成的，`runs` 是排队和执行中的 Run，`approvals` 是待审批的，`discovered` 是还没登记的仓库。`projects` 包含已归档的项目（`archived_at > 0`），由前端分开显示。项目的每个位置（`nodes[]`）带 `git?: {head, upstream, ahead, behind, changes, untracked, fetched_at}`：机器上次扫描时 checkout 相对上游的状态 |
| PUT | `/nodes/{id}/role` | `{role: "dev"\|"server"}`：只能把开发机改成服务器；服务器改回开发机只能在那台机器的 node.json 里改（403 `disabled`） |
| PUT | `/nodes/{id}/workspaces` | `{workspaces: ["~/Code"]}`：替换机器的工作空间（绝对路径或 `~/` 开头，目录必须存在），立即扫描，返回 `{workspaces, repos}` |
| POST | `/nodes/{id}/scan` | 立即扫描工作空间，返回 `{workspaces, repos}` |
| GET | `/memories?q=&kind=&limit=` | 有 `q` 时按相关度搜索，否则按更新时间列出（可按 `kind` 过滤）；返回 `{memories, kinds}` |
| POST | `/memories` | 新建 `{kind, content}`，返回 201 和记忆 |
| PUT/DELETE | `/memories/{id}` | 修改 `{kind, content}` / 删除 |
| GET/POST | `/schedules` | 列出 `{schedules}`；创建 `{title?, cron, prompt, tz?, thread_id?, enabled?}`，返回 201。`cron` 是 5 个字段（分 时 日 月 周，也接受 `@daily` 等），`tz` 是 IANA 时区（缺省用免打扰设置里记下的设备时区，再缺省用 Hub 的时区），`thread_id` 缺省为主线 |
| PATCH/DELETE | `/schedules/{id}` | 修改（只改出现的字段）/ 删除 |
| POST | `/schedules/{id}/run` | 立刻触发一次（不影响下次的时间） |
| GET | `/findings?run_id=&limit=` | 巡检发现的问题，新的在前 |
| GET | `/usage?days=&tz=` | 最近 N 天（默认 30，按 `tz` 的自然日算）的 token 用量：`{days, since, usage, daily, by_project}`。`usage` 按 provider、model、role 汇总；`daily` 是 `[{day: "YYYY-MM-DD", role, calls, input_tokens, output_tokens, cached_tokens}]`；`by_project` 是 `[{project_id, name, calls, input_tokens, output_tokens, cached_tokens}]`（任务的调用算在任务的项目上，项目对话里的算在那个项目上，`project_id` 为空是主线对话和其他） |
| GET/PATCH | `/settings` | `{default_mode, default_agent, notify: {kind: bool}, quiet: {enabled, start, end, tz}, roles, push_devices}`；`roles` 只读（每个角色当前的“供应商/模型 · 思考强度”，在 `/models` 里改），不返回任何密钥。PATCH 只改出现的字段 |
| POST/DELETE | `/push/subscriptions` | 浏览器的 `PushSubscription.toJSON()`（`{endpoint, keys: {p256dh, auth}}`）；删除时 `{endpoint}` |
| GET | `/push/vapid-public-key` | `{key}`：base64url 的 VAPID 公钥（`applicationServerKey`） |
| POST | `/push/test` | 给所有已订阅的设备发一条测试通知，返回 `{devices, sent, errors}` |
| GET | `/updates?refresh=1` | `{hub: {version, update_available?}, latest?: {tag, name, url, published_at, notes}, error?, checked_at, repo, nodes: [{id, name, version, online, os, arch, update_available}]}`。最新发布从 GitHub Releases 读取，缓存 1 小时，`refresh=1` 强制刷新 |
| POST | `/nodes/{id}/update` | 让机器安装 `{version?}`（缺省为最新发布）并重启，返回 202 `{version}`；机器离线时 409。结果以主线里的提醒告诉用户 |
| POST | `/nodes/{id}/agents/detect` | 立刻重新检测，返回 `{agents: [AgentInfo]}` |
| POST | `/nodes/{id}/agents/{agent}/probe` | 启动 agent、开一个会话读出它的选项后关掉（不调用模型，最长约 2 分钟），返回 `{options: [ConfigOption], at}` |
| POST | `/nodes/{id}/agents/{agent}/install` | `{upgrade?, cols?, rows?}`：在伪终端里运行内置的安装（`upgrade` 时是升级）命令，返回 `{job_id, kind, agent, command}` |
| POST | `/nodes/{id}/agents/{agent}/login` / `logout` | `{cols?, rows?}`：运行内置的登录 / 退出登录命令，返回同上 |
| PUT | `/nodes/{id}/agents/{agent}/defaults` | `{options: {<选项 id>: <值>}}`：这台机器上这个 agent 每个新会话的默认选项；空对象表示全部跟随 agent |
| PUT | `/nodes/{id}/agents/{agent}/env` | `{name, value}`：设置 API Key 环境变量（只能是 `key_env` 里的名字），`value` 为空表示清除 |
| GET | `/nodes/{id}/jobs/{job}` | `{job_id, kind, agent, command, offset, data, running, exit_code?}`：作业最后 64 KB 的终端输出（`offset` 是 `data` 开头的字节位置） |
| POST | `/nodes/{id}/jobs/{job}/input` / `resize` / `cancel` | `{data}` 写入终端；`{cols, rows}` 改终端大小；结束作业。终端（`kind: shell`）只有打开它的登录会话能用，别的会话一律 404 |
| GET | `/models` | `{providers: [{id, name, kind, base_url, api_key_env, models, key_set, env_set, default_url, created_at, updated_at}], roles: {角色: {provider_id, model, effort?, context_window?, inherited}}, role_order, kinds, efforts}`。角色是 `supervisor`、`reviewer`、`approver`、`inspector`、`compactor`（整理对话的摘要）；`context_window` 是模型的上下文窗口（token，没有时按 128000）；没有单独选的角色 `inherited: true`，内容是主管的。从不返回 API Key，只有 `key_set`（Hub 上存了）和 `env_set`（`api_key_env` 指的环境变量有值） |
| POST / PATCH | `/models/providers`、`/models/providers/{id}` | 添加、修改供应商 `{name, kind, base_url?, api_key?, api_key_env?, clear_key?, models?}`（PATCH 只改出现的字段；`api_key` 留空不改，`clear_key: true` 删掉存的 Key）。`base_url` 必须是 https（本机 `http://localhost` 除外），不填用这个协议的官方地址。需要近期登录（401 `reauth`）：地址决定 Key 发到哪里。返回同 `GET /models` |
| DELETE | `/models/providers/{id}` | 删除供应商和它的 Key；还有角色在用时 409 `in_use`。需要近期登录 |
| POST | `/models/providers/{id}/discover` | 问供应商有哪些模型（OpenAI 兼容 `GET /models`，Anthropic `GET /v1/models`），返回 `{models}`，不保存 |
| POST | `/models/test` | `{provider_id, model, effort?}`：发一句短消息试试，返回 `{ok, reply?, error?, ms}` |
| PUT | `/models/roles` | `{角色: {provider_id, model, effort?, context_window?} 或 null}`：只改出现的角色；不带 `context_window` 时，模型没变就保留原来的窗口，`0` 清除（8000 到 10000000 之间才有效）；`null` 或空 `model` 让它跟主管一样（主管不能清空）。下一次调用模型就生效，不用重启。返回同 `GET /models`。改动后 `/ws` 推送 `models.changed`（`{what}`，不含密钥） |
| GET | `/mcp` | `{servers: [{id, name, slug, transport, command, args, url, enabled, pending, source, note, state, error?, tools: [{name, description, disabled, read_only, exposed?}], disabled_tools, env_keys: [{name, set}], header_names: [{name, set}], missing}], stdio_allowed}`。`transport` 是 `stdio`（在 Hub 上运行程序）或 `http`（远程，streamable HTTP）；`state` 是 `off` / `connecting` / `ready` / `error`；`pending: true` 是主管提议、用户还没批准的服务，它不会启动；`source` 是 `user` 或 `agent`。环境变量和请求头的值（可能是令牌）永远不返回，只有名字和有没有值（`env_keys`、`header_names`），`missing` 是还没填值的名字 |
| POST | `/mcp/servers` | `{name, transport, command?, args?, url?, env?, headers?, enabled?}`：添加服务并立刻连接。需要近期登录（401 `reauth`）。名字重复（按 `slug`）409，配置不合法 400（`stdio` 在 `mcp.stdio_disabled` 时拒绝；带 `headers` 时地址要 https，本机除外）。返回同 `GET /mcp` |
| PATCH | `/mcp/servers/{id}` | 只改出现的字段：`name`、`transport`、`command`、`args`、`url`、`env` / `headers`（设置；已有的名字值为空则保留原值）、`remove_env` / `remove_headers`、`enabled`、`disabled_tools`（对主管隐藏的工具名）。改前几类（决定运行什么、令牌发到哪）需要近期登录；只改 `enabled` / `disabled_tools` 不需要，其中 `disabled_tools` 不会重连。待批准的服务不能 `enabled: true`（409）。返回同 `GET /mcp` |
| POST | `/mcp/servers/{id}/approve` | 批准主管提议的服务并启动它；还有没填值的变量或请求头时 409。需要近期登录。返回同 `GET /mcp` |
| POST | `/mcp/servers/{id}/reconnect` | 断开重连，重新读取工具列表（最多等 35 秒）。返回同 `GET /mcp`，结果看 `state` / `error` |
| DELETE | `/mcp/servers/{id}` | 删除服务（待批准的提议用它来拒绝），它的工具立刻从主管的工具列表里去掉 |
| POST | `/projects/{id}/deploy/check` | 立即检查部署分支：让项目所在的在线机器（和部署目标）`git fetch` 并重新扫描，再检查一次有没有要部署的新提交。返回 `{fetched, failures, deployed_commit}`；项目没有部署分支时 400 |
| POST | `/projects/{id}/locations/{node}/sync` | 把项目在那台机器上的 checkout 和上游对齐：先 fetch，能快进就快进；有未提交的改动（`dirty`）、和上游分叉（`diverged`）、没有跟踪上游（`no_upstream`）时什么都不改。返回 `{outcome, message, git}`，`outcome` 还有 `up_to_date`、`fast_forwarded`。记一条 `project.repo_op` 事件 |
| POST | `/projects/{id}/locations/{node}/wip` | 把那台机器上未提交（含新文件，遵守 .gitignore）和未推送的改动做成一个提交，推到 origin 的 `sous/wip/<机器>-<时间>`；本地的文件、暂存区和分支都不变。返回 `{outcome: pushed|nothing, message, branch?, commit?}`。记一条 `project.repo_op` 事件 |
| GET | `/projects/{id}/shells` | `{shells: [{node_id, job_id, root, command, started_at, running, exit_code?}]}`：当前登录会话在这个项目各台在线机器上开的终端 |
| POST | `/projects/{id}/shells` | `{node_id, cols?, rows?}`：在这个项目在那台机器上的目录里开一个终端，返回 `{node_id, job_id, command, root}`。需要近期登录（401 `reauth`）；项目在那台机器上没有目录 400；node.json 关闭了终端（`remote_shell: false`）或远程管理 403 `disabled`；一台机器最多 8 个 409 `busy`。之后用 `/nodes/{id}/jobs/{job}` 那几个接口读写。打开时记一条 `agent.job.started`（`kind: shell`，带 `path`、`project_id`），终端的输入输出不保存 |
| GET | `/nodes/{id}/files/{agent}/{file}` | `{agent, file, path, format: "json"\|"jsonc"\|"toml"\|"text", exists, content, sha256}`。文件只能是 `AgentInfo.configs` 里的；`agent` 为 `sous`、`file` 为 `node` 时是机器的 node.json，设备令牌和 API Key 显示成 `***` |
| PUT | `/nodes/{id}/files/{agent}/{file}` | `{content, base_sha256}`：`base_sha256` 是读到的 `sha256`（文件不存在时为空），不一致返回 409 `conflict`；格式不对 400。写入前把原文件存为 `<文件>.sous-bak`，返回新内容和 `backup` |
| GET | `/projects/{id}/tree?path=&node=` | 项目目录里一层文件：`{node_id, result: {rel, entries: [{name, dir, size}]}}`（文件夹在前，不含 `.git`）。`node` 省略时用项目第一台在线的机器；都不在线 409 `offline` |
| GET | `/projects/{id}/file?path=&node=` | `{node_id, result: {rel, size, binary, content, sha256, data?}}`；超过 1 MB 或二进制的文件不返回 `content`，其中 png / jpg / gif / webp / bmp / ico / avif 图片带 `data`（base64）供页面显示 |
| PUT | `/projects/{id}/file?node=` | `{path, content, base_sha256}`：`base_sha256` 是读到的 `sha256`（新文件为空），不一致返回 409 `conflict`；返回新文件内容 |

**远程管理 agent**（上面 `/nodes/{id}/agents/*`、`/jobs/*`、`/files/*` 这几组）：Hub 转成 `node.request` 发给机器，等它回答（一般 20 秒超时）。机器离线 409 `offline`，sous 版本太旧 409 `unsupported`，机器的 node.json 关闭了远程管理 403 `disabled`，超时 504 `timeout`。`GET /nodes` 的每台机器多一个实时字段 `admin`：`on`、`off`、`unsupported`，离线时没有。`AgentInfo` 多了这些字段：`install? upgrade? login? logout?`（sous 会运行的命令，没有就不能做）、`missing?`（缺少的命令，如 `npx`）、`key_env?`、`env_set?`（已设置的变量名，不含值）、`options?`（agent 上次报告的会话选项 `[{id, name, description?, category?: "model"|"thought_level"|"model_config"|…, current, values: [{value, name, description?, group?}]}]`）、`options_at?`、`defaults?`、`configs?: [{id, path, format}]`、`launch_override?`、`job?`（正在进行的作业）。`/ws` 推送 `agent.job.started`（`{node_id, job_id, kind, agent, command}`）、临时事件 `agent.job.output`（`{node_id, job_id, offset, data}`，seq 为 0）、`agent.job.ended`（`{node_id, job_id, kind, agent, exit_code, error?}`）、`node.config_changed`（`{node_id, agent, what: "defaults"|"env:<名字>"|"file:<id>"}`，不含内容）。

**机器代用接口**：已配对的机器用设备令牌（`Authorization: Bearer`）调用 `/api/v1/device/` 下的这几个接口，给 `sous chat` 用（见 `core-daemon-ipc.md`）：`GET/POST /device/threads`、`GET/POST /device/threads/{id}/messages`、`GET /device/ws`、`GET /device/approvals`、`POST /device/approvals/{id}/decide`。行为与同名接口相同，不需要 CSRF。设置、机器、任务等其他接口仍然只能用 Passkey 登录后访问。`/device/ws` 只推送对话相关的事件：`thread.*`、`message.*`、`approval.*`、`trigger.*`、`run.queued`、`run.started`、`run.ended`；终端和安装作业的输出、配置变化、文件、记忆等都不发给设备令牌。

另有 `GET /healthz`（不在 `/api/v1` 下，不需要登录）返回 `{status, version, commit, web?: {commit, dirty?, built_at}}`：Hub 和它托管的页面各自的构建提交（页面的来自 `build/web/build-info.json`，由 web 构建写入），两者不一致说明页面是旧构建。机器用的 `POST /api/v1/node/pair` 和 `GET /node` 见 `core-hub-node.md`。

**消息字段**：`{id, thread_id, role: "user"|"assistant", content, error?, reply_to?, kind?, source?, seq, created_at}`。`reply_to` 是一条回复所回答的最新用户消息的 seq；回复期间新到的消息，会在下一轮一起回答。`kind` 为空表示普通消息，`notice` 表示主管主动发出的提醒（总在主线），`silent` 表示主管判断这条消息不需要回复（`content` 是原因），`report` 是任务引擎发的任务汇报，`trigger` 是没人输入、但要主管处理的消息（`role` 为 `user`：定时任务到点，或者 sous 的自动跟进，内容以【定时任务…】或【跟进】开头），`inspector` 是巡检发现问题时在主线留下的说明，`consult` 是项目顾问的回答（原样显示；`source: {run_id, agent, project_id, project?, node_id?}` 说明是谁；`reply_to` 是它回答的那条用户消息，但它不算主管对这条消息的回复）。

**Run 和审批**：`/ws` 会推送 `run.queued`、`run.started`、`run.update`、`run.ended`、`approval.requested`、`approval.decided`，以及临时事件 `run.output`（seq 为 0，payload `{kind: "text"|"thought", text}`，不补发；完整文字在 `/runs/{id}/log` 里）。机器变化时推送 `node.*` 事件。

**任务**：`{id, parent_id, origin_thread_id, kind: "code"|"general"|"ops", title, detail, status, status_reason, priority, project_id, agent_pref, node_pref, permission_mode, persona_id, branch, base_branch, pr_url, review_note, next, dismissed, depends_on, created_at, updated_at, place, review, on_fail, base_from, base_commit, git_state, handoff?, deploy_commit?, last_agent?, last_node?}`。`last_agent` / `last_node` 是最近一个工作 Run 的 agent 和机器（任务行显示实际执行者）。部署任务（带 `deploy_commit`）的 agent 跑完后，项目设置了健康检查时 Hub 让 Node 跑一次（`next: health`），通过才算完成、才记 `project.deployed`，不通过就受阻；用户取消的部署不会被下一次扫描重新建出来。一个工作 Run 没做任何事（`outcome: no_action`），或者有操作被拒绝而没有留下改动，任务转为 `blocked`，并给主管发一条跟进；有操作被拒绝的一轮验收没通过时不自动返工。`next` 在排队时是下一个 Run 的用途，执行中是当前 Run 的用途（`work`、`review`），工作台据此画出任务所在的阶段。`status` 取值 `inbox planned queued running waiting_approval blocked review done failed cancelled`；`queued` 表示等依赖或等机器（原因在 `status_reason`）；`review` 时 `status_reason` 含“等你验收”表示等用户。任务做完就是 `done`：改动留在任务分支上（或就地），不会自动提交、合并、推送或开 PR（决策 0022）；`git_state` 记录之后发生了什么：`unmerged`、`merged`、`pr`、`discarded`、`restored`（就地任务被撤销）。`base_commit` 是改动的起点（分支开始的提交或就地任务的快照）。`handoff` 是从顾问讨论转来的任务带的会话 `{node_id, agent, session_id?, note?}`。`/ws` 推送 `task.created`、`task.updated`、`task.status`、`task.reviewed`、`task.git`（`{state, note?, pr_url?, run_id?}`）、`project.put`、`project.deleted`、`project.deployed`（`{project_id, commit, baseline?}`）、`consult.closed`（`{project_id, reason?}`）、`approval.escalated`、`notify.sent`。任务的结果由引擎以 `kind: "report"` 的助手消息发到发起它的线程；git 操作的结果也是一条 `report`，除非主管正在等它（那时主管自己用一句话说）。

**Run**（M3 起）：多了 `task_id, purpose: "work"|"rebase"|"deliver", permission_mode, base_branch, pr_url, conflict, title, force`。决策 0022 起 `purpose` 还可以是 `review`（审查岗位验收）、`git`（一次 git 操作，`git: {op, into?, base?, branch?, remote?, squash?}`，结束后 `git_result: {op, branch?, before?, after?, commits?, conflicts?, pr_url?, message}`）、`consult`（项目顾问，带 `project_id`、`session`、`resume_session`、`consult_closed`），`kind` 可以是 `in_place`，并多了 `base_from`、`base_commit`，以及 `outcome?`、`error_kind?`、`refused?`（见 `core-hub-node.md` 的 `run.ended`）；`rebase` 和 `deliver` 不再出现在新的 Run 上。`/ws` 多推送 `run.turn`（`{text, session_id?}`，顾问每一轮的回答）。M5 起 `kind` 可以是 `ops`，`purpose` 可以是 `clone`、`create`（新建或 clone 项目，`prompt` 是项目 id），并多了 `persona?`（岗位名）、`options?`（岗位的会话选项）、`setup?`（`{name, remote?, layout?, owner?, private?}`）。`run.update` 的 `kind` 为 `permission` 时，表示机器上的策略自己做的决定（`category`、`decision: allow|deny`）。

**定时、巡检和恢复相关的事件**：`schedule.put`、`schedule.deleted`；`trigger.fired`（payload `{kind: "cron"|"followup", key, schedule_id?, message_id, title, content}`，同时作为 `kind: trigger` 的消息出现在它的线程里）；`inspector.finding`（`{finding_id, rule: "repeat"|"stalled"|"long"|"tokens", severity, action: "notify"|"pause"|"stop"|"none", reason, done}`）；`run.paused`（`{reason}`）、`run.resumed`；`run.message`（`{message_id, text}`，追加指令）、`run.message.delivered`（`{message_ids}`，已经送进 agent 的会话）；`run.archived`；`node.update_requested`、`node.update_result`（`{node_id, version, error?}`）。Run 多了 `paused, pause_reason, archived, recovery_note, last_event`；`recovery_note` 非空表示它是中断后自动重新开始的。

**M5 的其他字段和事件**：机器多了 `role: "dev"|"server"`、`workspaces`、`wok`（wok 的版本，空表示没装）；项目多了 `remote_key`（规范化的仓库地址，项目的身份）、`default_persona`、`notes`、`layout: "wokspace"|""`、`deploy`、`archived_at`（归档时间，0 表示激活）、`touched_at`（项目或它的任务最后一次改动的时间，工作台卡片右下角的“上次修改”），`nodes[].source` 是 `scan`（在工作空间里自动找到）或 `manual`；消息多了 `context`。`/ws` 多推送 `persona.put`、`persona.deleted`、`node.repos`、`node.role`。

**推送**：通知点开后打开 PWA 的哈希路由：`/#approvals`、`/#task=<id>`、`/#thread=<id>`。M5 起工作台自己的路由是 `/#/`、`/#/projects`、`/#/project=<id>`、`/#/task=<id>`、`/#/inbox`，旧的三种链接仍然有效。

**记忆**：`{id, kind: "profile"|"project"|"lesson"|"note", content, created_at, updated_at}`。记忆变化时，`/ws` 推送 `memory.put` / `memory.deleted` 事件。

**上下文**：一条对话的上下文分成若干段。每段开头是用户画像记忆的快照和一份摘要，摘要代替 `covers_up_to` 及以前的消息；之后的消息原样发给模型。占用超过窗口的 60%（`soft`）时，回合结束后自动整理一次；超过 85%（`hard`）时先整理再回复。整理先让模型把值得记住的写进记忆，再生成新摘要，然后推送 `thread.compacted`（`{segment, covers_up_to, summary, profile, reason, input_tokens?, model?}`，`reason` 是 `start` / `soft` / `hard` / `overflow` / `manual` / `edit`）。消息本身不删不改，`/threads/{id}/messages` 照样能翻到全部历史。

## WebSocket `/ws`

- 连接地址：`/ws?since=<seq>`。服务端先补发 `seq > since` 的事件，然后实时推送新事件。
- 服务端发给客户端的帧：`{"seq": 123, "id": "...", "ts": 0, "type": "task.status", "thread_id"?, "task_id"?, "run_id"?, "payload": {...}}`。事件类型见 `work/specs/sous-mvp/design.md` 第 4 节。
- `seq` 为 0 的是临时事件（目前只有 `message.assistant.delta`，payload `{message_id, delta, thinking?}`），只实时推送，不补发。回复结束后会推送同一个 `message_id` 的 `message.assistant`。
- 写操作一律走 REST；客户端不需要发任何帧。服务端用 WebSocket 协议层的 ping 保活（每 30 秒）。
- 客户端跟不上推送时，服务端会以 1013（Try Again Later）关闭连接；客户端带上最后收到的 seq 重连即可补齐。
