# core

- Purpose: sous 的 Go 核心。编译出两个二进制：`sous-hub`（服务端大脑，部署在 Linux 服务器）和 `sous`（客户端 daemon + 命令行，运行在 Windows / macOS / Linux）。
- Stack: Go（版本见 `go.mod`），SQLite（`modernc.org/sqlite`，纯 Go，无需 CGO），`coder/websocket`，`go-webauthn/webauthn`，`kardianos/service`（客户端的系统服务）。
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
| build-hub-linux | GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o "$WOK_OUT/linux-amd64/" ./cmd/sous-hub | set GOOS=linux&& set GOARCH=amd64&& set CGO_ENABLED=0&& go build -trimpath -o "$WOK_OUT/linux-amd64/" ./cmd/sous-hub |

## Conventions

- 结构：`cmd/sous-hub`（服务端）、`cmd/sous`（客户端：pair / daemon / status / agents / service / unpair）是入口；业务代码都在 `internal/` 下：
  - Hub：event、store、eventlog、effects、provider、supervisor、prompts、tool、auth、httpapi、nodehub（机器、Run、审批）、task（任务引擎：状态、派发、验收、审批 agent、交付、主管的任务工具、任务和项目接口）、router（选机器和 agent，纯函数）、notify（Web Push、通知设置）、hub（组装）。
  - 客户端：node（daemon、发件箱、Run 执行、交付）、policy（权限分类、模式表、底线规则）、agents（agent 注册表和检测）、acp（ACP 客户端）、workspace（worktree、diff、commit / push / PR）。
  - 两边共用：proto（Hub ↔ Node 帧类型）、config、ids、version。
- 测试里需要真实的 agent 进程时，用 `internal/acp/acptest` 的假 agent：在测试包的 `TestMain` 里调用 `acptest.RunIfFake()`，再用 `acptest.Command(mode, dir)` 启动（模式：`write` 请求执行命令、`edit` 请求改文件、`floor` 请求 `rm -rf ~`、`slow`、`crash`、`auth`、`nologin`）。`internal/node/e2e_test.go` 是 Hub + daemon + 假 agent 的端到端测试，`internal/node/task_e2e_test.go` 加上任务引擎、effects、裸仓库 origin 和假 `gh`，跑完整的任务闭环。改动 nodehub、node、acp、proto、task、policy、workspace 之后要跑。
- 任务的推进都走 effects 队列（`task.advance`、`approve`、`push`，在投影里和事件同一个事务入队），处理函数要幂等：先重新读任务，已经往前走了就什么都不做。
- 本地试完整闭环、不用真实 agent：`go test -c -o <dir>/fakeagent ./internal/node` 得到假 agent，在 `node.json` 里把一个没有登录检测的 agent（如 `opencode`）覆盖成 `["env", "SOUS_FAKE_ACP_AGENT=1", "FAKE_ACP_MODE=write", "<dir>/fakeagent", "-test.run=^$"]`；仓库加一个裸仓库做 origin，PATH 前面放一个假 `gh` 脚本（参考 `task_e2e_test.go`），daemon 用同样的 PATH 启动。
- `node.json` 的 `policy` 字段配置底线规则：`floor_disabled`、`floor`（替换默认规则）、`extra_floor`（追加），都是 Go 正则。
- 唯一写入者：领域数据只能通过 `eventlog.Log` 写入（追加事件 + 投影视图 + 入队副作用，一个事务）。不要绕过它直接写 `events` 或视图表。
- 事件表只追加。`migrations` 只能在末尾追加新版本；已发布的迁移不能修改。
- 模型提供方实现 `provider.Provider` 并在 `init()` 里 `provider.Register`。新增 kind 时补上用 `httptest` 模拟 SSE 的测试。
- 本地开发：`configs/hub.dev.yaml` 默认用 DeepSeek，密钥从 `apps/core/.env` 读取（从 `.env.example` 复制，已被 git 忽略，绝不提交）。没有密钥时，把 `roles.supervisor` 改成 `mock/echo` 即可离线使用。`dev: true` 时开放仅限本机回环地址的 `POST /api/v1/auth/dev-login`，生产配置不要开启。
- 数据目录 `.data/` 和 `bin/` 已被忽略，不要提交。
- 运维命令：`sous-hub admin passkey-reset [--revoke-all]`、`sous-hub admin backup <file>`。
- 本地试客户端：用 `--home` 指定一个临时目录，避免碰到 `~/.sous`。例如 `go run ./cmd/sous pair http://localhost:8787 <配对码> --home /tmp/sous-node`，然后 `go run ./cmd/sous daemon --home /tmp/sous-node`。在 `node.json` 的 `agents` 字段里可以覆盖某个 agent 的启动命令。
- 不要把 agent 的 ACP 适配器版本写成 `latest`：`internal/agents` 里固定了版本号，升级时要实测后再改。
