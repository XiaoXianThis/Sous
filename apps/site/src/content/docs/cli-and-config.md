---
title: "CLI 命令与配置文件参考（node.json / hub.yaml）"
description: "sous 与 sous-hub 全部 CLI 子命令与参数，以及客户端 node.json、服务端 hub.yaml 的完整字段说明。"
group: "reference"
order: 1
sourceDoc: "apps/core/configs/hub.example.yaml"
updatedAt: "2026-11-09"
---

## 为什么需要这份参考

当你在终端执行机器配对、后台服务安装、版本自升级，或者直接编辑电脑上的 `~/.sous/node.json` 与服务器上的 `hub.yaml` 时，可以在本页查阅与 `apps/core` 源码逐项核对过的命令行参数与配置字段。

---

## `sous` 客户端 CLI

`sous` 是运行在开发机与被管服务器上的客户端及守护进程二进制（源码位于 `apps/core/cmd/sous/main.go`）。所有子命令均支持全局参数 `--home <DIR>`（默认 `~/.sous`，也可通过环境变量 `SOUS_HOME` 指定）与 `--debug`。

### 命令总览

| 子命令与参数 | 说明 |
|---|---|
| `sous pair <hub-url> <code> [--name NAME] [--role dev\|server] [--workspace DIR]` | 使用网页生成的 6 位一次性配对码将本机与 Hub 配对，写入 `~/.sous/node.json`（权限 `0600`） |
| `sous daemon` | 在前台启动节点守护进程（连接 Hub `/node`、监听本地 IPC Socket、按指令拉起 ACP Agent） |
| `sous status` | 显示本机配对信息、守护进程运行状态（当前版本、是否已连接 Hub、活跃 Run 数、待发队列）、系统服务状态及已检测到的 Agent |
| `sous agents` | 并行检测本机已安装的编码 Agent、版本号、登录状态及缺失依赖（如 `npx`） |
| `sous chat [--thread ID]` | 通过本机守护进程在终端直接与主管对话（默认进入主线对话，支持流式输出与内联审批） |
| `sous update [--check] [--version vX.Y.Z]` | 检查或安装 GitHub Releases 发布的新版本（若守护进程正在运行，通过本地 IPC 由守护进程完成下载校验与自动重启） |
| `sous service <install\|uninstall\|start\|stop\|restart\|status>` | 安装、卸载、启停或查看本机后台系统服务（macOS LaunchAgent / Linux systemd / Windows 服务） |
| `sous unpair` | 删除本机 `~/.sous/node.json` 取消配对（需同时在网页「机器」页面移除该节点记录） |
| `sous version` | 输出当前 `sous` 版本号 |
| `sous mcp --home DIR --run ID --token T` | 供子 Agent 通过 stdio 调用的内置 MCP 服务（提供 `report_progress`、`ask_supervisor`、`report_result`，由 `sous daemon` 自动挂载，无需手动运行） |

### `sous pair` 参数说明

```bash
sous pair https://agent.wxian.cc 123456 \
  --name macbook-pro \
  --role dev \
  --workspace ~/Code,~/work
```

| 参数 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `<hub-url>` | 位置参数 1 | 必填 | Hub 的访问地址（如 `https://agent.wxian.cc` 或本地开发 `http://localhost:8787`；非回环地址强制要求 `https`） |
| `<code>` | 位置参数 2 | 必填 | 在网页「机器 → 配对新机器」生成的 6 位配对码 |
| `--name` | `string` | 系统主机名 | 本机在 Sous 中显示的名称 |
| `--role` | `string` | `"dev"` | 机器角色：`"dev"`（开发机）或 `"server"`（服务器，不启用 `open` 权限模式） |
| `--workspace` | `string` | 空（可后续在网页设置） | 搜索 Git 仓库的工作空间目录，多个路径用英文逗号 `,` 分隔 |

---

## `sous-hub` 服务端 CLI

`sous-hub` 是中心服务端二进制（源码位于 `apps/core/cmd/sous-hub/main.go`）。

### 命令总览

| 命令与参数 | 说明 |
|---|---|
| `sous-hub [-config hub.yaml] [-debug]` | 启动 Hub 服务（默认读取当前目录下的 `hub.yaml`） |
| `sous-hub [-config hub.yaml] admin passkey-reset [-revoke-all]` | 生成一个新的 24 小时有效的一次性 Passkey 注册链接并打印到终端；传入 `-revoke-all` 时会同时清空已有 Passkey 并登出所有会话 |
| `sous-hub [-config hub.yaml] admin backup <file>` | 在线生成一份一致性的 SQLite 数据库快照备份到 `<file>` |
| `sous-hub version` | 输出当前 `sous-hub` 版本号 |

---

## `~/.sous/node.json` 配置参考

`node.json` 位于 `~/.sous/node.json`（文件权限 `0600`，源码定义位于 `apps/core/internal/node/config.go` 与 `apps/core/internal/policy/policy.go`）。决定「本机允许执行什么」的安全敏感字段（如 `role` 改回 `dev`、`custom_agents`、`sous_mcp`、`remote_admin`、`remote_shell`、`policy` 底线）只能在本机文件中修改，Hub 无法远程放宽。

### 完整结构示例

```json
{
  "hub_url": "https://agent.wxian.cc",
  "node_id": "01J9X8K2M3N4P5Q6R7S8T9U0V1",
  "name": "macbook-pro",
  "token": "sn_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  "role": "dev",
  "workspaces": [
    "~/Code",
    "~/work"
  ],
  "concurrency": 2,
  "consult_idle_minutes": 30,
  "default_agent": "claude",
  "disabled_agents": ["opencode"],
  "custom_agents": [
    {
      "name": "my-agent",
      "title": "My Custom ACP Agent",
      "command": ["my-agent", "acp"]
    }
  ],
  "agents": {
    "claude": ["npx", "-y", "@agentclientprotocol/claude-agent-acp@0.100.0"]
  },
  "agent_options": {
    "claude": {
      "model": "claude-sonnet-4-6"
    }
  },
  "agent_env": {
    "claude": {
      "ANTHROPIC_API_KEY": "sk-ant-..."
    }
  },
  "sous_mcp": [],
  "remote_admin": true,
  "remote_shell": true,
  "auto_fetch": true,
  "approval_inspect_disabled": false,
  "policy": {
    "legacy_shell": false,
    "hard_read_disabled": false,
    "floor_disabled": false,
    "floor": [],
    "extra_floor": [],
    "extra_hard": ["kubectl\\s+delete", "terraform\\s+destroy"],
    "routine_disabled": false
  }
}
```

### `node.json` 字段说明表

| 字段路径 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `hub_url` | `string` | 必填 | Hub 的 HTTP/HTTPS Origin 地址（连接 WebSocket 时自动转为 `ws(s)://<host>/node`） |
| `node_id` | `string` | 配对生成 | Hub 分配的节点 ULID |
| `name` | `string` | 主机名 | 节点显示名称 |
| `token` | `string` | 配对生成 | 设备鉴权令牌（Hub 端仅保存其 SHA-256 哈希） |
| `role` | `string` | `"dev"` | 机器角色：`"dev"` 或 `"server"`。网页只能把 `"dev"` 改为 `"server"`，改回 `"dev"` 必须直接编辑本机 `node.json` |
| `workspaces` | `string[]` | `[]` | 扫描 Git 仓库的工作空间目录列表（支持 `~/` 展开；新建或克隆项目默认落入第一个目录） |
| `concurrency` | `int` | `2` | 本机允许同时执行的最大 Run 数量 |
| `consult_idle_minutes` | `int` | `30` | 项目顾问（`consult`）会话空闲等待下一问的分钟数（超时后进程退出，但保留 `session_id` 供下次恢复） |
| `default_agent` | `string` | `""` | 本机默认 Agent 名称（当任务、预设、项目均未指定 Agent 时采用） |
| `disabled_agents` | `string[]` | `[]` | 在本机禁用的 Agent 名称列表（即使已安装也不会使用） |
| `custom_agents` | `CustomAgent[]` | `[]` | 自定义 ACP Agent 列表，每项含 `name`（小写字母/数字/连字符）、`title`、`command`（字符串数组，首项在 `PATH` 查找）。仅能在本机修改 |
| `agents` | `map[string]string[]` | `{}` | 覆盖内置 Agent 的 ACP 启动命令，如 `{"claude": ["/path/to/claude-agent-acp"]}` |
| `agent_options` | `map[string]map[string]string` | `{}` | 每个 Agent 新建 ACP 会话时的默认配置项（如 `model`、`thought_level`） |
| `agent_env` | `map[string]map[string]string` | `{}` | 按 Agent 注入的环境变量（如 `ANTHROPIC_API_KEY`、`OPENAI_API_KEY`） |
| `sous_mcp` | `string[]` | 当前二进制 `mcp` | 覆盖内置 `sous mcp` 的启动命令；设为 `["off"]` 时关闭内置 `sous mcp` 挂载 |
| `remote_admin` | `*bool` | `true` | 是否允许从网页远程管理本机 Agent（安装、升级、登录、编辑 Agent 配置文件）。仅能在本机修改 |
| `remote_shell` | `*bool` | `true` | 是否允许从网页在项目目录下打开交互式终端（需 `remote_admin` 同为开启）。仅能在本机修改 |
| `auto_fetch` | `*bool` | `true` | 是否每 10 分钟在后台对工作空间仓库执行一次只读 `git fetch`（绝不改动工作区 checkout） |
| `approval_inspect_disabled` | `bool` | `false` | 设为 `true` 时关闭审批 Agent 在本机的只读取证（`inspect` / `read_file`）能力 |
| `policy.legacy_shell` | `bool` | `false` | 设为 `true` 时退回旧版正则拆分分类器（默认使用 `mvdan.cc/sh/v3` AST 语法树解析） |
| `policy.hard_read_disabled` | `bool` | `false` | 设为 `true` 时关闭 `HardRead` 敏感读取分类升级 |
| `policy.floor_disabled` | `bool` | `false` | 设为 `true` 时关闭本地底线拦截规则（不建议在生产机器开启） |
| `policy.floor` | `string[]` | `DefaultFloor`（13 条正则） | 若非空，用给定的 Go 正则表达式列表替换默认底线规则 |
| `policy.extra_floor` | `string[]` | `[]` | 在默认底线规则之上追加的禁止命令正则表达式 |
| `policy.extra_hard` | `string[]` | `[]` | 追加的硬信号正则表达式：命中的命令在 `auto` 模式下也必定交由人工审批，绝不交给 AI 审批员 |
| `policy.routine_disabled` | `bool` | `false` | 设为 `true` 时，将推送任务分支、清理构建产物等常规操作也送交审批 Agent 审核 |

---

## `hub.yaml` 配置参考

`hub.yaml` 是 `sous-hub` 的配置文件（源码定义位于 `apps/core/internal/config/config.go`，示例文件位于 `apps/core/configs/hub.example.yaml`）。API Key 永不直接明文写死在 `hub.yaml` 中，而是通过 `api_key_env` 引用环境变量或 `env_file`（也可在网页「设置 → 模型」中直接配置并加密存入数据库）。

### 完整结构示例

```yaml
listen: "127.0.0.1:8787"
public_url: "https://agent.wxian.cc"
data_dir: "./data"
web_dir: "../../build/web"
env_file: "./.env"
tls_cert: ""
tls_key: ""
insecure_listen: false
dev: false

providers:
  - name: anthropic
    kind: anthropic-messages
    base_url: "https://api.anthropic.com"
    api_key_env: "ANTHROPIC_API_KEY"
  - name: openai
    kind: openai-responses
    base_url: "https://api.openai.com"
    api_key_env: "OPENAI_API_KEY"

roles:
  supervisor: "anthropic/claude-sonnet-4-6"
  reviewer: "anthropic/claude-sonnet-4-6"
  approver: "anthropic/claude-haiku-4-5"
  inspector: "anthropic/claude-haiku-4-5"
  compactor: "anthropic/claude-haiku-4-5"

efforts:
  supervisor: "high"
  reviewer: "medium"

approval_policy:
  allow_medium: true
  max_ai_allows_per_run: 150

triggers:
  followup_every: 5m
  blocked_after: 4h
  run_timeout: 2h
  approval_after: 2h
  question_after: 8h

inspector:
  every: 10m
  repeat: 5
  stalled_after: 30m
  long_after: 3h
  token_alarm: 2000000

retention_days: 90
update_repo: "XiaoXianThis/Sous"

web:
  disabled: false
  search:
    provider: "duckduckgo"
    api_key_env: ""
    base_url: ""

mcp:
  disabled: false
  stdio_disabled: false
```

### `hub.yaml` 字段说明表

| 字段路径 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `listen` | `string` | `"127.0.0.1:8787"` | HTTP 服务监听地址。若监听非本机回环地址且未配 `tls_cert`/`tls_key`，必须显式设 `insecure_listen: true`，否则拒绝启动 |
| `public_url` | `string` | `"http://localhost:8787"` | 用户在浏览器访问的完整 Origin（同时作为 WebAuthn Origin，其主机名作为 Passkey RPID；非 localhost 强制要求 `https://`） |
| `data_dir` | `string` | `"data"` | 存放 `hub.db` 与运行日志的目录（相对路径基于配置文件所在目录解析） |
| `web_dir` | `string` | `""` | 前端静态产物目录（如 `build/web`）；留空时不托管前端静态页 |
| `env_file` | `string` | `""` | 可选的 `KEY=VALUE` 文件路径，启动时在读取 Provider Key 之前加载（已存在的系统环境变量优先） |
| `tls_cert` / `tls_key` | `string` | `""` | 让 Hub 直接提供 HTTPS 的 PEM 证书与私钥路径（无前置反代时使用） |
| `insecure_listen` | `bool` | `false` | 允许在非回环地址上使用明文 HTTP（仅当处于容器内网或独立宿主机 TLS 反代后方时开启） |
| `dev` | `bool` | `false` | 开启仅限本机回环访问的开发免密登录（`POST /api/v1/auth/dev-login`） |
| `providers[].name` | `string` | 必填 | 模型供应商标识名（如 `anthropic`、`openai`） |
| `providers[].kind` | `string` | 必填 | 接口协议类型：`anthropic-messages`、`openai-responses`、`openai-chat` 或测试用的 `mock` |
| `providers[].base_url` | `string` | 官方地址 | 供应商 API 基础地址 |
| `providers[].api_key_env` | `string` | `""` | 存放该供应商 API Key 的环境变量名称 |
| `roles.<role>` | `map[string]string` | 未设角色默认继承 `supervisor` | 以 `"provider/model"` 格式指定 `supervisor`（主管）、`reviewer`（模型验收）、`approver`（审批 Agent）、`inspector`（后台巡检）、`compactor`（对话摘要整理）五个内部角色使用的模型 |
| `efforts.<role>` | `map[string]string` | 模型默认 | 指定各角色的推理思考强度：`low`、`medium`、`high`、`xhigh` 或 `max` |
| `approval_policy.allow_medium` | `bool` | `false`（示例开启为 `true`） | 在 `auto` 模式下是否允许审批 Agent 评估中等风险（`Medium`）操作 |
| `approval_policy.max_ai_allows_per_run` | `int` | `150` | 单个 Run 中审批 Agent 自动放行的次数上限（合法范围 `150..300`） |
| `triggers.followup_every` | `duration` | `5m` | 后台检查超时任务与跟进提醒的周期 |
| `triggers.blocked_after` | `duration` | `4h` | 任务处于受阻状态多久后唤醒主管跟进 |
| `triggers.run_timeout` | `duration` | `2h` | 单个 Run 的最大运行超时时间 |
| `triggers.approval_after` | `duration` | `2h` | 待审批请求超时未处理的提醒阈值 |
| `triggers.question_after` | `duration` | `8h` | 子 Agent 提问（`ask_supervisor`）等待用户回答的超时阈值 |
| `inspector.every` | `duration` | `10m` | 巡检 Agent 扫描活跃 Run 的周期 |
| `inspector.repeat` | `int` | `5` | 连续重复相同动作多少次触发巡检告警 |
| `inspector.stalled_after` | `duration` | `30m` | Run 超过多久无输出判定为停滞（`stalled`） |
| `inspector.long_after` | `duration` | `3h` | Run 持续运行超过多久触发长耗时巡检 |
| `inspector.token_alarm` | `int` | `2000000` | 单周期 Token 消耗告警阈值 |
| `retention_days` | `int` | `90` | 已关闭任务的运行日志与 Diff 归档保留天数（设为 `-1` 永不清理） |
| `update_repo` | `string` | `"XiaoXianThis/Sous"` | 检查与下载版本更新的 GitHub 仓库（`owner/name`） |
| `web.disabled` | `bool` | `false` | 设为 `true` 时移除主管的 `web_search` 与 `web_fetch` 联网工具 |
| `web.search.provider` | `string` | `"duckduckgo"` | 联网搜索引擎：`duckduckgo`（默认免 Key）、`brave`、`tavily` 或 `searxng` |
| `web.search.api_key_env` | `string` | `""` | `brave` 或 `tavily` 所需的 API Key 环境变量名 |
| `web.search.base_url` | `string` | `""` | 当 `provider: "searxng"` 时的实例地址 |
| `mcp.disabled` | `bool` | `false` | 设为 `true` 时彻底关闭主管的外部 MCP Server 功能 |
| `mcp.stdio_disabled` | `bool` | `false` | 设为 `true` 时禁止在 Hub 上启动本地 `stdio` 类型的 MCP 进程，仅允许连接远程 `http` MCP 服务 |

---

## 下一步

- 查阅全部 HTTP 接口、WebSocket 事件与内置 Agent 矩阵：[HTTP 接口、事件类型与 Agent 模型矩阵](/docs/api-and-events)
- 了解如何在本地编译、测试与扩展 Sous：[开发指南：仓库结构、构建测试、扩展与发布签名](/docs/development-guide)
