# core-desktop-sandbox-dispatch：本机 Hub → 桌面待授权请求

Core 和 Desktop 在包内启用此协议，Web 只使用公有回执。它不是网络 Node 协议，也不用于远程 Hub。

## Web → Hub

`GET /api/v1/capabilities` 返回 `local_sandbox_dispatch: true` 才显示入口。仅在独立本机 Hub 运行 `--app-managed --desktop-sandbox-dispatch` 时启用。以下请求使用本机会话和同源检查，`POST` 需要 CSRF，且请求体只能是 `{}`。

| 方法 | 路径 | 成功响应 |
|---|---|---|
| GET | `/api/v1/tasks/{task_id}/sandbox-dispatch` | `200 {"dispatch": View 或 null}`，只读当前任务最近一条回执 |
| POST | `/api/v1/tasks/{task_id}/sandbox-dispatch` | `202 {"dispatch": View}`，同一任务非终态的请求去重 |
| POST | `/api/v1/sandbox-dispatch/{id}/cancel` | `200 {"dispatch": View}`，撤回 pending，或向已领取者请求停止 |

`View` 仅含 `id`、`task_id`、`status`、`changes`（变更项数，仅 `ready` 有意义）、`updated_at` 和 `expires_at`（Unix 毫秒；后者是待授权截止）。HTTP 不含认领和完成接口；不返回目录、命令、文件、报告、授权或租约。

## Hub ↔ Desktop 私有管道（v1）

桌面在包内具有受支持的执行器时启动 `sous hub --app-managed --desktop-sandbox-dispatch`。就绪行在 `origin` 和 `setup_url` 之外包含 `sandbox_dispatch: 1`；桌面拒绝版本不匹配。不监听 socket，不复用 HTTP 或 Relay。stdin 关闭后 Hub 退出。stdin/stdout 每行一个 UTF-8 JSON 对象，stderr 为日志；请求的正整数 `id` 对应同一 `id` 的回复。成功返回 `data`，失败返回 `error`（人可读文本）。以下为 `action` 所需的额外字段：

| 动作 | 字段 | `data` |
|---|---|---|
| `list` | 无 | 至多 48 条待确认项，包含 `View`、`title`、`detail`；文字只供阅读 |
| `claim` | `dispatch_id` | 32 字节随机租约（小写十六进制）；只在原生确认并消耗本地路径授权后调用 |
| `heartbeat` | `dispatch_id`, `lease` | `true` 要求停止；任何失联或失败也会停止本次 Guest。正常运行每 2 秒心跳，租期 30 秒 |
| `complete` | `dispatch_id`, `lease`, `outcome`, 可选 `changes` | `View`；终态为 `ready`、`failed`、`cancelled` 或 `interrupted`，相同终态与变更数可重试 |

只有待分配或待办、未处于归档项目、且无未完成依赖的任务可以请求。请求 24 小时未领取转 `expired`；待确认时任务版本、文字或可执行条件变化转 `stale`。已领取后任务变化与撤回会转 `cancel_requested`。每工作区同时一条租约；桌面确认清理后才报告 `cancelled`。若经核验的完成报告先于停止生效，则仍可以返回 `ready` 并保留本地报告。租约超期或 Hub 重启置 `interrupted`，旧租约不能修改终态，不自动重跑。`ready` 表示本机审阅材料已保存，不代表已写回原件或任务流转为完成。

Desktop 严格使用自己的系统选择器、授权、本机输入命令和独立写回确认。请求不会预选路径、填充或执行命令。运行器与本机审阅的独立契约见 [core-sandbox-cli](core-sandbox-cli.md)。
