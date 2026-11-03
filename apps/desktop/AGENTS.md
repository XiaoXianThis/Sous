# desktop

- Purpose: sous 的桌面壳：托盘图标、窗口（内嵌 web 的聊天界面）、本机 daemon 状态与手动更新。
- Stack: Go + Wails v3
- Depends on: `build/web/`（web 的构建产物）；core 的 `docs/api/core-daemon-ipc.md` 与 `docs/api/core-hub-http.md` 契约。

## Tasks

Commands run in `apps/desktop/`. Leave a command empty to use what `wok` detects; `wok ls desktop` shows the result.

| task | command | windows |
|---|---|---|
| install | | |
| dev | | |
| test | | |
| build | | |

## Conventions

- 第二阶段开工（MVP 先用 PWA + `sous` 命令行）。
- 不引用 core 的源码；与 daemon 只通过本地 IPC 契约通信。
