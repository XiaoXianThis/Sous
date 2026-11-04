# 工作台单页：设计

需求见 `requirements.md`。本文写怎么做。只改 `apps/web`；core 的执行逻辑不动，后端只有一个可选项（7.3）。

## 1. 结构

```
Shell.svelte
├── Rail（桌面）：工作台 / 对话 / 设置
├── Workbench.svelte                     ← 取代 Overview
│   ├── WorkbenchHeader                  ← 标题、需要你处理筛选、新建项目 / 登记仓库、机器条
│   ├── ProjectCard.svelte × N           ← 瀑布流
│   │   ├── Locations.svelte             ← 机器 / 部署 / 远端 chip
│   │   ├── AgentCard.svelte × n         ← 运行中、暂停、等审批的 Run
│   │   │   └── 2 行活动流（读 wb.live）
│   │   ├── TaskRow.svelte × n           ← 待办、进行中、要你处理、已结束
│   │   └── ApprovalCard × n             ← 项目里的待审批
│   └── 空状态
├── ChatPanel（右侧，可收起）
├── Tabs（手机）：工作台 / 对话 / 设置
└── SheetStack
    ├── TaskSheet.svelte                 ← 复用 TaskView
    ├── ProjectSheet.svelte              ← 编辑、位置、部署、clone、完整任务列表
    ├── ProjectSetupSheet.svelte         ← 新建项目、登记发现的仓库
    └── RunSheet.svelte                  ← 完整活动流和操作
```

- 硬性约束：`AgentCard` 是唯一读 `wb.live[run.id]` 的组件。`Workbench` 和 `ProjectCard` 不把实时活动拼进 `projectCards()` 的结果，否则每个 chunk 都会重算整个网格。
- 项目名、任务行、agent 卡是并列的 `<button>`；项目卡本身不是按钮。
- 所有状态文字、图标、颜色继续走 `status.ts`，组件里不新造。

## 2. 路由

`lib/route.ts` 的形状基本不变，只是 `Shell` 不再切页面，而是“工作台 + 可选 sheet”：

| hash | `parseRoute` | 渲染 |
|---|---|---|
| `#/` | `overview` | 工作台 |
| `#/projects` | `projects` | 工作台（兼容旧链接） |
| `#/task=<id>` | `task` | 工作台 + 任务 sheet |
| `#/project=<id>` | `project` | 工作台 + 项目 sheet |
| `#/inbox` / `#approvals` | `inbox` | 工作台 + “要你处理”筛选 |
| `#thread=<id>` | `thread` | 聊天（不变） |

- `Shell` 对 `view` 只做两件事：设置筛选（`inbox`）和打开对应的 sheet；主体永远是 `<Workbench />`。
- 打开 sheet 走 `go()`（pushState），关闭走 `history.back()`；`popstate` / `hashchange` 已有监听，沿用。
- Run sheet 不单独占 hash（v1），用组件内的 sheet 栈；任务 sheet 里点 Run 打开它。
- 手机返回键等同于关闭最上层 sheet。

## 3. 数据

```ts
// app.svelte.ts
export const wb = $state({
  data: null as Overview | null,   // GET /overview
  inbox: null as Inbox | null,     // GET /inbox
  live: {} as Live,                // 按 run id 的活动流
  error: '',
})
```

- `/overview`：项目、机器、岗位、开放任务 + 24 小时内完成的任务、活跃 Run、待审批。
- `/inbox`：待处理数量和完整的待处理任务（包括超过 24 小时的“已完成 + 有 PR”任务）。卡片的任务列表把 `overview.tasks` 和 `inbox.tasks` 按 id 去重合并，避免旧 PR 突然消失。
- `run.options` 在 `/overview` 的 Run 里有；实时 `run.queued` 事件没有映射这个字段，所以卡片以 `/overview` 的 Run 为准。
- 没有项目的 Run 不存在：主管只能通过任务派活，`purpose: create|clone` 的 Run 用 `run.prompt` 指向项目。

## 4. 任务四态

在 `lib/status.ts` 里加（纯函数，配 `status.test.ts`）：

```ts
export type TaskState = 'todo' | 'active' | 'attention' | 'closed'

export const taskStates: Record<TaskState, { text: string; icon: string; tone: Tone }> = {
  todo:      { text: '待办',     icon: 'circle', tone: 'neutral' },
  active:    { text: '进行中',   icon: 'play',   tone: 'accent' },
  attention: { text: '要你处理', icon: 'hand',   tone: 'warning' },
  closed:    { text: '已结束',   icon: 'check',  tone: 'neutral' },
}

export function taskState(t: Pick<Task, 'status' | 'status_reason' | 'pr_url' | 'dismissed'>): TaskState
```

映射：

| `task.status` | 条件 | 四态 | badge 的补充文字 |
|---|---|---|---|
| `inbox` | | `todo` | 收件箱 |
| `planned` | | `todo` | 待开始 |
| `queued` | `status_reason` 以“等待机器”开头 | `attention` | 等待机器 |
| `queued` | 其他 | `active` | 排队 |
| `running` | `status_reason` 以“已暂停”开头 | `attention` | 已暂停 |
| `running` | 其他 | `active` | 执行中 |
| `review` | | `active` | 验收中 |
| `waiting_approval` | | `attention` | 等你审批 |
| `blocked` | | `attention` | 受阻 |
| `failed` | | `attention` | 失败 |
| `done` | 有 `pr_url` 且未 `dismissed` | `attention` | PR 待合并 |
| `done` | 其他 | `closed` | 完成 |
| `cancelled` | | `closed` | 已取消 |

- 详细状态仍然用 `taskLook()` 的图标和语气；四态只决定 badge 的文字和底色。
- 排序：`attention(0) > active(1) > todo(2) > closed(3)`，同组按 `updated_at` 倒序。
- 全局“要你处理”筛选：`inbox.approvals`（`review: user`）+ `inbox.tasks`；卡片上的 badge 用同一个函数算，两者不会各说各话。
- 测试覆盖上表每一行，以及 `done + pr_url + dismissed` 的边界。

## 5. 项目卡片与瀑布流

### 5.1 卡片模型

`lib/workbench.ts` 用 `projectCards(overview, inboxTasks)` 生成：

```ts
interface ProjectCard {
  project: Project
  locations: Location[]            // 机器、部署、远端
  agents: CardAgent[]              // 运行中 / 暂停 / 等审批
  tasks: CardTask[]                // 去掉已经在 agents 里的任务
  counts: { todo: number; active: number; attention: number; closed: number }
  rank: TaskState                   // 卡片里最需要注意的状态
}
```

- Run 归属：`run.task_id → task.project_id`；`create` / `clone` 的 setup Run 用 `run.prompt`；其余归 `misc`。
- 位置：`project.nodes`（按 `nodes` 补名称、在线、角色、系统）+ `project.deploy` + `project.remote_key` / `repo_url` 的 host。host 不写死 GitHub。
- 任务：`overview.tasks` 与 `inbox.tasks` 合并去重；已经在 `agents` 里的任务不再进 `tasks`。
- 卡片排序 `rank`：有要你处理的 0，有活跃 Run 的 1，只有待办/已结束的 2；`misc` 永远最后。同 rank 按项目名。

### 5.2 瀑布流

用**列栈**：先把卡片按顺序分到 N 列（每次放进当前估算高度最短的列），每列是一个独立的纵向网格。不用 CSS `columns`（列优先会打乱注意力顺序），也不用 `grid-auto-rows` + span（行优先会在短列下面留下大洞）。

- 列数：`columnCount(width, min = 330, gap = 14)`，容器 `bind:clientWidth` 后算出，1～4 列。
- 分配：`distribute(cards, cols, estimateHeight)`，纯函数 + 单测；每列渲染成 `<div class="col">`，卡片按分到的顺序堆叠。
- 高度估算 `estimateHeight(card)`：基础高度 + agent 数 × 96 + 任务数 × 48 + 阶段条 + 隐藏项；只用于平衡，误差不会造成重叠。
- 卡片内容高度要稳定：2 行活动流固定高度、任务最多 4 条，审批多了会正常长高（列栈会把它下面的卡往下推，不会压到别的列）。
- 手机（< 760px）：固定一列，顺序不变。
- 不重排的策略：`projectCards()` 只在 `/overview` 或 `/inbox` 重拉后重算；`wb.live` 不参与；只有宽度变化才重新分列。刷新时保留 `{#each ... (project.id)}` 的 key。

## 6. 子 agent 卡与实时活动

### 6.1 数据

`lib/live.ts`，纯函数 + 测试：

```ts
export type ActivityKind = 'text' | 'thought' | 'tool' | 'status' | 'permission'
export interface Activity { id: string; kind: ActivityKind; text: string; status?: string }
export type Live = Record<string, Activity[]>

export function applyLive(live: Live, ev: HubEvent): boolean
export function tail(acts: Activity[], n = 2): Activity[]
export function oneLine(a: Activity, max = 90): string
export function parseLogTail(text: string, maxChars = 4096): Activity[]
export function seedLive(live: Live, runId: string, text: string): boolean
```

- `run.output`：`text` / `thought` 追加；连续同类型合并到同一条，避免每个 token 一条。
- `run.update`：`tool` 按 `tool_call_id` 原位更新（标题 / 状态 / 类型）；`permission` 追加一条“自动放行 / 已拒绝”；`status` 替换最后一条状态行；`plan` 不进活动流（在详情里看）。
- 上限：每个 Run 最多 24 条活动，每条文本最多 2000 字符（保留尾部）；全局最多 50 个 Run，按最后事件时间淘汰。
- 流式节流：事件先进普通对象缓冲，每 100ms 合并进 `$state` 的 `wb.live`，避免 token 级渲染。纯函数负责合并，计时器只负责 flush。
- `run.ended` 后保留活动流，直到下一次 `/overview` 重拉发现这个 Run 不在列表里；如果 Run sheet 正开着，保留到关闭。
- 页面重连后 `run.output` 不会重放；v1 接受“从重连这一刻开始”。

### 6.2 渲染

- `AgentCard` 用 `tail(wb.live[run.id], 2)`：
  - 第 1 行：最新一条活动；工具调用用 `⚒` 一类的图标 + 标题，思考用 `bulb` 图标 + 灰一点的颜色，文本用原文。
  - 第 2 行：再往前一条；如果只有一条，第二行显示状态行或留空。
  - 每行 `white-space: nowrap; overflow: hidden; text-overflow: ellipsis`，不换行、不撑高。
- 没有活动时：运行中显示“等待输出…”，排队不显示 agent 卡。
- 第二行的 meta（岗位 · agent · 模型 · 思考强度 · 机器）：
  - agent 名：`node.agents.find(a => a.name === run.agent)?.title ?? run.agent`；
  - 模型 / 思考强度：`run.options.model` / `run.options.thought_level`，空时 `agent.defaults`，再空时 `agent.options[].current`；把 value 映射成 `values[].name`；
  - 机器：`nodes.find(n => n.id === run.node_id)?.name`。
- 审批：`overview.approvals` 里 `run_id` 对应的、`review === 'user'` 的，直接渲染 `ApprovalCard` 在 agent 卡里。

### 6.3 刷新

- `reduce.ts` 的 `ChatState` 加 `runsRev`；`run.queued`、`run.started`、`run.ended`、`run.paused`、`run.resumed` 时自增。`run.output`（`seq: 0`）不自增。
- `Shell` 的 `$effect` 监听 `tasksRev`、`nodesRev`、`inboxRev`、`runsRev`，150～300ms 去抖后 `loadOverview()`。
- 删掉 `Shell` 里基于 `chat.runs` 的 `runKey`：它只覆盖打开过的对话线程，别的线程里 Run 启动不会刷新。
- `applyEvent` 的流回调顺序：先 `applyLive(wb.live, ev)`，再 `applyEvent(chat, ev)`。`run.output` 对未知 Run 会被 `applyRunEvent` 直接忽略，所以 live store 必须在前面。

## 7. 详情层

### 7.1 详情层

- 顶层 sheet 由路由决定：`#/task=<id>` → `TaskSheet`，`#/project=<id>` → `ProjectSheet`；`#/inbox` 只是筛选。没有单独的 sheet 栈。
- 打开和关闭走 `go()` / `closeSheet()`：`go()` 从工作台进入 sheet 时 `pushState({sheet:true})`；`closeSheet()` 有 `sheet` 状态就 `history.back()`，直接打开深链接时 replace 回工作台，不会退出应用。
- 新建任务（`ui.newTask`）和 Run 详情（`ui.runSheet`）是本地覆盖层，渲染在 Shell 最外层、路由 sheet 之后，所以永远在最上面；浏览器返回时 `followHash` 清掉它们。
- Sheet 复用 `Sheet.svelte`（手机上自动变成底部全屏）；打开前记录焦点，关闭后恢复。

### 7.2 三种详情

| 组件 | 内容 | 复用 |
|---|---|---|
| `TaskSheet` | 阶段时间线、每个 Run 的步骤、diff、审批、操作、追加指令 | `TaskView` 加 `embedded` prop：去掉 `.page` 的 padding、返回按钮和整页高度，Sheet 负责滚动 |
| `ProjectSheet` | 位置（加机器 / clone）、部署目标、默认岗位、项目说明、编辑、完整任务列表、新建任务、待审批 | `ProjectEditor`、`TaskRow`、`ApprovalCard` |
| `RunSheet` | 完整活动流、工具步骤、计划、审批、暂停 / 继续 / 取消、日志、改动 | `api.run(id)` 取已存事件，`wb.live` 取实时流，`TextViewer` 看日志和 diff |
| `ProjectSetupSheet` | 新建项目、登记发现的仓库 | 从 `ProjectsList` 抽出的表单 + `ProjectEditor` |

- `TaskSheet` 打开时先 `GET /tasks/{id}`（和现在的 TaskView 一样），保证深链接进入不依赖 `/overview` 里有没有这个任务。
- `RunSheet` 的完整活动 = `api.run(id)` 重放出来的工具 / 计划 + `wb.live[id]`；两者按顺序拼接，不重复渲染工具。
- 任务 sheet 里的 Run 卡保持现状（RunCard 自己展示步骤）；`RunSheet` 从工作台的 agent 卡打开，叠在任务 sheet 上。

## 8. 旧代码的去留

| 文件 | 处理 |
|---|---|
| `components/Overview.svelte` | 由 `Workbench.svelte` 取代，删除 |
| `components/ProjectsList.svelte` | 表单抽到 `ProjectSetupSheet.svelte`，删除 |
| `components/Inbox.svelte` | 删除；审批用 `ApprovalCard`，任务用 `TaskRow`，全局筛选在 `WorkbenchHeader` |
| `components/ProjectBoard.svelte` | 删除；任务列表进卡片和 `ProjectSheet`，新建任务进 `ProjectSheet` |
| `components/TaskView.svelte` | 保留，加 `embedded` |
| `components/RunCard.svelte` | 保留（任务 sheet） |
| `lib/workbench.ts` | `rows()` 换成 `projectCards()`；保留 `personaFor`、`deployedAfter` |
| `lib/status.ts` | 加 `TaskState`、`taskStates`、`taskState()`；`looks` 继续服务详细状态 |
| `lib/tasks.ts` | `board()` / `columns` 如果没有使用者就删掉；`statusText` 保留 |
| `Shell.svelte` | 删掉五个页面的分支，只留工作台 + sheet + 聊天 + 标签 |
| `lib/route.ts` | 保留现有 hash 形式；补任务 / 项目 / inbox 的测试 |

- 删除前先 `grep` 使用点；没有使用者的导出一起删，不留兼容壳。

## 9. 性能与风险

| 风险 | 处理 |
|---|---|
| 卡片重排时跳变 | 只在去抖后的 `/overview` 重拉时排序；`wb.live` 不参与；key 用项目 id |
| 瀑布流高度抖动 | 活动流固定 2 行、任务最多 4 条、审批最多 2 条；超出进项目 sheet |
| 流式输出太密 | 100ms flush + 每个 Run 只保留尾部；只有 `AgentCard` 读 live |
| 列栈高度估算偏差 | 估算只影响列平衡，不会重叠；`estimateHeight` 纯函数 + 测试 |
| 表格里看不到旧 PR | 卡片任务列表合并 `inbox.tasks`（不受 24 小时窗口限制） |
| 深链接任务不在 overview | `TaskSheet` 自己按 id 拉取 |
| 刷新后没有历史输出 | 已实现方案 1：首次看到活跃 Run 时读日志尾部 |
| sheet 焦点和滚动 | 打开时记录触发元素和滚动位置，关闭时恢复；Esc 只关最上层 |
| 手机上卡片过长 | 一列；agent 卡仍然 2 行；任务最多 4 条 |

## 10. 后端

### 10.1 第一版：零改动

- `/overview`、`/inbox`、`/tasks/{id}`、`/runs/{id}`、`/runs/{id}/log` 已经够用。
- 四态是前端显示映射，不改任务状态机。

### 10.2 刷新后的最近输出（已实现方案 1）

1. **客户端读日志尾部（已实现）**：Run 第一次出现在工作台时，`GET /runs/{id}/log`，取最后 4 KB，按行塞进 `wb.live`（`text` / `tool`）。不改后端；日志大时多下载。只在第一次出现时读一次。
2. **给日志接口加 `?tail=<bytes>`**：Go 侧 `nodehub.Hub` 加一个 tail 读取（`Seek` 到末尾），`GET /runs/{id}/log?tail=4096`。改动小，避免下载整个日志。以后日志变大时再做。
3. **`/overview` 带 `recent`**：每个活跃 Run 返回最近几条活动。一次请求，但 Go 侧要新增读取逻辑，且和实时事件有重复。

- 思考内容不落盘（`appendLog` 对 `UpdateThought` 直接返回），任何方案都补不回来；这一点在界面上不承诺。
- 选 2 的话要同步 `docs/api/core-hub-http.md` 和 `apps/web/src/lib/api.ts`。

## 11. 实施顺序

见 `tasks.md`。每一组结束时都能跑起来：先纯函数和测试，再单页骨架，再卡片和 sheet，最后接实时活动。

## 12. 已定

- 不另做“全部待办”列表；工作台的“要你处理”筛选加上卡片里的任务行就是全部。
- 手机不做长按预览；点击进任务 sheet。
- 刷新后的最近输出用客户端读日志尾部（10.2 方案 1），思考内容不承诺。
