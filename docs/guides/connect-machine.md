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
- 代码任务做完后，改动留在任务分支上（或就地），sous 不会自己提交、推送或开 PR。只有你明确让它推送或开 PR 时，这台电脑上的 git 才要能 push 到项目的 `origin`，并且装好、登录 GitHub CLI：`gh auth login`。sous 不会自己合并 PR。
- 决定一个**工作空间**：放项目的目录，比如 `~/Code`。sous 会在这里找 git 仓库（往下两层），远端地址和已登记项目一致的仓库自动成为那个项目在这台电脑上的位置；新建或 clone 的项目也放在这里。新建项目默认用 [wokspace](https://github.com/XiaoXianThis/wokspace) 规范，需要电脑上装了 `wok`。

## 2. 安装 sous 客户端

**一条命令安装（也用来更新）**——脚本会下载最新 Release、按 `checksums.txt` 校验、装到 `~/.local/bin`（已装过则原地替换，并重启运行中的服务）：

macOS / Linux：

```sh
curl -fsSL https://raw.githubusercontent.com/XiaoXianThis/Sous/main/work/scripts/install-sous.sh | sh
```

Windows PowerShell：

```powershell
irm https://raw.githubusercontent.com/XiaoXianThis/Sous/main/work/scripts/install-sous.ps1 | iex
```

指定版本用环境变量 `SOUS_VERSION=v0.1.1`，指定目录用 `SOUS_INSTALL_DIR`。macOS 上用脚本装的不会被系统拦下（没有隔离标记）。

也可以手动安装：从 GitHub Releases 下载对应平台的 `sous_<版本>_<系统>_<架构>`（macOS、Linux 是 `.tar.gz`，Windows 是 `.zip`），解压后把 `sous`（Windows 上是 `sous.exe`）放进 `PATH`。可以用同一个 Release 里的 `checksums.txt` 校验下载的文件。

- macOS 上第一次运行会被系统拦下（只有自签名、没有公证，见 docs/decisions/0026）：在“系统设置 → 隐私与安全性”里允许它，或者执行 `xattr -d com.apple.quarantine sous`。之后 sous 自己下载的更新不会再被拦。
- Windows 上会出现 SmartScreen 提示，选“仍要运行”。

也可以在仓库根目录自己构建：`wok build core`，产物在 `build/core/` 下。

## 3. 配对

0.3 起这台电脑只接受你的指令（`docs/architecture/trust.md`）。配对时，你亲手把一串配对串从网页复制到这台电脑上，它带着你身份的指纹，所以中继没法冒充任何一方。

1. 在网页上打开“设置 → 机器”，点“添加机器”。页面会显示一条命令，10 分钟内有效；配对完成前保持这个页面打开。
2. 在这台电脑上执行这条命令，例如：

   ```
   sous pair https://relay.example.com sous1-abcd… --name 我的笔记本
   ```

3. 网页上会出现“我的笔记本请求加入”。如果这台电脑是你信任的、常开的，可以勾上“设为大脑机器”，AI 会在这里思考。然后刷 Face ID 确认添加。
4. 核对指纹：在这台电脑上运行 `sous trust status`，确认“只认这个身份”后面的指纹和网页“机器”页右上角显示的一致。
5. 检查 agent 是否可用：

   ```
   sous agents
   ```

   “登录”一栏显示“未登录”的 agent，sous 不会把任务派给它。

配对时可以一并写好：

```
sous pair https://relay.example.com sous1-abcd… --name 我的笔记本 --workspace ~/Code
```

之后也能在网页的“设置 → 机器”里修改工作空间。

### 服务器

服务器用同样的方式接入，加上 `--role server`：

```
sous pair https://relay.example.com sous1-abcd… --name 云服务器 --role server
```

- 服务器只接运维任务（部署、重启、查服务），不接写代码的任务；不能使用完全开放模式；改文件、删除、启停服务都要你在网页上批准（见 `docs/decisions/0014`）。
- 网页上可以把开发机改成服务器，但服务器改回开发机只能在那台机器的 `~/.sous/node.json` 里删掉 `"role"`。
- 在项目设置里登记部署目标：哪台服务器、哪个目录、部署命令（最好是仓库里的部署脚本）、健康检查命令，以及部署分支和是否在它出现新提交时自动部署。自动部署看的是 origin 上部署分支的最新提交（按提交时间），不是 PR 是否合并，也不需要装 `gh`；机器每 10 分钟在后台 fetch 一次，sous 推送后也会重新扫描。
- 不建议把服务器设为大脑机器：大脑能读到它参与的项目的数据，也能在自主范围内指挥其他机器。
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

macOS：项目在 `~/Documents`、`~/Desktop` 这类受保护目录下时，LaunchAgent 启动的 sous 一访问就会弹窗要权限，没人点任务就停住。给 `sous` 开「完全磁盘访问权限」，并用固定身份签名，让授权在重建之后依然有效（从发布产物开始签名的版本起，Release 的更新也带同一个签名，不用再重签），见 [macos-signing-and-permissions.md](macos-signing-and-permissions.md)。

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

在网页的 **机器** 面板里，有新版本时每台在线的电脑旁边会出现“更新到 vX.Y.Z”。点了之后要刷一次 Face ID（安装更新是关键操作，由这台电脑自己核对）。然后这台电脑上的 daemon 从 GitHub Releases 下载对应的安装包，先核对作者离线发布密钥对 `checksums.txt` 的签名（`checksums.txt.sig`），再按 `checksums.txt` 校验文件，替换自己，然后重启。没有签名或签名不对的版本一律不装；下载地址写死在程序里，大脑改不了。正在执行的任务会在重启后接着做。

也可以在这台电脑上执行：

```
sous update --check   # 只看有没有新版本
sous update           # 安装最新版本（daemon 在运行时由它来装并重启）
```

## 崩溃和重启

- `sous service install` 注册的服务在进程退出后会被系统自动拉起（launchd 的 KeepAlive、systemd 的 `Restart=always`）。daemon 自己也会检查是否卡住，卡住就退出，让服务管理器重启它。
- daemon 发给大脑的事件先写在本地 `outbox.jsonl` 里，大脑确认收到后才删除，所以断网、大脑换人、daemon 崩溃都不会丢。
- daemon 重启时，正在执行的任务：agent 支持恢复会话（ACP 的 `session/load`）的，接着原来的会话继续；不支持的，标记为中断，大脑会带上一段“恢复说明”自动重新开始一次（agent 会先检查之前做到了哪一步），并在对话里告诉你。再中断一次就标记为失败。

## 文件位置

sous 的文件都在 `~/.sous/`（Windows 上是 `%USERPROFILE%\.sous\`），可以用 `--home` 或环境变量 `SOUS_HOME` 改到别处：

- `node.json`：配对信息：中继地址、这台电脑的 id、固定的身份公钥（只有你能读）。
- `trust/keys.json`：这台电脑自己的签名和加密密钥（权限 0600）；`trust/tunnel.json`：验证过的名册、自主范围等；`trust/audit.log`：它接受和拒绝的每条指令（哈希链），网页“机器 → 审计记录”能看；`trust/frozen.json`：自我冻结时出现。
- `brain/`：这台电脑当大脑时的数据（Hub 的数据库等），只在大脑机器上有。
- `worktrees/<仓库名>/<id>/`：每个代码任务的独立 git worktree，同一个任务的多次执行共用它，分支名为 `sous/<标题>-<id>`。任务做完后 worktree 会保留，方便看 diff 或返工；分支合并、丢弃、开了 PR 或任务取消后才删除 worktree，分支保留。你的原始仓库和工作区不会被修改。
- `scratch/<id>/`：不涉及仓库的一般任务的工作目录。
- `outbox.jsonl`：还没被大脑确认的事件。
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

在网页上编辑 node.json 只能收紧这些规则（可以加 `extra_floor`），不能关掉或替换底线；放宽要在这台电脑上直接改文件。

## 网页上的终端

项目弹窗的“终端”标签可以在这个项目的目录里开 shell，“+”选机器，可以同时开几个。打开时要刷一次 Face ID，由这台电脑自己核对；终端的输入只接受打开它的那个会话直接发来的，大脑转发的一律拒绝；换设备要另开。终端里的命令由你自己输入，不经过权限策略。

sous 每 10 分钟会在后台对工作空间里的仓库执行一次 `git fetch`（只更新远端跟踪分支，不改你的文件），这样网页上才能显示“落后远端几个提交”。不需要的话在 `node.json` 里加 `"auto_fetch": false`。

不想让这台电脑接受网页上的终端，在 `node.json` 里加 `"remote_shell": false`；整个远程管理都不要，加 `"remote_admin": false`。这两个开关只能在这台电脑上改。

## 本机的硬规则

`node.json` 里只能在这台电脑上改的开关：`"allow_open": false` 不论用户的自主范围怎么写都不接受 open 模式；`"remote_admin": false`、`"remote_shell": false` 见上。这台电脑在 10 分钟内收到 3 次越权指令会自我冻结，网页上显示警报，刷 Face ID 解除。

## 不再使用

```
sous service stop
sous service uninstall
sous unpair
```

然后在网页的“机器”里把它踢出（刷 Face ID）。踢出的电脑会删掉自己的密钥，其他机器立刻不再理它。

想让这台电脑忘掉配对、重新配对：`sous trust reset`（只能在本机执行）。
