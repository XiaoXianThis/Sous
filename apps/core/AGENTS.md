# core

- Purpose: sous 的 Go 核心。编译出两个二进制：`sous-hub`（服务端大脑，部署在 Linux 服务器）和 `sous`（客户端 daemon + 命令行，运行在 Windows / macOS / Linux）。
- Stack: Go（版本见 `go.mod`），SQLite（`modernc.org/sqlite`，纯 Go，无需 CGO），`coder/websocket`，`go-webauthn/webauthn`，`kardianos/service`（客户端的系统服务），`aymanbagabas/go-pty`（安装和登录用的伪终端，含 Windows ConPTY），`pelletier/go-toml/v2`（校验 TOML 配置文件）。
- Depends on: 运行时读取 `build/web/`（web 的构建产物）作为 PWA 静态文件。对外提供 `docs/api/core-hub-http.md`、`docs/api/core-hub-node.md`、`docs/api/core-daemon-ipc.md` 三份契约。

## Tasks

Commands run in `apps/core/`. Leave a command empty to use what `wok` detects; `wok ls core` shows the result.

| task | command | windows |
|---|---|---|
| install | go mod download | |
| dev | go run ./cmd/sous-hub -config configs/hub.dev.yaml | |
| test | go test ./... | |
| vet | go vet ./... | |
| fmt | gofmt -l -w . | |
| build | go build -trimpath -o "$WOK_OUT/" ./cmd/... | |
| release-snapshot | goreleaser release --snapshot --clean | |
| build-hub-linux | GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o "$WOK_OUT/linux-amd64/" ./cmd/sous-hub | set GOOS=linux&& set GOARCH=amd64&& set CGO_ENABLED=0&& go build -trimpath -o "$WOK_OUT/linux-amd64/" ./cmd/sous-hub |

## Conventions

- 结构：`cmd/sous-hub`（服务端）、`cmd/sous`（客户端：pair / daemon / status / agents / chat / update / service / unpair）是入口；业务代码都在 `internal/` 下：
  - Hub：event、store、eventlog、effects、provider、supervisor、prompts、tool、auth、httpapi、nodehub（机器、Run、审批、暂停、追加消息、归档、设备令牌接口）、task（任务引擎：状态、派发、验收、审批 agent、交付、中断后重试、主管的任务工具、任务和项目接口）、router（选机器和 agent，纯函数）、notify（Web Push、通知设置）、trigger（定时任务、跟进、数据保留）、inspector（巡检 agent）、hub（组装）。
  - 客户端：node（daemon、持久化的发件箱、Run 执行和恢复、暂停、交付、IPC 服务、自更新）、policy（权限分类、模式表、底线规则）、agents（agent 注册表和检测）、acp（ACP 客户端）、workspace（worktree、diff、commit / push / PR）、ipc（本地 JSON-RPC 的协议和客户端）。
  - 两边共用：proto（Hub ↔ Node 帧类型）、project（项目身份和名字规则）、config、ids、version、update（GitHub Releases：检查、下载、校验、替换）、watchdog（进程内存活检测）。
- 测试里需要真实的 agent 进程时，用 `internal/acp/acptest` 的假 agent：在测试包的 `TestMain` 里调用 `acptest.RunIfFake()`，再用 `acptest.Command(mode, dir)` 启动（模式：`write` 请求执行命令、`edit` 请求改文件、`floor` 请求 `rm -rf ~`、`slow`、`crash`、`auth`、`nologin`、`loop` 反复调用同一个工具；环境变量 `FAKE_ACP_LOAD=1` 让它支持 `session/load`，`FAKE_ACP_PROMPTS=<文件>` 把收到的每个 prompt 追加到文件里）。`internal/node/recovery_e2e_test.go` 覆盖巡检暂停、暂停后继续、daemon 重启后恢复会话、中断后带恢复说明重试、`sous chat` 的 IPC。`internal/node/e2e_test.go` 是 Hub + daemon + 假 agent 的端到端测试，`internal/node/task_e2e_test.go` 加上任务引擎、effects、裸仓库 origin 和假 `gh`，跑完整的任务闭环；`internal/node/workbench_e2e_test.go` 覆盖工作空间扫描、新建（假 `wok`）和 clone 项目、服务器上的运维任务和岗位选项、PR 合并后自动部署。改动 nodehub、node、acp、proto、task、policy、workspace 之后要跑。
- 任务的推进都走 effects 队列（`task.advance`、`approve`、`push`，在投影里和事件同一个事务入队），处理函数要幂等：先重新读任务，已经往前走了就什么都不做。
- 本地试完整闭环、不用真实 agent：`go test -c -o <dir>/fakeagent ./internal/node` 得到假 agent，在 `node.json` 里把一个没有登录检测的 agent（如 `opencode`）覆盖成 `["env", "SOUS_FAKE_ACP_AGENT=1", "FAKE_ACP_MODE=write", "<dir>/fakeagent", "-test.run=^$"]`；仓库加一个裸仓库做 origin，PATH 前面放一个假 `gh` 脚本（参考 `task_e2e_test.go`），daemon 用同样的 PATH 启动。
- 远程管理 agent（安装、登录、默认模型和思考强度、API Key、编辑配置文件，见 `docs/decisions/0012`）：Hub 侧在 `nodehub/admin.go`（`node.request` 请求 / 应答和 HTTP 接口），Node 侧在 `node/admin.go`、`node/job.go`（伪终端作业，`aymanbagabas/go-pty`）、`node/files.go`（配置文件读写）。要运行的命令和能编辑的文件只能写在 `internal/agents` 的 `Install`、`Upgrade`、`Login`、`Logout`、`Configs`、`KeyEnv` 里，不要让请求带命令或路径。改这些内置命令前先在真实机器上试。
- `node.json` 的 `agent_options`（每个新会话的默认选项）、`agent_env`（API Key）、`remote_admin`（`false` 关闭远程管理，只能本地改）。改 node.json 后 `Daemon.apply` 立即生效，只有并发数要重启。
- Hub 把 `inventory` 解码成 `proto.AgentInfo` 再存，所以给 `AgentInfo` 加字段时 Hub 也要一起更新，否则新字段会被丢掉。
- 决定 agent 会不会请求权限的会话选项（Kiro 的 `autopilot`）写在 `internal/agents` 的 `AskOptions` / `OpenOptions` 里，由 sous 按权限模式设置，用户不能改。Kiro 还会按 `~/.kiro/settings/permissions.yaml` 自己放行操作。
- `--home` / `SOUS_HOME` 会转成绝对路径：agent 要求会话的工作目录是绝对路径。
- 假 agent 加 `FAKE_ACP_OPTIONS=1` 时报告 `configOptions`（model、effort）并接受 `session/set_config_option`，设置记录在 `FAKE_ACP_PROMPTS` 里（`set <id>=<值>`）。`internal/node/admin_e2e_test.go` 覆盖读取选项、默认值套用、API Key、配置文件、伪终端作业和关闭远程管理。
- `node.json` 的 `policy` 字段配置底线规则：`floor_disabled`、`floor`（替换默认规则）、`extra_floor`（追加），都是 Go 正则。
- 唯一写入者：领域数据只能通过 `eventlog.Log` 写入（追加事件 + 投影视图 + 入队副作用，一个事务）。不要绕过它直接写 `events` 或视图表。
- 事件表只追加。`migrations` 只能在末尾追加新版本；已发布的迁移不能修改。
- 模型提供方实现 `provider.Provider` 并在 `init()` 里 `provider.Register`。新增 kind 时补上用 `httptest` 模拟 SSE 的测试。
- 本地开发：`configs/hub.dev.yaml` 默认用 OpenCode Go 订阅的 `deepseek-v4.1-flash`（`OPENCODE_API_KEY`），密钥从 `apps/core/.env` 读取（从 `.env.example` 复制，已被 git 忽略，绝不提交）。没有密钥时，把 `roles.supervisor` 改成 `mock/echo` 即可离线使用。`dev: true` 时开放仅限本机回环地址的 `POST /api/v1/auth/dev-login`，生产配置不要开启。
- 数据目录 `.data/` 和 `bin/` 已被忽略，不要提交。
- 运维命令：`sous-hub admin passkey-reset [--revoke-all]`、`sous-hub admin backup <file>`。
- 本地试客户端：用 `--home` 指定一个临时目录，避免碰到 `~/.sous`。例如 `go run ./cmd/sous pair http://localhost:8787 <配对码> --home /tmp/sous-node`，然后 `go run ./cmd/sous daemon --home /tmp/sous-node`。在 `node.json` 的 `agents` 字段里可以覆盖某个 agent 的启动命令。
- opencode 的权限靠 `internal/agents` 里的 `OPENCODE_CONFIG_CONTENT` 设成 `ask`，否则它不经 sous 的策略直接执行。在 `node.json` 里覆盖 opencode 的启动命令时，不要再设 `OPENCODE_CONFIG_CONTENT`（会把这些规则覆盖掉）；选模型用 `["env", "OPENCODE_CONFIG=<含 model 的 json 文件>", "opencode", "acp"]`。
- 发布：打 `v*` tag 触发 `.github/workflows/release.yml`，用 `.goreleaser.yaml` 构建并发布到 GitHub Releases；压缩包名必须是 `<二进制>_<版本>_<系统>_<架构>`，`internal/update` 按这个名字找安装包。本地先 `wok build web`，再 `wok run core release-snapshot` 试构建（产物在 `dist/`，已忽略）。
- 本地试更新：让 daemon 带上 `SOUS_UPDATE_API=<假的 GitHub API 地址>`，服务端按 `/repos/<owner>/<name>/releases/{latest,tags/<tag>}` 返回 release JSON，资产指向 `dist/` 里的文件。
- daemon 的本地接口在 `<home>/daemon.sock`：同一个 home 只能跑一个 daemon。`sous chat --home <同一个目录>` 连它。
- ACP 的通知在读循环里同步处理（保证顺序），通知回调不能阻塞；会阻塞的只有请求（权限）。
- agent 的会话模式名各家不同：`internal/agents` 的 `AskMode` / `OpenMode` 指定要切到哪个模式（Cursor 是 `agent`，它的 `ask` 是只读问答）；不指定时 `node.pickMode` 按常见的名字猜。
- M5（`work/specs/m5-workbench`，`docs/architecture/workbench.md`）：
  - 项目的身份是 `internal/project.Key(远端地址)`（纯函数，Hub 和 Node 共用）；内置项目 `misc`（`project.MiscID`）由 `task.Engine.Seed` 在启动时补上，内置岗位只在第一次启动时创建（`settings.personas_seeded`）。都走事件，不在迁移里插数据。
  - `project_nodes.source = scan` 的行由 `node.repos` 投影自动维护（`store/workbench.go` 的 `linkScans`），`project.put` 会重新整理；手填的（`manual`）不受扫描影响。扫描用 `git config --get remote.<名>.url`，不经过 insteadOf 改写。
  - 岗位：`task.personaOf` 取任务的岗位，否则项目默认岗位；agent 顺序是任务 > 岗位 > 项目 > 全局；岗位说明拼在提示最前面，会话选项随 `run.start.options` 下发，在 `admin.applyDefaults` 里优先于 node.json 的默认值、不覆盖强制选项。
  - 机器角色在 node.json 的 `role`，策略用 `policy.DecideFor(role, mode, cat)`；`policy.EffectiveMode` 把服务器上的 `open` 降为 `default`。新增 `service` 类别。
  - 运维任务（`kind: ops`）：router 只派到服务器（`Request.Role`），部署目标的目录通过 `Request.Path` + `NodePref` 指定；Node 用 `workspace.PrepareInPlace`，不建 worktree、不传 diff；引擎成功后直接完成，不验收。worktree 清理跳过 ops Run。
  - 新建 / clone 项目是 `purpose: create|clone` 的 Run（agent 为 `sous`，`prompt` 是项目 id），在 `node/workbench.go` 里执行写死的命令并逐条过策略；结束后 effects 队列的 `project.setup` 由 `task.Engine.Setup` 给项目加上位置并汇报。
  - 合并后自动部署：`task.Engine.CheckMerges`（启动 30 秒后一次，之后每 5 分钟）用 `node.request gh.pr_state` 查 PR，按 `settings` 的 `deploy:<任务 id>` 只触发一次。
  - 任务执行中 `next` 记着当前 Run 的用途（`keepNext`），前端据此画阶段。
  - `mock/verdict` 模型回固定的 JSON 结论（验收通过、审批交给用户），离线跑通完整闭环时把 reviewer、approver、inspector 设成它。
- 不要把 agent 的 ACP 适配器版本写成 `latest`：`internal/agents` 里固定了版本号，升级时要实测后再改。
