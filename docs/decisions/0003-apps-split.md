# 0003 拆成 core、web、desktop 三个 app

- Status: Accepted
- Date: 2026-11-03

## Context

客户端要在 Windows、macOS、Linux 上运行：有常驻的 daemon，之后还有窗口和托盘图标，并且客户端里也能聊天。wokspace 规范禁止 app 之间引用源码，只允许消费 `build/<app>/` 产物或者通过 `docs/api/` 的契约通信。

## Decision

- `apps/core`：一个 Go module，编译出两个二进制。`sous-hub` 是服务端；`sous` 是客户端，同时负责 daemon 和命令行（`sous daemon`、`sous pair`、`sous status`、`sous chat`）。Hub 与 Node 的协议类型在同一个 module 里共用。
- `apps/web`：React PWA，构建到 `build/web/`。
- `apps/desktop`：Go + Wails v3，提供托盘和窗口。窗口内嵌 `build/web/`，与本机 daemon 通过本地 IPC 契约通信。
- daemon 和桌面界面是两个独立进程：daemon 作为系统服务常驻，界面退出或崩溃都不影响正在执行的任务；没有图形界面的 Linux 机器只装 `sous`。

## Alternatives

- 拆成 hub、node 两个 app：协议类型只能靠契约生成代码来共享，增加同步成本。
- daemon 和界面做成同一个进程：关掉窗口就会中断任务，也没法在无图形界面的服务器上运行。
- desktop 用 Electron 或 Tauri：一个体积大，一个要引入 Rust；Wails 和主技术栈都是 Go。

## Consequences

- 聊天界面只写一份，网页、手机、桌面共用。
- desktop 依赖 web 的构建产物，构建顺序是先 web 后 desktop。
