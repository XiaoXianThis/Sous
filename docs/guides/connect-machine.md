# 接入一台电脑或服务器

让 sous 能把编程任务派到你的电脑上，把部署和维护派到你的服务器上。Windows、macOS、Linux 都支持；服务器一般是 Linux。

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
- 代码任务完成后，sous 会 push 分支并创建 PR，所以这台电脑上的 git 要能 push 到项目的 `origin`，并且装好、登录 GitHub CLI：`gh auth login`。sous 永远不会合并 PR。
- 决定一个**工作空间**：放项目的目录，比如 `~/Code`。sous 会在这里找 git 仓库（往下两层），远端地址和已登记项目一致的仓库自动成为那个项目在这台电脑上的位置；新建或 clone 的项目也放在这里。新建项目默认用 [wokspace](https://github.com/XiaoXianThis/wokspace) 规范，需要电脑上装了 `wok`。

## 2. 安装 sous 客户端

从 GitHub Releases 下载对应平台的 `sous_<版本>_<系统>_<架构>`（macOS、Linux 是 `.tar.gz`，Windows 是 `.zip`），解压后把 `sous`（Windows 上是 `sous.exe`）放进 `PATH`。可以用同一个 Release 里的 `checksums.txt` 校验下载的文件。

- macOS 上第一次运行会被系统拦下（没有代码签名，见 docs/decisions/0008）：在“系统设置 → 隐私与安全性”里允许它，或者执行 `xattr -d com.apple.quarantine sous`。之后 sous 自己下载的更新不会再被拦。
- Windows 上会出现 SmartScreen 提示，选“仍要运行”。

也可以在仓库根目录自己构建：`wok build core`，产物在 `build/core/` 下。

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

配对时可以一并写好：

```
sous pair https://agent.wxian.cc 123456 --name 我的笔记本 --workspace ~/Code
```

之后也能在网页的“设置 → 机器”里修改工作空间。

### 服务器

服务器用同样的方式接入，加上 `--role server`：

```
sous pair https://agent.wxian.cc 123456 --name 云服务器 --role server
```

- 服务器只接运维任务（部署、重启、查服务），不接写代码的任务；不能使用完全开放模式；改文件、删除、启停服务都要你在网页上批准（见 `docs/decisions/0014`）。
- 网页上可以把开发机改成服务器，但服务器改回开发机只能在那台机器的 `~/.sous/node.json` 里删掉 `"role"`。
- 在项目设置里登记部署目标：哪台服务器、哪个目录、部署命令（最好是仓库里的部署脚本）、健康检查命令，以及是否在 sous 开的 PR 合并后自动部署。自动部署需要至少一台在线的开发机装好并登录 `gh`，sous 用它查 PR 是否已合并。
- 服务器上一般没有浏览器：登录 agent 时优先用 API Key（“设置 → 机器 → 管理”里填），或者 agent 提供的设备码登录。

## 4. 让它一直运行

先在前台试一下，网页的机器面板里应该显示“在线”：

```
sous daemon
```

没问题之后，在 macOS 和 Linux 上注册成随登录启动的服务（macOS 是 LaunchAgent，Linux 是 systemd 用户服务）。在服务器上，systemd 用户服务默认会在你退出 SSH 后停止，先执行一次 `loginctl enable-linger $USER` 让它一直运行：

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

## 在终端里对话

daemon 在运行时，可以直接在这台电脑的终端里和 sous 对话，不用登录（daemon 替你用这台机器的设备令牌转发）：

```
sous chat
```

默认进入主线，和网页上是同一份对话。回车发送；`/threads` 和 `/use <序号>` 切换对话，`/approvals` 列出待审批的操作，`/approve <序号> [附言]`、`/reject <序号> [附言]` 审批，`/quit` 退出。daemon 重启时它会自己重连。

## 更新

在网页的 **机器** 面板里，有新版本时每台在线的电脑旁边会出现“更新到 vX.Y.Z”。点了之后，这台电脑上的 daemon 从 GitHub Releases 下载对应的安装包，按 `checksums.txt` 校验，替换自己，然后重启。正在执行的任务会在重启后接着做。

也可以在这台电脑上执行：

```
sous update --check   # 只看有没有新版本
sous update           # 安装最新版本（daemon 在运行时由它来装并重启）
```

## 崩溃和重启

- `sous service install` 注册的服务在进程退出后会被系统自动拉起（launchd 的 KeepAlive、systemd 的 `Restart=always`）。daemon 自己也会检查是否卡住，卡住就退出，让服务管理器重启它。
- daemon 发给 Hub 的事件先写在本地 `outbox.jsonl` 里，Hub 确认收到后才删除，所以断网、Hub 重启、daemon 崩溃都不会丢。
- daemon 重启时，正在执行的任务：agent 支持恢复会话（ACP 的 `session/load`）的，接着原来的会话继续；不支持的，标记为中断，Hub 会带上一段“恢复说明”自动重新开始一次（agent 会先检查之前做到了哪一步），并在对话里告诉你。再中断一次就标记为失败。

## 文件位置

sous 的文件都在 `~/.sous/`（Windows 上是 `%USERPROFILE%\.sous\`），可以用 `--home` 或环境变量 `SOUS_HOME` 改到别处：

- `node.json`：配对信息和设备令牌（只有你能读）。
- `worktrees/<仓库名>/<id>/`：每个代码任务的独立 git worktree，同一个任务的多次执行共用它，分支名为 `sous/<标题>-<id>`。任务完成或取消后 worktree 会被删除，分支保留。你的原始仓库和工作区不会被修改。
- `scratch/<id>/`：不涉及仓库的一般任务的工作目录。
- `outbox.jsonl`：还没被 Hub 确认的事件。
- `runs/<id>.json`：正在执行的 Run 的状态（agent 的会话、已送达的追加消息），重启后用来恢复，Run 结束就删除。
- `daemon.sock`：本机接口（`sous chat`、`sous status` 用），只有你能访问。

## 底线规则

不论权限模式是什么，agent 要执行的命令命中底线规则（比如删除 home 目录、格式化磁盘、`curl … | sh`）都会被直接拒绝。规则只在这台电脑上生效，可以在 `node.json` 里调整，改完重启 daemon：

```json
"policy": {
  "extra_floor": ["\\bdrop\\s+database\\b"],
  "floor_disabled": false
}
```

`extra_floor` 追加规则，`floor` 整个替换默认规则，`floor_disabled: true` 关掉底线。规则按命令文本匹配，换个写法就能绕过：它是安全网，不是隔离。

## 不再使用

```
sous service stop
sous service uninstall
sous unpair
```

然后在网页的机器面板里删除这台电脑。
