# core-sandbox-cli — 桌面本机沙盒模块

- Provider：core 的 `sous-sandbox`，仅 Apple Silicon / macOS 实现真实执行。
- Consumer：桌面原生沙盒窗口。以桌面进程启动并监管独立子进程，不调用 Hub 或旧 daemon。也可由本机用户直接使用 CLI。
- stdin/stdout/stderr 是子进程管道，不是网络端点。命令、源文件绝对路径、目录身份、产物路径和内容不通过 Hub、浏览器或中继。

## 执行与撤权

1. 桌面选择器确定源路径后，调用 `sous-sandbox identify ABS_SOURCE`，stdout 是一个 JSON 字符串，代表初始文件/目录身份。桌面只在内存中保留路径、身份和 10 分钟时限。
2. 用户确认本次运行后，桌面消费该授权并调用 `sous-sandbox --parent-managed --source ABS_SOURCE --source-identity ID --runtime-home ABS_VM_HOME --output ABS_OUT --msb ABS_PROGRAM --firmware ABS_FW [--offline | --image-archive ABS_OCI] -- GUEST_COMMAND [ARGS...]`。Core 重新核对授权对象，核验运行时摘要，在无网络 Guest 内执行，只在 Guest 销毁确认后返回结果。`--image-archive` 隐含离线。
3. 桌面持有 `--parent-managed` 的 stdin 管道；EOF 取消本次处理，尝试销毁 Guest。`--recover --parent-managed --runtime-home … --msb … --firmware …` 只对账本机未清理实例，不接收项目或命令。
4. stderr 是阶段和错误的纯文本，不作为命令解释。成功 stdout 为一行 `{ "directory": "…", "exit_code": 0, "imported_files": 1, "excluded_entries": 0 }`。Guest 命令非零退出时，若 Guest 已回收且报告完整，可以同时返回非零进程退出码和可审阅但不可写回的结果。无报告或销毁未确认时不返回成功。

## 审阅和写回

以下命令的 `ABS_RESULT` 必须是本机的私有 `sous-result-*` 目录。`REV` 是 `review` 返回的 SHA-256 版本，`REL` 必须与清单中的一条相对路径完全一致。JSON 输出只供可信本机调用方读取。

| 命令 | stdout |
|---|---|
| `review ABS_RESULT` | 清单（`version`, `run_id`, `exit_code`, `imported_files`, `excluded_entries`, `changes`）及 `revision`、`can_apply`。不返回目标宿主路径。 |
| `preview ABS_RESULT REV REL` | 对应变更的 `path`、`operation`、前后摘要、`revision`、可选 `before_text` / `after_text`、`binary`、`truncated`。最多每侧 64 KiB 可见文本；不会解释 HTML。 |
| `logs ABS_RESULT REV` | JSON 字符串。读取当前结果的有界日志；替换不安全控制字符。 |
| `apply ABS_RESULT REV REL` | 需要单项本机用户确认。核对源绑定、目标与基线。stdout 为 `path`、`operation`、可选 `local_backup`；出现交换后的并发冲突也可能返回带恢复位置的 JSON，但进程返回非零。无任意写入目的路径参数。 |

桌面只接受自己的原生沙盒窗口从 `wails://localhost/sandbox/index.html` 主 frame 发来的窄消息，不绑定对外可发现的执行服务。源对象、运行时文件或审阅目录通过系统选择器授权，而不是由消息参数传入。`run` 与 `apply` 另经系统对话框确认。
