# message-hygiene：消息累积、话题折叠、"需要你处理"与聊天卡片滚动 — 诊断与方案

状态：诊断完成，方案待用户拍板。未改任何产品代码。

## 0. 需求原文

> 我讨厌消息累积，能不能让 agent 自己清理消息，并且加个兜底机制（最多积压 7 条，多的折叠；可以快速结束/归档；超过 1 天未处理就收起）？更早的对话可以留，但是不再在聊天对话界面里；"需要你处理"里面我点进去都不知道怎么处理，已完成的也还一直在这里展示（是服务的遗留数据问题吗），"进行中"还会有重复的任务，而且也不是我触发的（是系统？）；还有聊天框任务展开之后内容过长，要滑动才能找到底部聊天框和顶部收起区域

## 一句话结论

1. **话题只增不减是设计问题。** 话题只能由主管调 `topic_close`，或用户手动点“结束话题”来关闭。主管对自动通知调 `stay_silent` 时，话题会一直开着；系统也没有任何按时间收起的机制。
2. **"进行中"里的“重复、不是我触发的”多半是系统自己开的话题。** 跟进、定时任务、并行改同一文件的提醒都不带话题 id，**每次都新开一个话题**，标题还是固定的“跟进任务”“Run 超时”等。另外有一个真 bug：git 操作的汇报会给任务**另开一个话题，并把任务从原话题挪走**（`task/git.go:369`）。
3. **"需要你处理"里出现已完成的任务，是查询条件本身造成的，不是数据残留。** 后端 inbox 把“已完成且开了 PR、没标记已处理”的任务算进来（`task/routes.go:334-339`），前端却给它们显示“已结束”标签，也不说为什么。失败/受阻的任务在界面上**没有“标记已处理”按钮**，只能重试或取消。
4. **展开卡片后需要来回滚动是布局问题。** 展开的话题卡片没有高度上限，标题栏也不是 sticky；话题自己的回复框放在卡片内容最后，所以内容一长，收起按钮和回复框不可能同时在视口里。
5. **实际数字：拿不到。** hub 在云服务器 `/opt/sous/data/hub.db`（约 46MB，WAL 约 4MB），主管不允许从本机连生产库。所有统计都写成了只读 SQL（`queries.sql`），已在按 `migrate.go` 建出的空库上跑通；第 1.4 节的数字全部标为**待跑**。

---

## 1. 话题与消息从哪来

### 1.1 “没有话题的消息会自动开话题”的规则

- 后端：`apps/core/internal/store/topics.go:36-47` `startsTopic`：`role == "user"`（用户消息和**所有 trigger**，trigger 写入时 role 也是 user，见 `store/auto.go:53`），以及 `kind` 是 `report` / `notice` / `inspector` 的消息。主管自己的回复（kind 为空、`step`、`silent`）不会开话题。
- 新话题的 id 就是这条消息的 id，状态是 `open`（`topics.go:78-87`）。
- 标题：`TopicTitle(title, content)`（`topics.go:54-71`）优先用调用方给的 title，没有就用正文第一行非空内容，压成一行，截到 30 个字符（`TopicTitleRunes`）后加“…”，空了就叫“新话题”。trigger 的 title 是固定字符串，比如“跟进任务”，所以**同类 trigger 开出的话题全都同名**。
- 重新打开：同样这几类消息落进一个已关闭的话题时会把它重新打开（`reopensTopic`，`topics.go:51`，`UPDATE ... status = CASE WHEN ? = 1 THEN 'open'`）。
- 前端照搬了同一规则：`apps/web/src/lib/topics.ts:9-17`。

### 1.2 各来源：代码位置、触发条件、频率、会不会新开话题

“新开话题？”一列：**是** = 一定新开；**看情况** = 任务已经有话题就并进去，否则新开。

| 来源 | 代码位置 | 触发条件 / 频率 | 消息形式 | 新开话题？ |
|---|---|---|---|---|
| 任务汇报（完成/失败/受阻/中断/部署失败） | `task/advance.go:133,196,227,325,468,806` → `post` `advance.go:618-652` | 任务状态变化时，每次一条 | `message.assistant` kind=report，带 `TopicID: t.TopicID` | 看情况：任务没话题时（工作台建的任务）开一个，并写 `task.updated topic_id`（`advance.go:631-639`） |
| 【任务进展】叫醒主管 | `advance.go:651` `e.wake(... "report:"+msgID ...)` | 每条汇报跟一个 trigger | trigger kind=task，`TopicID: e.topicOf(t)` | 否（跟随任务话题） |
| 【任务受阻】依赖失败 | `task/engine.go:715` | 依赖任务失败或取消，每对（任务、依赖、状态）一次 | trigger kind=task | 看情况（任务话题） |
| 【任务在等机器】 | `engine.go:731` | 派不出去且原因变化时，每个原因一次；任务状态 queued+“等待机器” | trigger kind=task | 看情况 |
| 【任务派到了别的机器】 | `engine.go:886` | 路由回退到别的机器，每台机器一次 | trigger kind=task | 看情况 |
| 【agent 的问题】 | `nodehub/peers.go:97-127` | agent 调 `ask_supervisor`，每问一次 | trigger kind=task，`TopicID: TaskTopic(thread, task)` | 看情况（没有任务的 Run 会新开） |
| git 操作结果（提交/合并/推送/开 PR…） | `task/git.go:333-335` → `postThread` `git.go:369-375` | git op 结束、且没有工具在同步等结果时（比如等审批、超时） | kind=report | **是，而且是 bug**，见 1.3 |
| 部署分支有新提交 | `task/workbench.go:552` → `postThread(thread, "", …)` | 每次检测到部署分支新提交 | kind=report | **是**（没有任务） |
| 项目 clone/创建结果 | `workbench.go:470,484` `say` | 每次 clone/创建 | kind=report，不带话题 | **是** |
| 【跟进】问题没回复 | `trigger/trigger.go:213-227` | 每 5 分钟扫一遍，主管提问 8 小时没回，每条消息一次 | trigger kind=followup，**不带 TopicID**（`fire` 只传 title，`trigger.go:166-183`） | **是**，标题“问题没有回复” |
| 【跟进】任务受阻/失败太久 | `trigger.go:229-248` | 受阻或失败 4 小时且没标记已处理，每个（任务、状态、原因哈希）一次 | 同上 | **是**，标题“跟进任务” |
| 【跟进】Run 超时 | `trigger.go:250-264` | Run 跑满 2 小时，每个 Run 一次 | 同上 | **是**，标题“Run 超时” |
| 【跟进】审批在等 | `trigger.go:266-273` | 用户审批等 2 小时，每个审批一次 | 同上 | **是**，标题“审批在等” |
| 定时任务 | `trigger.go:145-161` | 到点就触发，每次 | trigger kind=cron，不带 TopicID | **是**，每次都新开，**每天同名** |
| 并行任务改到相同文件（overlap） | `task/overlap.go:62-108`，`main := store.Task{}` 后 `e.followup` | 验收前比对文件，每对任务一次 | trigger kind=followup，任务是空的，所以 `TopicID` 为空 | **是**，标题“并行任务改到了相同的文件” |
| 巡检 finding | `inspector/inspector.go:318-345`（每 10 分钟）；`nodehub/nodehub.go:580-600`（重复调用规则，即时） | 规则命中且模型判断要告诉用户 | kind=inspector，`TopicID: TaskTopic(main, task)` | 看情况：token 告警这类没有 Run/任务的、或任务话题不在主线的，**新开** |
| 主管 `notify` 工具 | `tool/notify.go:30-55` | 主管主动提醒 | kind=notice，只有回合在主线时才带当前话题 | 看情况：回合不在主线（项目对话）就**新开** |
| 主管连续自动推进到上限 | `supervisor/supervisor.go:322-328`、`notice` `:582-594` | 自动推进到 `MaxAutoTurns` | kind=notice，跟当前话题 | 否（同上例外） |
| 上下文整理连续失败 | `supervisor/compact.go:195-204` | 连续 3 次失败 | kind=notice，**不带话题** | **是** |
| 机器更新结果 | `nodehub/conn.go:545-565` | 每台机器每次更新 | kind=notice，不带话题 | **是**，标题“机器「X」已经安装 vY…” |
| 主管自己的回复 | `supervisor/supervisor.go` `reply`/`store` | 每个回合 | kind 为空/step/silent | 否（跟回合话题，不会开或重开话题） |

### 1.3 为什么话题只增不减

- **关闭途径只有两条**：主管调 `topic_close`（`supervisor/topics.go:309-319`，提示词 `prompts/prompts.go:96` 要求“任务都结束、没有等用户决定的事时”关闭），或用户 `PATCH /api/v1/topics/{id}`（`httpapi/topics.go:78-128`，前端只在卡片**展开后**显示“结束话题”按钮，`TopicList.tsx:269-278`）。
- **`stay_silent` 不会关话题**：`supervisor.go:453-455` 只写一条 kind=silent 的回复。提示词要求主管对【跟进】、【任务进展】等“没有要做的事就 stay_silent”（`prompts.go:86-87`、`overlap.go:144-146`、`trigger.go:198`）。结果是：系统开出的话题收到一句静默回复后**永远停在 open**。
- **没有任何按时间收起的逻辑**：trigger 循环里只有 90 天的 Run 日志归档（`trigger.go:110-115`），不碰话题。
- **话题 bug：git 汇报会抢走任务的话题**。`postThread`（`task/git.go:369-375`）构造的是 `store.Task{ID: taskID, OriginThreadID: thread}`，**TopicID 为空**。到了 `post`（`advance.go:631`），`t.TopicID == "" && t.ID != ""` 成立，于是新开话题，并写一条 `task.updated topic_id = <新消息 id>`，任务被挪到新话题。旧话题里还留着这个任务之前的汇报和对话，只是不再有任务卡片。用户看到的就是**同一个任务的两个话题**，后一个由“合并完成/推送完成”之类的系统汇报开出，“不是我触发的”。触发条件是 `!e.waiting(r.ID)`（`git.go:333`）：git op 结束时没有工具在同步等结果，比如推送或开 PR 要等用户审批，或者超过了工具的等待时间。（`advance.go` 里其他调用都传完整的 `store.Task`，带着 TopicID，不受影响；`overlap.go:154-164` 的 `topicOf` 就是为了绕开这类陈旧 TopicID 才写的，`post` 没用它。）

### 1.4 数据统计（**待跑**）

主管确认：hub 在云服务器（43.142.103.28）`/opt/sous/data/hub.db`，46,452,736 字节，另有 hub.db-wal 4,165,352 字节。本机没有凭据，也不应该连生产库，所以没有实际数字。统计 SQL 在 [`queries.sql`](queries.sql)，开头是 `PRAGMA query_only = 1`，全部是 SELECT。我用 `migrate.go` 的全部 19 段迁移建了一个空库，17 条查询都能执行、没有语法或列名错误。

| 问题 | 查询 | 结果 |
|---|---|---|
| 现有话题数 / 未关闭数 | Q1、Q2 | 待跑 |
| 按来源分的话题条数（user / trigger:cron / trigger:followup:task / overlap / report / notice / inspector …） | Q3 | 待跑 |
| 最老的未关闭话题年龄 | Q4 | 待跑 |
| 未关闭且超过 24 小时没动的话题 | Q5 | 待跑 |
| 单个话题最多消息数、平均值 | Q6 | 待跑 |
| 没有绑定任务的未关闭话题 / 只有“触发+静默”的话题 | Q8、Q9 | 待跑 |
| 同名的未关闭话题（看起来重复的） | Q10 | 待跑 |
| 同一任务的汇报分散在多个话题 / 任务话题被改过的次数（1.3 的 bug） | Q11、Q12 | 待跑；列出的 task_id 和 topic_ids 就是第 2.3 节要的实例 |
| “需要你处理”按入选原因分，其中“done+PR” | Q13 | 待跑 |
| 话题已关闭或任务已标记已处理，但前端仍算“要你处理”的 | Q15 | 待跑 |
| 最近 7 天每天新开话题数（按来源） | Q16 | 待跑 |

拿到结果后，把数字填进这张表，第 5 节一次性清理的阈值也可以照着调。

---

## 2. 前端“需要你处理”和“进行中”

聊天主栏（`ChatView` 话题模式 → `TopicList`）有两块同时显示：

### 2.1 顶部“需要你处理 · N”卡片（`NeedsYou`）

- 前端：`apps/web/src/components/TopicList.tsx:148-182`。数据是 `wbState.inbox`，`GET /api/v1/inbox`（`lib/api.ts:116`、`lib/app.ts:124-134`）。前端只对审批再过滤一次（`review === 'user' && status === 'pending'`，`:151`），任务原样显示。
- 后端取数：`apps/core/internal/task/routes.go:297-343`：
  - 审批：`status = pending`（全部，前端再挑 review=user）。
  - 任务：`blocked`、`failed`、`queued` 且原因以“等待机器”开头、`review` 且原因含“等你验收”，排除 `dismissed` 和已归档项目（`:303-327`）。
  - `running`、`waiting_approval` 且已暂停（`:328-333`），**不看 dismissed**。
  - **`done` 且 `pr_url != ""` 且没有 dismissed（`:334-339`）**。
- **“已完成的还一直在这里”**：就是最后这一条。开过 PR 的任务完成后会**一直**留在“需要你处理”，直到用户在任务详情页底部点“标记已处理”（`TaskDetail.tsx:367-369`）。这一行用 `TaskPill`→`taskState`（`lib/status.ts:62-82`）画状态，`done` 显示成“**已结束**”，旁边的 `status_reason` 通常也说不清还要做什么，看起来就是“已完成却还要我处理”。所以这**不是数据残留，是查询条件故意这样写的，但界面没有说明原因**。PR 有没有合并，后端也没回查。
- **“点进去不知道怎么处理”**：每一行只做 `go({ view: 'task', id })`（`:166,174`），打开的是完整的任务编辑页。
  - 失败或受阻的任务：`isOpen(t)` 对它们为真（`lib/tasks.ts:26-28`，只有 done/cancelled 不算 open），所以**不显示“标记已处理”**，只有“重试”和“取消任务”（`TaskDetail.tsx:359-366`）。想“知道了，先不管”做不到，它会一直在这里，4 小时后还会触发一个“跟进任务”新话题（1.2）。
  - 等待机器：页面上没有“换机器 / 换 agent”的快捷入口，只能自己改表单再“重新派发”。
  - 已完成+PR：“标记已处理”是表单按钮行里的一个小链接，也没提示“PR 合并后点这里”。
- 计数在两个地方：卡片头 `approvals.length + tasks.length`（`:159`）；`Shell.tsx` 的 `pending` 徽标只算 `review === 'user'`，不看 status（后端已经只给 pending，结果一样）。

### 2.2 话题分组“要你处理 / 进行中 / 已结束”（`topicGroup`）

- `apps/web/src/lib/topics.ts:48-55`：
  - 话题里**任一任务** `taskState === 'attention'`，或有该任务 pending 的用户审批，就进“要你处理”。这一步**在判断话题是否关闭之前**，所以已关闭的话题也会被拉回来；它也**不看 `dismissed`**，标记已处理的失败任务照样让整个话题留在“要你处理”。
  - 否则，`status === 'closed'` 的进“已结束”（默认折叠，`TopicList.tsx:109-121`）。
  - **其余所有 open 话题都进“进行中”**，不管有没有任务、是谁开的。
- 所以“进行中 · N”数的是**话题**，不是任务。1.2 里系统开的话题（跟进、定时、overlap、机器更新、巡检、git 汇报）只要没被主管关掉，就都算在里面。“不是我触发的”就是这个原因：**它们是系统和主管开的话题**。
- 主管正在处理或排队的话题（`busy`）即使已关闭也会显示在“进行中”（`topics.ts:64-66`），这一点没问题。
- 列表最多 200 个话题（`httpapi/topics.go:15` `TopicLimit`），按 `updated_at` 倒序，更老的 open 话题会悄悄掉出列表，但仍是 open。

### 2.3 “进行中有重复的任务”是什么

不是同一条记录渲染了两次：`TopicCard` 以 `topic.id` 为 key，任务按 `tasks.topic_id` 只会归到一个话题（`store.TopicTasks`，`topics.go:260-283`）。是**内容相同或高度相似的多条独立话题**，有三种：

1. **同名的系统话题**：每次跟进、每次定时任务、每对 overlap 都新开一个，标题都是固定串（“跟进任务”“Run 超时”“审批在等”“问题没有回复”“并行任务改到了相同的文件”、定时任务的标题）。同一任务失败 → 重试 → 再失败，原因哈希变了，就会再来一个“跟进任务”（`trigger.go:245`）。
2. **git 汇报抢走任务话题的 bug**（1.3）：同一个任务对应两个话题，一个是原始对话，一个是“合并完成”之类的汇报，而任务卡片只在后一个里。
3. **同一件事在两个区域各出现一次**：失败或受阻的任务同时出现在顶部 `NeedsYou` 和下面的“要你处理”话题组里。这不算 bug，但看起来像重复。

实际的 topic id / task id：要等 Q10（同名话题）、Q11 和 Q12（同一任务多个话题）跑出来。本机拿不到数据，这里不编例子。

---

## 3. 话题卡片展开后的滚动和布局

结构：`ChatView`（`apps/web/src/components/ChatView.tsx:342`）是 `flex flex-col min-h-0 h-full`。中间的 `.chat-scroll` 是 `flex-1 overflow-y-auto`（`:394`），**整个话题列表就在这一个滚动容器里**；底部的主输入框 `<form className="chat-composer">`（`:435`）是它的兄弟节点，桌面端一直固定在底部。手机端是 `position:absolute` 浮在底部（`app.css:503`）。所以主输入框本身没有被滚走。

用户说的“底部聊天框”和“顶部收起区域”，都在**卡片内部**：

- **卡片没有高度上限，也没有内部滚动**：`TopicCard` 的 `<article className="card grid ...">`（`TopicList.tsx:242`）和展开后的内容 `<div className="grid ... gap-4 px-3.5">`（`:288-327`）都没有 `max-height` 或 `overflow`。依次是：当前结论 → 每个任务的块，含 `TalkLog`（展开后每条只在 `Clamp max=320` 里截断，条数不限，`TalkLog.tsx:58-99`）→ **话题的全部消息**（`own.map`，`:311-313`，普通消息不截断；汇报默认一行，展开后 `Clamp max=280`）。高度随内容增长，只能靠外层 `.chat-scroll` 整体滚动。
- **“顶部收起区域”被滚出视口**：收起靠卡片 `<header>`（`:243-279`，chevron、改标题、结束话题都在这里），它是普通流内元素，**不是 `position: sticky`**。往下看内容时，header 跟着卡片顶部滚出 `.chat-scroll` 的可视区域。
- **“底部聊天框”要滑到最下面才能找到**：话题自己的回复框 `TopicReply`（`:325`、`:383-424`）渲染在**所有消息之后**，同样是普通流。展开时也没有任何滚动定位：点 header 只是 `toggle`（`:92`），不会把卡片滚到合适位置；只有从推送或链接打开时才 `scrollIntoView({ block: 'nearest' })`（`:83-86`）。卡片比视口高时，`nearest` 只保证一端可见。
- 话题模式下“跟随到底部”被关掉了（`ChatView.tsx:150-153` `if (topics) return`），这是对的。但手机端两个 ResizeObserver（`:110-133`）在 `stick.current` 为真时仍会 `scrollTo(scrollHeight)`。`setMode` 把 `stick` 设成 true（`:101`），用户还没滚动过时，输入框高度一变（比如键盘弹出），列表就会被拉到最底下，造成“滚动失控”的感觉。属于次要因素。

根因：**卡片展开后高度不受约束（没有 max-height 或独立滚动区），header 和回复框都不是 sticky，只能共用外层 `.chat-scroll` 的一个滚动条。**

---

## 4. 问题分类与优先级

| # | 问题 | 类型 | 修法 | 优先级 |
|---|---|---|---|---|
| A | git 汇报新开话题并抢走任务（`git.go:369`） | **bug**（代码） | `post` 改用 `e.topicOf(ctx, t)`，或 `postThread` 先查 `TaskTopic`；已被挪走的任务可以在清理里挪回去（可选） | P0 |
| B | 跟进、定时、overlap 不带话题，每次都新开 | **设计问题**（小改） | 有任务的跟进带上 `TaskTopic`；overlap 挂到触发任务 `t` 的话题；问题跟进挂到那条消息的话题；定时任务**同一个 schedule 复用一个话题**（`schedules` 加 topic 记录，或用 `schedule:<id>` 找最近的话题） | P0 |
| C | `stay_silent` 后话题永不关闭 | **设计问题** | 话题是系统开的（首条是 trigger/notice/report/inspector）、没有未结束任务、没有用户消息在等，且主管这一轮静默时，**由代码自动关闭**，summary 写“自动收起：无需处理” | P0 |
| D | “需要你处理”里有 done+PR | **查询条件** | 改成只在 PR 未合并时出现，显示“PR 待合并”而不是“已结束”；或从 inbox 拿掉、放进任务页（需拍板，Q1） | P1 |
| E | 失败或受阻不能“标记已处理” | **前端条件** | `TaskDetail` 放开 `dismissed` 按钮；`NeedsYou` 每行直接给原因和按钮（重试 / 换路 / 忽略 / 去审批 / 打开 PR） | P1 |
| F | `topicGroup` 不看 dismissed、attention 压过 closed | **前端条件** | `dismissed` 的任务不算 attention；已关闭的话题不进“要你处理”（顶部 NeedsYou 已经单独列出这些任务） | P1 |
| G | 没有数量兜底和按时间收起 | **设计问题** | 见第 5 节 | P1 |
| H | 卡片展开后滚动失控 | **前端布局** | 见第 5.4 节 | P1（独立，可并行） |
| I | 已积压的大量 open 话题 | **数据遗留** | 一次性清理（5.5），在 A、B、C 上线之后跑 | P1，跟着 A、B、C |
| J | 机器更新、部署、clone 的通知各开一个话题 | 设计（次要） | 归入一个“系统通知”话题（按天或长期复用一个），或只推送不进主线 | P2 |

建议顺序：**A → B → C →（I 一次清理）→ F/E/D → G → H**。H 和前面的工作没有依赖，可以另起一个任务并行做。

---

## 5. 方案

### 5.1 原则

- **话题状态在后端落库**：收起、归档都要写成事件（`topic.updated`），这样投影可以重放，各设备也一致。**折叠只是显示**，由前端算。
- **“收起”和“关闭”是同一件事**：话题 status 维持两个值（`open` / `closed`），不新增第三种状态。“归档”= 关闭，并从聊天界面消失（只在“历史”里出现）。这样不用改主管的话题模型，`reopensTopic` 也照常工作：关闭的话题有新进展会自动重新打开。
- **“需要你处理”看任务和审批的状态，不看话题**。话题关了，但任务仍要处理，还是在顶部 NeedsYou 里显示，**不会因为话题被收起而丢失**。

### 5.2 后端

**数据模型（迁移 20，只加列，不需要回填）：**

```sql
ALTER TABLE topics ADD COLUMN closed_at    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE topics ADD COLUMN close_reason TEXT    NOT NULL DEFAULT '';  -- supervisor / user / idle / silent / sweep
ALTER TABLE topics ADD COLUMN origin       TEXT    NOT NULL DEFAULT '';  -- user / task / followup / cron / system（新话题写入时填）
CREATE INDEX topics_status ON topics(thread_id, status, updated_at);
```

- `event.TopicUpdatedPayload` 加一个可选字段 `Reason`。`projectTopic` 在 status 变为 closed 时写 `closed_at = ev.TS`、`close_reason`，变为 open 时清空。`topicOfMessage` 重新打开话题时也清空。
- `origin` 由 `topicOfMessage` 根据 role、kind（以及 trigger 的 kind）写入。老话题保持 `''`，查询时当作“未知”。这一列只是为了第二阶段的筛选和“系统通知”分组，**第一阶段可以不加**。
- 是否需要迁移：需要，但只加列，旧数据不受影响。以前关闭的话题 `closed_at` 是 0，排序时退回用 `updated_at`。

**自动收起（“超过 1 天未处理就收起”）：** 在 trigger 的 Followups 循环（每 5 分钟）里加一个 `SweepTopics`：

条件（全部满足才收起）：
1. `status = open`，`updated_at` 早于现在减 24 小时（可配置，`hub.yaml` 的 `topic_idle_hours`，默认 24）；
2. 话题里**没有**状态是 attention 或 active 的任务（排队、执行中、等审批、受阻、失败未标记已处理、等你验收）；
3. 没有绑定到这个话题任务的 pending 用户审批；
4. 最后一条消息不是用户发的、还在等回复的消息（与前端 `waitingIn` 同一个判断）。

动作：追加 `topic.updated{status: closed, reason: "idle", summary: 原 summary 或“24 小时没有新进展，已自动收起”}`，actor 为 system。

条件 2 里**有未处理任务的话题不会被收起**：这些任务本来就在 NeedsYou 里，话题保留更符合“需要你处理”的含义。如果用户希望“受阻 1 天也收起，只留在 NeedsYou”，把条件 2 放宽成“没有 active 的任务”即可，见拍板 Q2。

**主管自己清理（“让 agent 自己清理”）：**
- 修 C：`supervisor.go:453` 处理静默时，满足“话题首条消息不是用户发的、话题里没有未结束任务、没有等待中的用户消息”，就追加 `topic.updated{closed, reason: silent}`。
- 提示词（`prompts.go:96`）补一句：对系统通知类话题，处理完或判断不用处理时，用 `topic_close` 代替 `stay_silent`。（代码兜底已经够了，提示词只是锦上添花。）

**API 变更：**

| API | 变更 |
|---|---|
| `GET /api/v1/threads/{id}/topics` | 新增 `?status=open\|closed\|all`（默认 `open`），关闭的话题分页用 `?before=<updated_at>&limit=`；返回体加 `closed_count`，让前端显示“历史 · N”。返回的话题带上 `closed_at`、`close_reason`、`origin` |
| `PATCH /api/v1/topics/{id}` | 已有。body 可带 `reason`（默认 `user`） |
| `POST /api/v1/threads/{id}/topics/close` | **新增**，批量关闭：`{ids: [...]}` 或 `{idle_hours: 24}` / `{all_without_pending: true}`，用来做“一键收起”和一次清理；只追加事件，返回关闭的数量 |
| `GET /api/v1/inbox` | 改条件：去掉 done+PR，或改成 `kind: "pr"` 并只在 PR 未合并时返回；每个任务加 `why`（等待机器 / 受阻 / 失败 / 等你验收 / 已暂停 / PR 待合并）和 `actions`（retry / dismiss / reroute / open_pr / approve），前端照着画按钮 |
| `PATCH /api/v1/tasks/{id}` | 已经支持 `dismissed`；不用改后端，只放开前端 |
| 搜索历史 | 现在没有消息搜索的 HTTP 接口（只有主管工具）。第二阶段新增 `GET /api/v1/threads/{id}/search?q=`（messages.content LIKE + topics.title），或先只做“历史话题列表 + 标题过滤” |

### 5.3 前端

- **最多 7 条，多的折叠**（前端计算，不落库）：在 `topicCards` 之后，attention 组全部显示（要你处理不能藏）；“进行中”只显示按活动排序的前 `7 - attention 数`（至少 3）个，其余折成一行“还有 N 个话题 ▸”，点开才渲染。这样实际可见的“积压”不超过 7 个，用户刚发的或正在处理的话题（busy、focus）始终显示。
- **快速结束、归档**：
  - 收起状态的卡片在 header 右侧直接给“结束”按钮（不用先展开）；手机端左滑出现“结束”。
  - “进行中”组标题加“全部收起”：调批量 API，只关“没有待处理任务的”，返回 N 后提示“已收起 N 个，可在历史里找回”，并给撤销按钮（再 PATCH 回 open）。
- **更早的对话离开聊天界面**：“已结束”组和 `Earlier`（话题前的旧消息）从主列表移走，换成列表底部的一个入口“历史 · N”。点进去是历史视图（侧滑面板或 `view: 'history'`），里面有已关闭话题的分页列表、标题过滤、以及老的“更早的对话”。可以重新打开。
- **“需要你处理”可操作**：每行显示 `why` 和 1 到 2 个直接操作（重试 / 忽略 / 换机器 / 去审批 / 打开 PR），点标题才进任务页；done+PR 改成“PR 待合并”标签。`TaskDetail` 对失败和受阻任务也显示“标记已处理”（文案“先不管它”）。
- **分组修正（F）**：`topicGroup` 跳过 `dismissed` 的任务；已关闭的话题不再因为 attention 被拉回“要你处理”。

### 5.4 卡片展开后的布局（H）

推荐**焦点模式**：展开话题时不再在列表里原地展开，而是让 `.chat-scroll` 的内容整体切换成“这个话题的对话”。顶部一行 sticky 的话题 header（返回列表 / 标题 / 结束），中间是消息，主输入框 `chat-composer` 直接作为这个话题的回复框（复用 `onSendIn`），卡片里的 `TopicReply` 去掉。进入时滚到底，和普通聊天一样。这样“底部聊天框”和“顶部收起”天然都在视口里，手机端也最简单。

如果想保留原地展开（改动更小）：
- `TopicCard` 的 `<header>` 加 `sticky top-0 z-[2] bg-panel`（手机端 `top` 要让出玻璃 header 的 `calc(var(--safe-t)+58px)`），这样收起按钮始终可见；
- 展开内容放进 `max-h-[min(70dvh,calc(100dvh-var(--composer-h)-160px))] overflow-y-auto overscroll-contain` 的内部滚动区，`TopicReply` 放在滚动区外、卡片底部固定；展开时把卡片 `scrollIntoView({ block: 'start' })`，内部滚动区滚到底；
- `ChatView.tsx:110-133` 的两个 ResizeObserver 在 `topics` 模式下不要 `scrollTo(scrollHeight)`。

注意：卡片里套一个滚动区，手机上会出现“滚动套滚动”，所以推荐焦点模式（拍板 Q3）。

### 5.5 一次性清理（数据遗留）

在 A、B、C 上线之后跑一次，否则清掉后还会继续冒出来。做法是调用批量关闭 API（或一个 `sous-hub` 子命令），**只追加 `topic.updated` 事件，不删任何数据、不直接改表**，可以重放，也可以撤销（重新打开）。

建议条件：open 且 idle 超过 24 小时，同时满足 5.2 自动收起的条件 2 到 4。也就是说，第一次跑 SweepTopics 时它自然会把存量收掉，**不需要单独的脚本**。可以先在一份 hub.db 拷贝上用 Q5、Q8、Q9 估算会关多少个。

可选：对 Q12 找到的“被 git 汇报挪走话题”的任务，追加 `task.updated topic_id = <原话题>` 挪回去。原话题取该任务最早的 `task.updated topic_id` 之前的值，或任务创建时的 topic_id。数量少的话手动处理即可。

### 5.6 分阶段

| 阶段 | 内容 | 工作量 | 风险 |
|---|---|---|---|
| **P0 止血**（最小可用） | A：`post` 用 `topicOf`；B：跟进、overlap、问题跟进带 TopicID，定时任务复用话题；C：静默自动关闭系统话题；各补测试（`task/topics_internal_test.go`、`trigger` 测试、`supervisor/topics_test.go`） | 约 1 天，core 改 4 到 5 处，每处几行到几十行 | 低。C 要防止误关：只关首条不是用户发的、没有未结束任务、没有等待中用户消息的话题；话题会被新消息自动重新打开，误关的代价小 |
| **P1 兜底 + NeedsYou** | 迁移 20（closed_at / close_reason）；SweepTopics 24 小时自动收起；批量关闭 API；inbox 改条件，加 why/actions；前端 7 条折叠、收起状态的“结束”按钮、“全部收起”加撤销、NeedsYou 行内操作、TaskDetail 放开忽略、topicGroup 修正；跑一次存量清理 | core 约 1 到 1.5 天，web 约 1.5 天 | 中。inbox 去掉 done+PR 后，未合并的 PR 只能在任务页看到（取决于 Q1）；需要更新 `docs/api/core-hub-http.md` 契约 |
| **P1' 布局**（可并行） | 第 5.4 节：焦点模式（或 sticky header + 内部滚动） | 焦点模式约 1 到 1.5 天，sticky 方案约 0.5 天 | 中。焦点模式要处理推送链接定位、返回和手势、TopicReply 的去留；需要浏览器端到端测试（桌面、手机两种宽度） |
| **P2 完整体验** | 历史视图（分页、标题过滤、重新打开）；消息搜索 API；`origin` 列和“系统通知”合并话题（J）；手机左滑结束 | 2 到 3 天 | 低到中。搜索要注意性能，46MB 的库直接 LIKE 可以接受，以后再考虑 FTS |

---

## 6. 需要拍板

1. **done+PR 的任务还要不要留在“需要你处理”？** 推荐：只在 PR 未合并时留下，标签改成“PR 待合并”，带“打开 PR / 忽略”按钮；合并状态要后端回查（P1 先不回查，只改标签和按钮也行）。另一个选项：直接从 inbox 拿掉，只在任务页显示。
2. **24 小时自动收起要不要包括“还有受阻、失败任务”的话题？** 推荐：**不包括**，这类话题留着，任务同时在 NeedsYou 里。如果你更想让主栏保持干净，可以收起话题，只在顶部 NeedsYou 留一行。
3. **展开话题的交互**：推荐“焦点模式”（点开整屏只看这个话题，底部主输入框直接回复这个话题，顶部固定返回和结束按钮）；另一个选项是保留原地展开，加 sticky 标题和卡片内滚动（改动小，但手机上会滚动套滚动）。

## 附：本次调查没有做的

- 没有连接生产 hub，统计全部待跑（`queries.sql`）；第 2.3 节要的具体 topic id / task id 实例由 Q10、Q11、Q12 给出。
- 没有在浏览器里复现滚动问题，第 3 节的结论来自读代码（组件结构和 CSS），没有截图验证。
