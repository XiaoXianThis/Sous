---
title: "主管、子 Agent、任务与执行位置"
description: "深入理解 sous 的主管与 ACP 子 Agent 闭环、Task 与 Run 的生命周期，以及 worktree / in_place / scratch 三种执行位置。"
group: "concepts"
order: 1
sourceDoc: "docs/architecture/workbench.md"
updatedAt: "2026-11"
---

`sous` 没有把你直接塞进某一个编程 Agent 的终端里，而是用「主管 → 任务（Task）→ 运行（Run）→ 执行位置（Place）」这四个概念，把多个项目和多台机器上的并发工作管理得井井有条。

## 本页目录

- [1. 主管（Supervisor）与子 Agent 的闭环](#1-主管supervisor与子-agent-的闭环)
- [2. 任务（Task）与运行（Run）](#2-任务task与运行run)
- [3. 三种执行位置：worktree / in_place / scratch](#3-三种执行位置worktree--in_place--scratch)

---

## 1. 主管（Supervisor）与子 Agent 的闭环

**为什么分两层 Agent**：编程 Agent（Claude Code、Codex、Cursor、OpenCode、Kiro）擅长在单个仓库里深入读改代码、跑测试，但它们不知道你手头有几台电脑、记不住跨项目的长期偏好，也没法在任务结束后自己串联下一个项目的部署。

因此 `sous` 将智能拆为两层（见决策记录 `0001`、`0004`、`0025`）：

### 主管（Supervisor，运行在 Hub）

- **唯一对话窗口**：你在网页、手机或 `sous chat` 里只跟主管说话。主管单轮最多可调用 **50 次模型**（`MaxSteps = 50`，同一工具同参数最多重复 2 次），倒数第二步会收到收尾提示并向你汇报阶段结论。
- **只读快查与项目顾问**：
  - 小问题直接调用 `project_read`（读文件、列目录、正则搜索）或 `machine_look`（在目标电脑上按 `readonly` 档位执行单条只读命令，20 秒超时、最多 16 KB，每轮最多 4 次）。
  - 需要深入分析代码时调用 `consult` 唤醒**项目顾问**：在项目所在开发机上启动一个强制 `readonly` 的常驻 ACP 会话，问题之间保持空闲（默认 30 分钟，`consult_idle_minutes`），讨论成熟后可通过 `from_consult` 把会话直接移交给正式任务。
- **事件唤醒与 20 秒合批**：主管不仅在你发消息时运行，当任务完成、受阻、失败、等待机器、派到了非偏好机器，或子 Agent 提问时，任务引擎都会发出 `trigger.fired`（`kind: task`）叫醒主管。为了避免并发任务频繁打断，`kind: task` 触发会等待 **20 秒（`TaskBatch`）** 合并为同一轮处理；在用户未发言期间，仅靠触发唤醒的连续回合最多 **8 轮（`MaxAutoTurns`）**。

### 子 Agent（ACP Coding Agents，运行在 Node）

- **统一走 ACP 协议**：Node 通过标准 Agent Client Protocol（stdio JSON-RPC）启动各家编程 Agent，把文件读写、终端命令和权限请求统一纳入 `sous` 管控。
- **内置 `sous mcp` 双向通道**：每个工作 Run 启动时，Node 都会自动向子 Agent 注入一个本地 stdio MCP 服务（`sous mcp --home <dir> --run <id> --token <随机令牌>`），提供三个工具：
  1. `report_progress`：子 Agent 用一句话报告当前阶段（如「依赖装好了，正在跑测试」），实时显示在工作台任务卡上。
  2. `ask_supervisor`：遇到需求有两种理解或缺少配置时向主管或用户提问。Hub 会叫醒主管回答（或转交用户），工具调用原地等待下一条追加指令（最长等 20 分钟，每个 Run 最多问 3 次）。
  3. `report_result`：结束时提交「做了 / 改动 / 验证 / 没做完与风险 / 需要决定」五项结构化结果。

---

## 2. 任务（Task）与运行（Run）

**为什么要区分 Task 和 Run**：一个需求（Task）可能经历「写代码 → 验收不通过返工 → 再跑一次 → 合并分支」多个步骤。把管理单元（Task）和单次执行（Run）分开，才能清晰追踪每次尝试的日志、Diff 和审批记录。

| 维度 | 任务（Task） | 运行（Run） |
|---|---|---|
| **定位** | 需求与工作项的管理单元，归属于某个项目（或内置 `misc`） | 某台机器上的某一个 Agent（或内置命令）针对任务的一次具体执行 |
| **前端展示状态** | 工作台统一简化为 **4 种状态**：`待办`、`进行中`、`要你处理`、`已结束` | 运行中展开为实时 Agent 卡；历史记录在任务详情中按时间倒序折叠展示 |
| **底层状态机** | `inbox` / `planned` / `queued` / `running` / `waiting_approval` / `blocked` / `review` / `done` / `failed` / `cancelled` | `queued` / `running` / `succeeded` / `failed` / `cancelled` / `interrupted`（附带 `paused` 标记） |
| **用途分类** | `kind` 仅作分类标签：`code`（代码）、`general`（一般）、`ops`（运维） | `purpose` 决定执行行为：`work`（干活）、`review`（只读验收）、`git`（受控 Git 操作）、`consult`（只读顾问）、`create` / `clone`（新建或克隆项目） |

### 会话续接（返工与重试不丢上下文）

当一个任务因为验收未通过（`on_fail: rework`）或中断后在同一台电脑、同一个 Agent 上再次启动工作 Run 时，如果上一个工作 Run 记录了 `session_id` 且该 Agent 支持 ACP 的 `session/load`，Node 会直接加载原会话并只发送简短的增量提示（`continue_prompt`：包含验收意见、上轮报错、被拒绝的操作和最新说明），避免重新读一遍整个仓库。

### 诚实的执行结果判定（`outcome.go`）

Node 不会盲目相信子 Agent 进程退出码为 0 就等于「做完了」。如果一次工作 Run 没有任何工具调用、没有产生任何文件改动，且仅输出了不超过 300 字的短回复：
- 若回复命中额度耗尽、限流或未登录特征，Run 直接标记为 `failed` 并附带 `error_kind`（`agent_quota` / `agent_rate_limit` / `agent_not_logged_in` / `agent_unavailable`）；
- 否则标记为 `outcome: no_action`，Hub 会将任务转为 `blocked`（受阻）并唤醒主管跟进，绝不虚报「已完成」。

---

## 3. 三种执行位置：`worktree` / `in_place` / `scratch`

**为什么不写死单一执行方式**：开发新功能时需要隔离分支以免弄乱你手头正在改的代码；在服务器上跑部署脚本或微调未纳入分支的配置时需要直接就地执行；而跑一个与仓库无关的数据处理脚本只需要一个干净的临时文件夹。

根据决策记录 `0022`，任务没有写死的默认位置，主管在派发任务前会根据场景明确指定以下三种 `place` 之一：

| 执行位置 (`place`) | 实际工作目录 | 适用场景 | 隔离与保护机制 |
|---|---|---|---|
| **`worktree`** | `~/.sous/worktrees/<仓库名>/<短id>` | 绝大多数代码开发、修 Bug、写文档、重构任务（支持多任务并发） | 使用 `git worktree` 切出独立分支 `sous/<标题slug>-<短id>`，**完全不触碰你原本的工作区和当前分支**。必须明确指定起点分支 `base_branch` 与来源 `base_from`（`local` 本地分支，或 `remote` 先 `git fetch --prune origin` 再从 `origin/<base_branch>` 切出）。同一个任务的多次 Run 共用该 worktree；任务完成后 worktree 保留供后续合并/开 PR，直到分支合并、丢弃、开 PR 或任务取消后才清理。 |
| **`in_place`** | 项目的原始目录（`repo_path`） | 服务器运维部署（`ops`）、没有远端的本地仓库、或者你明确要求直接在当前工作区就地修改 | 不建 worktree，同一个目录同时只允许跑 **1 个**就地任务（自动互斥排队）。第一次启动前，Node 会在 `~/.sous/shadow/<目录哈希>` 的**影子仓库**里为项目目录拍下开始快照（`refs/sous/<短id>`，排除 `node_modules`、`.venv` 等，且强制 `* -text` 避免 Windows 换行符被改写），每次 Run 结束再记结束快照（`refs/sous/<短id>-end`）。支持按快照精准只提交任务改过的文件（`git_commit`）或一键恢复任务改过的文件（`task_restore`），绝不覆盖你自己在工作区里的其他改动。 |
| **`scratch`** | `~/.sous/scratch/<短id>` | 不属于任何代码仓库的一般任务（自动归入内置的 `misc` 杂项项目） | 独立的空临时目录，不关联 Git 仓库，适合信息整理、一次性脚本验证或跨项目调研。 |

下一步：阅读 [预设（岗位）、机器工作空间与项目部署](/docs/presets-and-machines)，了解如何配置 Agent + 模型组合以及管理多台电脑。
