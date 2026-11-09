---
title: "架构概览：Hub、Node 与工作台"
description: "理解 sous 的事件溯源内核、Hub 与 Node 的安全边界、工作台实体模型、分层自动审批以及主管闭环机制。"
group: "development"
order: 1
sourceDoc: "docs/architecture/workbench.md"
updatedAt: "2026-11"
---

`sous` 取自法餐后厨的 *sous-chef*（副主厨）：你是主厨，只跟副主厨讨论需求和做关键决定；它替你管理项目、挑选合适的「agent + 模型」预设、把任务派发到对应的开发机或服务器上执行，并在需要审批或完成时向你汇报。

本文综合整理自仓库 `docs/architecture/workbench.md`、`docs/architecture/supervisor-context.md` 及核心决策记录（ADR `0001`、`0002`、`0019`、`0024`、`0025`）。

## 1. 仓库与应用拆分

仓库采用 [wokspace](https://wokspace.wxian.cc) 规范组织，核心代码拆分为独立的应用目录：

| 应用 | 职责 | 技术栈 | 构建产物 |
|---|---|---|---|
| `apps/core` | 服务端 `sous-hub` + 客户端 `sous`（daemon / CLI / MCP） | Go, SQLite（纯 Go `modernc.org/sqlite`） | `build/core/` |
| `apps/web` | 跨端工作台与对话 PWA（桌面三栏 + 手机端 Liquid Glass） | React 19, TypeScript, Vite, Tailwind CSS | `build/web/` |
| `apps/desktop` | 桌面托盘与窗口壳（内嵌 `build/web`） | Go, Wails v3 | `build/desktop/` |
| `apps/site` | 官方网站与文档站 | Astro, Tailwind CSS | `build/site/` |

应用之间不直接引用彼此源码，仅通过 `docs/api/` 下的三份契约通信：`core-hub-http`、`core-hub-node` 与 `core-daemon-ipc`。

## 2. 核心原则：Hub 只动脑，Node 才动手

由于 `sous-hub` 部署在公网可达的服务器上并持有模型 API Key 与历史事件，`sous` 在架构上严格隔离思考与执行（见决策 `0002`）：

- **Hub 负责思考与协调**：调用大语言模型、维护单一事实来源的事件日志（`eventlog.Log`）、联网搜索与抓取（带内网地址阻断）、管理长对话分段摘要、运行审批 agent 与巡检 agent、通过 Web Push 发送通知。**Hub 绝不执行 shell 命令、不直接读写代码仓库。**
- **Node 负责本地执行**：运行在你的开发机或服务器上，主动发起 WebSocket 连接至 Hub（本机零入站端口）。Node 通过 **ACP（Agent Client Protocol）** 统一调度 Claude Code、Codex、Cursor、OpenCode、Kiro 等编程 agent，并在本地再次强制校验权限模式、目录范围与底线规则。即使 Hub 被攻破，也无法绕过 Node 的本地安全策略。

## 3. 工作台的五个核心实体

工作台围绕五个核心概念运转（见 `docs/architecture/workbench.md`）：

1. **项目（Project）**：对应一个 git 仓库（以规范化远端地址为身份标识，外加内置的 `misc` 杂项项目），记录它在哪些机器的哪个目录、默认预设以及服务器部署目标。
2. **预设（Preset / Persona）**：常用的「agent + 模型 + 思考强度 + 默认权限模式」组合（如主力深度推理、快速修改、代码验收）。预设与具体电脑解耦，主管根据当前在线且已登录该 agent 的机器自动挑选。
3. **任务（Task）**：管理单元。没有僵化的固定流水线（决策 `0022`），由主管按场景决定：
   - **在哪儿做**：`worktree`（独立 git 分支与工作树，不碰原工作区）、`in_place`（在项目目录就地修改，自动拍快照支持一键恢复）或 `scratch`（临时目录）。
   - **是否验收（`review`）**：默认无需验收（`none`）；大改动可选模型看 diff（`ai`）、派验收 agent 跑只读测试（`agent`）或交由用户验收（`user`）。
4. **运行（Run）**：一次具体的 ACP 会话执行。除工作 Run 外，还包括只读常驻的**项目顾问（`consult`）**与受控的 **git 操作（`git`）**。
5. **机器（Node）**：开发机（`workstation`）或服务器（`server`），各自配置工作空间目录与并发槽位数。

## 4. 从讨论、执行、审批到交付的闭环

```
[用户 ↔ 主管讨论]
   ├─ 查代码 / 追问 ──► project_read / machine_look / 项目顾问 (consult, 只读常驻)
   └─ 确定动手 ──────► 创建并派发任务 (worktree / in_place / scratch)
                           │
                           ▼
               [Node 启动编程 Agent (ACP)]
                           │
        ┌──────────────────┼──────────────────┐
        ▼                  ▼                  ▼
   权限审批请求        sous mcp 通道       结构化结果汇报
 (底线/硬信号/AI审批)  (报进度 / 问主管)   (## 结果 → 叫醒主管)
```

- **先查后做**：只问一两个文件时，主管用 `project_read` 读取或用 `machine_look` 在只读档位下看一眼机器状态；需要通读代码库、连续探讨方案时，主管调用只读的**项目顾问（`consult`）**，讨论成熟后可带着顾问已读过代码的会话（`from_consult`）无缝转为正式任务。
- **三层权限与自动审批（决策 `0005`、`0019`、`0024`）**：
  1. **Node 本地底线规则**：命中高危正则（删主目录、格式化等）一律当场拒绝。
  2. **确定性规则与硬信号**：涉及凭据、外传、改远端、提权、受保护分支等硬信号一律交用户审批；`auto` 模式下常规开发操作自动放行并留痕。
  3. **审批 Agent**：对无硬信号的请求做风险判断，高把握时自动放行，信息不足时拒绝并提示 agent 先确认上下文，拿不准时交由你在工作台卡片或手机通知上一键「允许 / 拒绝」。
- **子 Agent 主动沟通（`sous mcp`，决策 `0025`）**：Node 为每个工作 Run 挂载本地 stdio MCP 服务，提供 `report_progress`（更新任务卡进度）、`ask_supervisor`（遇到分歧向主管或用户提问并等待回答）与 `report_result`（提交包含「做了 / 改动 / 验证 / 风险 / 需决定」的结构化结果）。
- **按需 Git 交付**：任务完成后改动留在独立分支或就地目录，触发事件叫醒主管汇报。只有当你明确要求时，才执行 `git_commit`、`git_merge`（仅快进合并）、`git_push` 或 `git_pr`。

## 5. 主管长对话上下文与缓存设计

为了支持跨天、跨周的长期连续对话而不丢失细节、也不破坏大模型 KV 缓存（见 `docs/architecture/supervisor-context.md` 与决策 `0020`）：

- **前缀只追加**：系统提示词与工具定义保持完全静态；当前时间、在线机器、激活项目、任务状态与召回记忆全部放入 `prompts.Tail`，仅追加在最新一条用户消息末尾的独立 `Part` 中，保证同一分段内历史前缀字节级不变，最大化命中上游模型的 Prompt 缓存。
- **分段结构化摘要与原文查回**：当未摘要消息接近软阈值时，后台自动将旧消息折叠为包含「已做的决定 / 待办和承诺 / 用户的偏好和纠正 / 进行中的话题 / 已完成或作废 / 其他背景」的结构化摘要（每条带 `[#seq]` 编号）；若需要核对早期某条命令或报错原文，主管可通过 `thread_search` 与 `thread_read` 从 SQLite 事件日志中精确查回原始对话。
