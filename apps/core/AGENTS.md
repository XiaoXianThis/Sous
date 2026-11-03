# core

- Purpose: sous 的 Go 核心。编译出两个二进制：`sous-hub`（服务端大脑，部署在 Linux 服务器）和 `sous`（客户端 daemon + 命令行，运行在 Windows / macOS / Linux）。
- Stack: Go，SQLite（Hub 存储）
- Depends on: none。对外提供 `docs/api/core-hub-http.md`、`docs/api/core-hub-node.md`、`docs/api/core-daemon-ipc.md` 三份契约。

## Tasks

Commands run in `apps/core/`. Leave a command empty to use what `wok` detects; `wok ls core` shows the result.

| task | command | windows |
|---|---|---|
| install | | |
| dev | | |
| test | | |
| build | | |

## Conventions

- 尚未初始化 Go module，待 `work/specs/sous-mvp/` 评审通过后开工。
