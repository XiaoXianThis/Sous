# core

- Purpose: sous 的 Go 核心。编译出两个二进制：`sous-hub`（服务端大脑，部署在 Linux 服务器）和 `sous`（客户端 daemon + 命令行，运行在 Windows / macOS / Linux）。
- Stack: Go（版本见 `go.mod`），SQLite（`modernc.org/sqlite`，纯 Go，无需 CGO），`coder/websocket`，`go-webauthn/webauthn`。
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

## Conventions

- 结构：`cmd/sous-hub`、`cmd/sous` 是入口；业务代码都在 `internal/` 下，按设计文档的包划分（event、store、eventlog、effects、provider、supervisor、tool、auth、httpapi、hub……）。
- 唯一写入者：领域数据只能通过 `eventlog.Log` 写入（追加事件 + 投影视图 + 入队副作用，一个事务）。不要绕过它直接写 `events` 或视图表。
- 事件表只追加。`migrations` 只能在末尾追加新版本；已发布的迁移不能修改。
- 模型提供方实现 `provider.Provider` 并在 `init()` 里 `provider.Register`。新增 kind 时补上用 `httptest` 模拟 SSE 的测试。
- 本地开发：`configs/hub.dev.yaml` 默认用 DeepSeek，密钥从 `apps/core/.env` 读取（从 `.env.example` 复制，已被 git 忽略，绝不提交）。没有密钥时，把 `roles.supervisor` 改成 `mock/echo` 即可离线使用。`dev: true` 时开放仅限本机回环地址的 `POST /api/v1/auth/dev-login`，生产配置不要开启。
- 数据目录 `.data/` 和 `bin/` 已被忽略，不要提交。
- 运维命令：`sous-hub admin passkey-reset [--revoke-all]`、`sous-hub admin backup <file>`。
