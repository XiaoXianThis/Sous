---
title: "预设（岗位）、机器工作空间与项目部署"
description: "理解预设（Agent + 模型 + 思考强度）的挑选优先级、开发机与服务器角色边界、工作空间自动扫描以及部署目标。"
group: "concepts"
order: 2
sourceDoc: "docs/decisions/0024-presets-machine-agents-and-a-lighter-supervisor.md"
updatedAt: "2026-11"
---

当你有不止一台电脑、不止一个编程 Agent 时，主管怎么知道该把任务派给哪台电脑、用哪个 Agent 和哪个模型？这由**预设（Preset）**、**机器与工作空间（Node & Workspaces）**以及**项目（Project）**共同决定。

## 本页目录

- [1. 预设（Preset / Persona）与模型选项](#1-预设preset--persona与模型选项)
- [2. 机器（Node）、角色与工作空间](#2-机器node角色与工作空间)
- [3. 项目（Project）与服务器自动部署](#3-项目project与服务器自动部署)

---

## 1. 预设（Preset / Persona）与模型选项

**为什么把「岗位」改成了「预设」**：在早期版本（决策 `0013`）中，`sous` 曾把配置叫做「人员/岗位」（如前端工程师、后端工程师）。但实际使用表明，给编程 Agent 塞一段拟人化的岗位提示词对代码质量帮助不大；真正决定速度、成本和效果的是 **「用哪个 Agent + 选哪个模型 + 开多高的思考强度」**（决策 `0024`，数据库表与 API 路径仍保留 `personas` 名称以兼容）。

### 预设包含什么

每个预设由以下字段组成：

- **名称（`name`）**：如 `主力重构`、`快速修改`、`验收`。
- **Agent（`agent`）**：如 `claude`、`codex`、`cursor`、`opencode`、`kiro`（留空则跟随项目或机器默认）。
- **会话选项（`options`）**：包含通用的 `model`（模型名）与 `effort`（思考强度：`low` / `medium` / `high` / `xhigh` / `max`），或特定 Agent 自己的配置项 ID。
- **定位说明（`brief`）**：**只给主管看**（例如「复杂跨文件重构时用，速度慢但稳」），帮助主管按任务难度挑选预设；**不会**拼进子 Agent 的提示词里（唯一的例外是内置的 `reviewer` 验收预设）。
- **默认权限模式（`default_mode`）**：`default`、`auto` 或 `open`。

> 新安装的 Hub 只内置一个 `验收（reviewer）` 预设（供 `review: agent` 验收时使用），其余预设由你在网页「设置 → 预设」中按自己常用的组合自由添加。

### 任务级覆盖（`options.model` / `options.effort`）

你不需要为了临时换个模型就新建一个预设。主管在创建或修改任务（以及唤起项目顾问）时，可以直接在任务上指定 `model` 和 `effort`。派发时：

1. **任务的 `options`** 覆盖 **预设的 `options`**，再覆盖 **电脑 `node.json` 的 `agent_options` 默认值**。
2. Hub 只需传通用的 `model` 和 `effort` 键；Node 端会根据各家 Agent 在 ACP `session/new` 中上报的选项类别（`category: "model"` 或 `"thought_level"`）自动映射到该 Agent 自己的选项 ID，并按值或名称（不区分大小写、支持唯一子串匹配）完成设置。

### Agent 选择优先级

当任务派发时，系统按以下从高到低的顺序确定使用哪个 Agent：

```text
1. 任务显式指定的 Agent (task.agent_pref)
2. 任务所选预设指定的 Agent (persona.agent)
3. 项目设置的默认预设 / 默认 Agent (project.default_persona / default_agent)
4. 目标电脑在 node.json 中配置的 default_agent
5. Hub 全局默认 Agent (settings.default_agent)
6. 目标电脑上任何已安装、未停用且未处于「未登录」状态的可用 Agent
```

> **注意**：在某台电脑 `~/.sous/node.json` 的 `disabled_agents` 列表中停用的 Agent，即使在任务里点名指定，该电脑也会拒绝启动。

---

## 2. 机器（Node）、角色与工作空间

每台运行 `sous daemon` 并配对成功的电脑或服务器都是一个 **Node**。

### 两种机器角色：`dev` 与 `server`

| 属性 | 开发机（`role: "dev"`，默认） | 服务器（`role: "server"`） |
|---|---|---|
| **偏好任务** | 优先承接 `worktree` 代码任务、`scratch` 一般任务与项目顾问（`consult`） | 优先承接运维与部署任务（`kind: ops` / `in_place`） |
| **权限策略上限** | 支持 `default`、`auto`、`open` 三档模式 | **禁止 `open` 模式**（请求 `open` 时自动降级为 `default`）；任何修改文件（`edit-worktree`）、删除（`delete`）、启停服务（`service`）、不可逆操作（`irreversible`）与未知脚本**一律交由用户人工审批**，绝不交给审批 Agent |
| **角色切换限制** | 可在网页「设置 → 机器」中改为 `server` | **不可从网页改回 `dev`**（防止被远程降级安全策略），只能登录该服务器修改本机 `~/.sous/node.json` |

> **偏好路由原则（决策 `0025`）**：机器角色是**路由偏好**而非死板的硬阻断。例如当没有服务器在线或项目未配置服务器时，任务仍可退回到持有该仓库的开发机并在任务状态与汇报中明确注明 `【任务派到了别的机器】`，由主管和你知情决策。

### 工作空间（Workspaces）与自动扫描

- 在配对时通过 `--workspace ~/Code` 或在网页「设置 → 机器」中添加工作空间目录。
- `sous daemon` 会在连上 Hub 时、工作空间变更时、新建/克隆项目后，以及**每 10 分钟**向下扫描最多两层目录，找出所有 Git 仓库（跳过隐藏目录、符号链接、`node_modules`、`vendor` 等）上报 `node.repos`。
- 如果扫描到的仓库远端地址（`remote_key`）与某个已登记项目一致，它会自动绑定为该项目在这台电脑上的位置（`source: scan`）；尚未登记的仓库会出现在工作台的「发现的仓库」中，方便一键添加。
- 默认情况下，daemon 每 10 分钟还会在后台对工作空间内有 remote 的仓库静默执行一次 `git fetch`（只更新远端跟踪分支，绝不改动你的工作区文件），以便在工作台位置标签上实时标出 `↓ 落后远端`、`↑ 未推送`、`● 未提交`。如需关闭后台 fetch，可在 `node.json` 中设置 `"auto_fetch": false`。

### 并发槽位（Concurrency）

每台机器在 `node.json` 中通过 `concurrency`（默认 `2`）限制同时执行的任务数：
- **占槽位的**：正在执行的工作 Run（`purpose: work`）和验收 Run（`purpose: review`）。
- **不占槽位的**：
  1. **项目顾问（`purpose: consult`）**：只读常驻会话，随时响应提问；
  2. **Git 操作（`purpose: git`）**：不启动编程 Agent，秒级完成；
  3. **正在等待用户人工审批的 Run**：进入「等你审批」期间会自动释放槽位让其他任务先跑，等你点击批准后再取回槽位继续。

---

## 3. 项目（Project）与服务器自动部署

每个 **项目（Project）** 对应一个 Git 仓库，以规范化后的远端地址（`internal/project.Key`，例如 `github.com/xiaoxianthis/sous`）作为唯一身份标识；此外系统内置一个不可删除的 `misc`（杂项）项目，用来收纳不涉及具体仓库的任务。

### 多机位置与优先级

一个项目可以同时存在于你的多台开发机和服务器上（`project.nodes`）。列表顺序即路由优先级：当你没有点名哪台电脑时，主管优先挑选排在前面且在线、空闲的电脑；而**同一个任务的后续 Run（如返工、验收、合并）会固定留在持有该任务 worktree 分支或就地快照的那台电脑上**。

### 部署目标（Deploy Target）与自动部署

在项目设置的「部署」区域，你可以为项目绑定一台角色为 `server` 的服务器：

- **服务器与目录（`node_id` + `path`）**：项目在服务器上的部署路径（如 `/opt/myapp`）。
- **部署命令（`command`）**：服务器上执行的部署脚本（如 `./deploy.sh` 或 `docker compose up -d --build`）。
- **健康检查命令（`health`，可选）**：部署 Agent 跑完后，Node 会在部署目录执行该命令（2 分钟超时，经过本地策略校验）。只有退出码为 `0` 才算部署成功并更新 `deployed_commit`；若失败则任务转为 `blocked`（受阻）并通知你。
- **部署分支与自动部署（`branch` + `auto_on_merge`）**：
  - 开启自动部署时必须指定 `branch`（如 `main` 或 `release`）。
  - 首次开启时，Hub 只记下当前 `origin/<branch>` 的提交作为基线（`baseline: true`），不会立即触发部署。
  - 此后每当任何机器扫描上报 `origin/<branch>` 出现了更新的提交（按提交时间戳 `head_times` 取最新，防止旧扫描回退），Hub 就会自动为该提交创建一个运维部署任务（每个新提交只触发一次）。你也可以随时在项目弹窗点「立即检查部署分支」或让主管手动发起部署。

下一步：阅读 [审批机制：策略、硬信号、审批 Agent 与复用](/docs/approval-mechanism)，了解 `sous` 如何在安全与少打扰之间取得平衡。
