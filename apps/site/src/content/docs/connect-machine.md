---
title: "接入一台电脑或服务器"
description: "在 macOS、Linux 或 Windows 上安装 sous 客户端，检测本机编程 agent，配对开发机或服务器并注册后台常驻服务。"
group: "getting-started"
order: 2
sourceDoc: "docs/guides/connect-machine.md"
updatedAt: "2026-11"
---

接入客户端后，`sous` 可以把编程任务派发到你的开发机上，把部署与维护任务派发到你的服务器上。客户端支持 macOS、Linux 与 Windows，通过主动连接 Hub 工作，本机不开放入站端口。

本文整理自仓库 `docs/guides/connect-machine.md`。

## 1. 准备本机编程 Agent 与工作空间

`sous` 调度你电脑上已安装并登录的编程 agent 干活（使用它们各自的账号或配置）：

| Agent | 二进制 / 启动方式 | 安装后登录命令 |
|---|---|---|
| **Claude Code** | `claude`（通过 ACP 适配器运行，需 `npx`） | 运行 `claude` 后执行 `/login`（或 `claude auth login`） |
| **Codex** | `codex`（通过 ACP 适配器运行，需 `npx`） | `codex login`（无浏览器环境可用 `codex login --device-auth`） |
| **Cursor** | `cursor-agent acp` | `cursor-agent login` |
| **OpenCode** | `opencode acp` | `opencode auth login` |
| **Kiro** | `kiro-cli acp` | `kiro-cli login --use-device-flow` |

此外建议准备好：

- **Git 与 GitHub CLI**：确保本机 `git` 能推送到仓库 `origin`，并执行 `gh auth login` 登录 GitHub CLI（用于创建或查询 PR；`sous` 绝不会自动合并主分支）。
- **工作空间目录**：选定一个存放项目的目录（如 `~/Code`）。`sous` 会向下扫描两层查找 git 仓库，远端地址与已登记项目匹配的仓库会自动关联为该项目在本机的位置。

## 2. 安装 sous 客户端

使用官方安装脚本一键安装（同样用于原地更新，会自动校验 `checksums.txt` 并安装至 `~/.local/bin`）：

**macOS / Linux：**

```sh
curl -fsSL https://raw.githubusercontent.com/XiaoXianThis/Sous/main/work/scripts/install-sous.sh | sh
```

**Windows PowerShell：**

```powershell
irm https://raw.githubusercontent.com/XiaoXianThis/Sous/main/work/scripts/install-sous.ps1 | iex
```

> 可通过环境变量 `SOUS_VERSION=v0.1.1` 指定版本，或通过 `SOUS_INSTALL_DIR` 指定安装目录。也可以在仓库根目录执行 `wok build core` 从源码构建（产物在 `build/core/`）。

## 3. 配对开发机或服务器

1. 打开 `sous` 网页端，进入 **机器 → 配对新机器**，获取 10 分钟内有效的配对命令。
2. 在目标电脑上运行配对命令（可同时指定工作空间目录）：

```sh
sous pair https://agent.wxian.cc 123456 --name 我的笔记本 --workspace ~/Code
```

3. 检查本机可用的编程 agent 与登录状态：

```sh
sous agents
```

若接入的是**生产或部署服务器**，配对时加上 `--role server`：

```sh
sous pair https://agent.wxian.cc 123456 --name 云服务器 --role server
```

服务器节点默认采用更严格的策略：不允许开启 `open`（完全开放）模式，修改文件、删除、启停系统服务都必须经过你在网页上批准。

## 4. 注册常驻后台服务

先在前台启动确认网页「机器」面板显示**在线**：

```sh
sous daemon
```

确认正常后，在 **macOS**（LaunchAgent）和 **Linux**（systemd 用户服务）上注册开机/登录自启服务（Linux 服务器建议先执行一次 `loginctl enable-linger $USER`）：

```sh
sous service install
sous service start
sous status
```

## 5. 在终端直接对话与审批

当 `daemon` 在本机运行时，你可以随时在终端里与 `sous` 对话，无需再次登录（由本机 `daemon.sock` 凭设备令牌转发）：

```sh
sous chat
```

常用交互命令：

- `/threads` 与 `/use <序号>`：查看并切换对话线程
- `/approvals`：列出当前等待你审批的操作
- `/approve <序号> [附言]`、`/reject <序号> [附言]`：批准或拒绝操作
- `/quit`：退出终端对话

## 6. 本机目录结构与安全底线

客户端文件默认位于 `~/.sous/`（Windows 为 `%USERPROFILE%\.sous\`，可通过 `--home` 或 `SOUS_HOME` 修改）：

| 路径 | 用途 |
|---|---|
| `node.json` | 配对信息、工作空间、agent 偏好与本地策略配置 |
| `worktrees/<仓库名>/<id>/` | 代码任务的独立 git worktree（分支 `sous/<标题>-<id>`），不污染原工作区 |
| `scratch/<id>/` | 不涉及仓库的一般任务临时目录 |
| `outbox.jsonl` | 尚未被 Hub 确认收到的本地事件队列，断网或重启不丢事件 |
| `runs/<id>.json` | 正在执行的 Run 会话状态，用于重启后恢复 |
| `daemon.sock` | 本机 IPC 套接字（仅供当前系统用户访问） |

无论任务处于何种权限模式，命中本地底线规则（如删除主目录、格式化磁盘、`curl … | sh`）的命令都会被 Node 直接拦截。你可以在 `~/.sous/node.json` 的 `policy.extra_floor` 中追加自定义正则规则；从网页远程编辑 `node.json` 只能收紧安全设置，无法关闭底线或关闭远程限制。
