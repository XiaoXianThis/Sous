# 接入一台电脑

让 sous 能把编程任务派到你的电脑上。Windows、macOS、Linux 都支持。

## 1. 准备

- 在这台电脑上安装、并**登录**你要让 sous 使用的编程 agent。sous 用它们自己的账号干活：

  | agent | 安装后登录 |
  |---|---|
  | Claude Code | 运行 `claude`，在里面执行 `/login` |
  | Codex | `codex login` |
  | Cursor | `cursor-agent login` |
  | OpenCode | `opencode auth login` |
  | Kiro | 按 Kiro CLI 的说明登录（sous 对 Kiro 的支持尚未实测） |

- Claude Code 和 Codex 是通过 ACP 适配器运行的，需要先装好 Node.js（包含 `npx`）。

## 2. 安装 sous 客户端

在仓库根目录构建（每个平台用对应的产物）：

```
wok build core
```

产物在 `build/core/` 下，把其中的 `sous`（Windows 上是 `sous.exe`）放进 `PATH`。

## 3. 配对

1. 在 sous 网页上打开侧边栏的 **机器** → **配对新机器**，会显示一条命令，10 分钟内有效。
2. 在这台电脑上执行这条命令，例如：

   ```
   sous pair https://agent.wxian.cc 123456 --name 我的笔记本
   ```

3. 检查 agent 是否可用：

   ```
   sous agents
   ```

   “登录”一栏显示“未登录”的 agent，sous 不会把任务派给它。

## 4. 让它一直运行

先在前台试一下，网页的机器面板里应该显示“在线”：

```
sous daemon
```

没问题之后，在 macOS 和 Linux 上注册成随登录启动的服务（macOS 是 LaunchAgent，Linux 是 systemd 用户服务）：

```
sous service install
sous service start
sous status
```

**Windows**：`sous service install` 会注册成 Windows 服务，但它以 SYSTEM 账户运行，读不到你账户下各个 agent 的登录信息，所以暂时不要用它。改用“任务计划程序”，在登录时运行 `sous daemon`（PowerShell）：

```powershell
$action  = New-ScheduledTaskAction -Execute (Get-Command sous).Source -Argument "daemon"
$trigger = New-ScheduledTaskTrigger -AtLogOn
Register-ScheduledTask -TaskName "sous" -Action $action -Trigger $trigger -Description "sous daemon"
```

## 文件位置

sous 的文件都在 `~/.sous/`（Windows 上是 `%USERPROFILE%\.sous\`），可以用 `--home` 或环境变量 `SOUS_HOME` 改到别处：

- `node.json`：配对信息和设备令牌（只有你能读）。
- `worktrees/<仓库名>/<id>/`：每个代码任务的独立 git worktree，分支名为 `sous/<id>`。你的原始仓库和工作区不会被修改。
- `scratch/<id>/`：不涉及仓库的一般任务的工作目录。

## 不再使用

```
sous service stop
sous service uninstall
sous unpair
```

然后在网页的机器面板里删除这台电脑。
