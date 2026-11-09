---
title: "接入电脑与配置各家 Agent"
description: "在 macOS、Linux、Windows 上安装配对 sous 客户端，安装与登录 Claude Code / Codex / Cursor / OpenCode / Kiro，以及通过 custom_agents 接入 Antigravity 等自定义 Agent。"
group: "guides"
order: 1
sourceDoc: "docs/guides/connect-machine.md"
updatedAt: "2026-11"
---

`sous` 依靠你各台电脑或服务器上已安装并登录的编程 Agent 干活。本指南介绍如何安装配对 `sous` 客户端、配置内置的五大 Agent（Claude Code、Codex、Cursor、OpenCode、Kiro），以及通过 `~/.sous/node.json` 的 `custom_agents` 接入 Antigravity 等自定义 ACP Agent。

## 本页目录

- [1. 安装与配对 `sous` 客户端](#1-安装与配对-sous-客户端)
- [2. 各家编程 Agent 的安装与登录](#2-各家编程-agent-的安装与登录)
- [3. 配置 `custom_agents`（接入 Antigravity 或其他 ACP Agent）](#3-配置-custom_agents接入-antigravity-或其他-acp-agent)
- [4. 在网页上远程管理 Agent 与配置](#4-在网页上远程管理-agent-与配置)
- [5. 注册后台常驻服务与终端 `sous chat`](#5-注册后台常驻服务与终端-sous-chat)

---

## 1. 安装与配对 `sous` 客户端

**什么时候用**：每当你想把一台新的笔记本、台式机或云服务器交给 `sous` 调度时执行一次。

### 一键安装（同时用于原地更新）

脚本会自动从 GitHub Releases 下载对应系统架构的压缩包，核对 `checksums.txt` 的 SHA-256，安装到 `~/.local/bin`（可通过 `SOUS_INSTALL_DIR` 自定义目录、`SOUS_VERSION=v0.1.7` 指定版本）：

**macOS / Linux：**

```sh
curl -fsSL https://raw.githubusercontent.com/XiaoXianThis/Sous/main/work/scripts/install-sous.sh | sh
```

**Windows PowerShell：**

```powershell
irm https://raw.githubusercontent.com/XiaoXianThis/Sous/main/work/scripts/install-sous.ps1 | iex
```

> **macOS 手动下载说明**：用一键脚本安装的二进制不会带浏览器下载的隔离属性（Quarantine）。如果你是从浏览器手动下载的 `.tar.gz`，首次运行时若提示「无法验证开发者」，可在「系统设置 → 隐私与安全性」里点击允许，或执行 `xattr -d com.apple.quarantine sous`。

### 配对开发机或服务器

1. 打开 `sous` 网页端，进入 **设置 → 机器 → 配对新机器**，获取 10 分钟内有效的 6 位配对码（连续输错 5 次会自动作废全部有效配对码）。
2. 在目标电脑上执行配对命令：

```sh
# 配对开发机（默认 --role dev），同时设置工作空间目录（多个目录用英文逗号分隔）
sous pair https://agent.wxian.cc 123456 --name 我的笔记本 --workspace ~/Code

# 配对生产或部署服务器（启用服务器严格权限策略）
sous pair https://agent.wxian.cc 123456 --name 云服务器 --role server --workspace /opt
```

3. 运行检测命令，确认本机哪些 Agent 可用：

```sh
sous agents
```

> **注意**：`sous agents` 中「登录」一栏显示为 **`未登录`** 的 Agent，`sous` 不会把任务派给它。

---

## 2. 各家编程 Agent 的安装与登录

`apps/core/internal/agents/agents.go` 开箱内置了 **5 个编程 Agent**（`claude`、`codex`、`cursor`、`opencode`、`kiro`）。你可以直接在终端安装登录，也可以在配对后通过网页「设置 → 机器 → 管理」点按钮在浏览器内置伪终端里安装和登录。

### 2.1 Claude Code（`claude`）

- **运行机制**：通过官方 ACP 适配器 `npx -y @agentclientprotocol/claude-agent-acp@0.100.0` 启动，因此电脑上**必须安装 Node.js（含 `npx`）**。
- **安装命令**：
  - macOS / Linux：`curl -fsSL https://claude.ai/install.sh | bash`
  - Windows PowerShell：`irm https://claude.ai/install.ps1 | iex`
- **登录方式**：
  - 交互登录：执行 `claude auth login`（或运行 `claude` 后输入 `/login`），按提示在浏览器授权。
  - 环境变量 / API Key：支持在网页机器管理面板或 `~/.sous/node.json` 的 `agent_env.claude` 中设置 `CLAUDE_CODE_OAUTH_TOKEN` 或 `ANTHROPIC_API_KEY`。
- **状态检测与配置文件**：通过 `claude auth status` 检测登录态；配置文件位于 `~/.claude/settings.json`。

### 2.2 Codex（`codex`）

- **运行机制**：通过 ACP 适配器 `npx -y @agentclientprotocol/codex-acp@2.5.0` 启动，同样**需要安装 Node.js（`npx`）**。
- **安装命令**：

```sh
npm install -g @openai/codex
```

- **登录方式**：
  - 本机或无浏览器服务器登录：`codex login` 或 `codex login --device-auth`（网页远程登录默认使用 `--device-auth` 设备码流程）。
  - 环境变量或自定义 Provider：支持设置 `OPENAI_API_KEY`，或在 `~/.codex/config.toml` 中配置带独立 `env_key` / `api_key` 的 `model_provider`（`sous` 会自动解析 `config.toml` 识别登录状态）。

### 2.3 Cursor（`cursor`）

- **运行机制**：原生内置 ACP 服务（`cursor-agent acp`）。由于 Cursor 的 `ask` 模式是纯只读问答，`sous` 会将其会话模式固定设为 `agent`，由 Cursor 在执行 shell 命令前向 `sous` 请求权限审批。
- **安装命令**：
  - macOS / Linux：`curl https://cursor.com/install -fsS | bash`
  - Windows PowerShell：`irm 'https://cursor.com/install?win32=true' | iex`
- **登录方式**：执行 `cursor-agent login`，或配置环境变量 `CURSOR_API_KEY`。
- **状态检测与配置文件**：通过 `cursor-agent status` 检测；配置文件位于 `~/.cursor/cli-config.json`。

### 2.4 OpenCode（`opencode`）

- **运行机制**：原生内置 ACP 服务（`opencode acp`）。由于 OpenCode 默认不询问直接改文件和跑命令，`sous` 启动它时会自动注入环境变量 `OPENCODE_CONFIG_CONTENT={"permission":{"edit":"ask","bash":"ask","webfetch":"ask","external_directory":"ask"}}`，强制它先向 `sous` 请求权限。
- **安装命令**：
  - macOS / Linux：`curl -fsSL https://opencode.ai/install | bash`
  - Windows：`npm install -g opencode-ai`
- **登录方式**：执行 `opencode auth login`（交互式选择模型服务商并粘贴 Key）。
- **注意事项**：OpenCode 支持多家模型服务商，无法通过单一命令预先判断登录态（`sous agents` 中登录状态显示为 `未知`，可直接派发任务）。若在 `node.json` 中覆盖其启动命令，请勿覆盖 `OPENCODE_CONFIG_CONTENT`；如需指定配置文件请用 `OPENCODE_CONFIG=<文件路径>`。配置文件位于 `~/.config/opencode/opencode.json`。

### 2.5 Kiro（`kiro`）

- **运行机制**：原生内置 ACP 服务（`kiro-cli acp`）。Kiro 默认开启 `autopilot` 会跳过询问，`sous` 在 `default` 和 `auto` 权限模式下会自动通过 ACP 将 `autopilot` 设为 `off`（`open` 模式下设为 `on`）。
- **安装与登录命令**：
  - macOS / Linux 安装：`curl -fsSL https://cli.kiro.dev/install | bash`
  - 登录：`kiro-cli login --use-device-flow`（通过 `kiro-cli whoami` 检测状态）。
- **注意事项**：Kiro 除了会话选项外，还会按 `~/.kiro/settings/permissions.yaml` 里的规则自行放行操作，那部分规则放行的操作不会经过 `sous` 审批。

---

## 3. 配置 `custom_agents`（接入 Antigravity 或其他 ACP Agent）

**什么时候用**：除了上述 5 个内置 Agent（`claude`、`codex`、`cursor`、`opencode`、`kiro`）之外，如果你本机安装了 **Antigravity** 或其他支持 **ACP（Agent Client Protocol，基于 stdio 的 JSON-RPC 2.0）** 的编程 Agent，可以通过 `~/.sous/node.json` 的 `custom_agents` 字段接入。

> **安全边界说明**：`custom_agents` 决定了本机实际会运行什么可执行程序，因此**只能在这台电脑本地直接编辑 `~/.sous/node.json`**；从网页端远程编辑 `node.json` 时，Node 会强制保留磁盘上的 `custom_agents` 不被远程覆盖（见决策记录 `0024`）。

### 配置步骤

打开本机的 `~/.sous/node.json`（Windows 为 `%USERPROFILE%\.sous\node.json`），添加 `custom_agents` 数组，并可选设置 `default_agent` 或 `disabled_agents`：

```json
{
  "hub_url": "https://agent.wxian.cc",
  "node_id": "01J...",
  "name": "我的开发机",
  "token": "...",
  "default_agent": "claude",
  "disabled_agents": ["kiro"],
  "custom_agents": [
    {
      "name": "antigravity",
      "title": "Antigravity",
      "command": ["agy", "acp"]
    }
  ]
}
```

字段规则（见 `apps/core/internal/node/config.go` 与 `admin.go`）：
- `name`：必填，只能包含小写字母 `a-z`、数字 `0-9` 和连字符 `-`，且不能与内置的 5 个名字（`claude` / `codex` / `cursor` / `opencode` / `kiro`）或 `sous` 重名。
- `title`：可选，界面上展示的友好名称（不填则使用 `name`）。
- `command`：必填，启动该 Agent ACP stdio 服务的命令与参数数组（第一个元素会在本机 `PATH` 中查找以判断是否已安装）。
  - *注：仓库 `apps/core/internal/agents/agents.go` 目前仅内置了上述 5 个 Agent，未内置 Antigravity 的启动定义；上方示例中的 `["agy", "acp"]` 子命令参数**「需确认」**（请以你本机安装的 Antigravity CLI 实际支持的标准 ACP stdio 启动参数或适配器命令为准）。*

保存 `~/.sous/node.json` 后无需重启 daemon（`Daemon.apply` 会立即重新加载配置并向 Hub 上报 `inventory`），你也可以在终端运行 `sous agents` 确认 `antigravity` 已出现在列表中。

---

## 4. 在网页上远程管理 Agent 与配置

当电脑的 `remote_admin` 保持开启（默认开启）时，你可以在网页 **设置 → 机器** 中点击某台在线电脑旁的 **管理**：

- **远程安装 / 升级 / 登录 / 退出登录**：点击按钮后，Node 会在本机伪终端（macOS/Linux `pty`，Windows `ConPTY`）中运行 `internal/agents` 里写死的命令，网页端实时渲染终端输出并提取授权链接或设备验证码，你也可以直接在网页终端里粘贴验证码。
- **探测会话选项（Probe）与设置默认模型**：点击「读取选项」会让 Node 启动一次该 Agent 的空会话，读出它支持的所有模型（`model`）与思考强度（`thought_level`）缓存在 `~/.sous/agent-options.json`，随后即可在页面上为该电脑的该 Agent 指定每个新会话的默认模型。
- **配置 API Key 环境变量**：直接填入 `ANTHROPIC_API_KEY`、`OPENAI_API_KEY`、`CURSOR_API_KEY` 等允许的变量（保存在 `node.json` 的 `agent_env` 中，网页读取时永远打码为 `***`）。
- **在线编辑配置文件**：支持直接编辑 `~/.claude/settings.json`、`~/.codex/config.toml`、`~/.cursor/cli-config.json`、`~/.config/opencode/opencode.json`、`~/.kiro/settings/*` 以及本机的 `node.json`（写入前自动备份为 `<原文件名>.sous-bak`，并校验 JSON/TOML/YAML 语法与 SHA-256 防并发冲突）。

---

## 5. 注册后台常驻服务与终端 `sous chat`

### 注册开机/登录自启服务

在 **macOS**（注册为 `LaunchAgent`）和 **Linux**（注册为 `systemd` 用户服务）上执行：

```sh
# 若在 Linux 服务器上，先开启 linger 防止退出 SSH 后用户服务停止：
loginctl enable-linger $USER

sous service install
sous service start
sous status
```

> **macOS 特别提醒**：如果你的代码仓库放在 `~/Documents`、`~/Desktop` 等受 macOS TCC 保护的目录下，LaunchAgent 启动的 `sous` 访问它们时会弹窗并卡住。请务必按 [macOS 签名与隐私权限](/docs/development-guide#macos-代码签名与-tcc-权限问题) 执行一次 `wok run core setup-signing` 并给 `sous` 开启「完全磁盘访问权限」。

> **Windows 特别提醒**：Windows 上 `sous service install` 注册的是以 `SYSTEM` 账户运行的系统服务，无法读取你个人用户目录下的 Agent 登录态。在 Windows 上请改用 PowerShell 注册「登录时启动」的计划任务：
> ```powershell
> $action  = New-ScheduledTaskAction -Execute (Get-Command sous).Source -Argument "daemon"
> $trigger = New-ScheduledTaskTrigger -AtLogOn
> Register-ScheduledTask -TaskName "sous" -Action $action -Trigger $trigger -Description "sous daemon"
> ```

### 在终端使用 `sous chat`

当本机 `daemon` 运行时，随时在终端输入：

```sh
sous chat
```

即可直接进入主线对话（也可带 `--thread <线程ID>` 进入指定对话），无需再次登录。支持以下命令：
- `/threads`：列出所有对话线程
- `/use <序号>`：切换到指定对话线程
- `/approvals`：查看当前待审批的操作
- `/approve <序号> [附言]`：批准指定操作（使用第一个允许选项）
- `/reject <序号> [附言]`：拒绝指定操作
- `/quit`（或 `/exit`）：退出终端聊天
