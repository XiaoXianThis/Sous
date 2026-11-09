# 设计

## 1. 数据

话题是同一条线程里的一个标签，不是新线程：主管的上下文、整理和摘要都还是按线程（决策 0020）。

- `messages.topic_id`、`messages.task_id`（迁移追加）。`MessagePayload` 加 `topic_id`、`topic_title`；`TriggerPayload` 加 `topic_id`、`quote`（agent 提问的原话）。
- 表 `topics(id, thread_id, title, summary, status, first_seq, created_at, updated_at)`，`status` 是 `open` / `closed`。
- **隐式开话题**：用户消息、trigger、`report` / `notice` / `inspector` 消息没有 `topic_id` 时，自己开一个话题，话题 id 就是消息 id，标题取 `topic_title`，没有就取正文第一行（最多 30 字）。不需要单独的“开话题”事件，投影是确定的。主管自己的回复（普通、step、silent、consult）不开话题。
- 有 `topic_id` 的消息刷新话题的 `updated_at`；用户消息、trigger 和 `report` / `notice` / `inspector` 会把已关闭的话题重新打开（有新动静），主管自己的回复不会（否则关闭后的那句回复又把它打开）。
- `topic.updated {topic_id, title?, summary?, status?}`：改标题、当前结论、开关。执行者是主管或用户。
- `message.moved {message_id, topic_id, from}`：并入和撤销并入。
- `message.seen {message_ids}`：临时事件（不入库），主管开始处理或插进当前回合时发。
- 任务：`tasks.topic_id`。`task_create` 取当前回合的话题（`tool.Turn.TopicID`），子任务继承父任务的。没有话题的任务（用户在页面上建的）第一次汇报时，汇报消息自己开话题，同一个事务里 `task.updated` 把 `topic_id` 设成它，之后的汇报都进这个话题。
- 汇报（`Engine.post`）、叫醒（`wake` / `tell`）、agent 的提问（`askTrigger`）都带任务的话题。`supervisor.notice` 带当前回合的话题。

## 2. 主管的回合

- `pending(history)`：用户消息（含 trigger）被“同一话题里、`reply_to` 不小于它的 seq 的一条非 step、非 consult 的助手消息”覆盖才算回复过。没有话题的旧消息算一个组（`""`），规则不变。
- 每轮选最早的一条未回复消息所在的话题 T，`upTo` 是 T 里最新的未回复消息。一轮结束后还有别的话题没回复，worker 接着跑下一轮。
- 历史里每条有话题的用户消息前加 `【话题#N】`（N 是话题的 `first_seq`，不会变；标题会改，所以不放进历史）。【本轮环境】里写本轮的话题（标题、当前结论、它的任务）和其他还在等回复的话题，并说明“这一轮只回答话题#N”。
- `clipAnswered` 和 `trim` 按每条消息自己有没有被回复判断，不再用全局的最大 `reply_to`。

### 插话（R3、R4）

- 回合开始时记下 `cursor`（历史里最大的 seq），并对本轮要处理的消息发 `message.seen`。
- 每次调用模型之前（第一步之后）读 `cursor` 之后的新消息，只看用户亲手发的（`role = user`、`kind = ""`）：
  - 话题是 T：作为补充插入，`upTo` 跟着推进；
  - 自成新话题且只有它一条：作为“可能的补充”插入，附上 `message_id`；主管调用 `topic_attach` 才并入并推进 `upTo`；
  - 其他：不插，留给它自己的回合。
- 插入的文字追加在最后一个工具结果的末尾（和 `wrapUpNote` 一样），各家接口都接受；不入库，下一轮从历史重建。
- 模型返回了工具调用、这期间又有要插入的新消息、并且调用里有副作用的工具（不在只读名单里的，包括 MCP 工具）时：这一批调用都不执行，每个都回“未执行：用户刚发来新消息”，把新消息插进去，让模型重新决定。只读工具名单：`task_list`、`task_result`、`thread_search`、`thread_read`、`project_read`、`git_status`、`memory_search`、`web_search`、`web_fetch`、`current_time`、`machine_look`、`mcp_list`、`schedule_list`，以及话题工具。

### 话题工具（主管内置，和 `stay_silent` 一样由回合自己处理）

- `topic_attach {message_id}`：只接受本轮插进来的“可能的补充”。发 `message.moved`，推进 `upTo`。
- `topic_update {title?, summary?}`：改当前话题的标题或当前结论（一两句现状）。
- `topic_close {summary}`：关闭当前话题，留一句总结（写进 summary）。
- 这三个工具在每轮的工具列表末尾、顺序固定，不影响缓存。

### 连发（R5）

`Options.Gather`：空闲线程收到用户消息后等这么久再开始（Hub 设 1.5 秒，0 表示不等）。线程正在跑时立即发信号，新消息由插话或下一轮处理。连发的消息在全局输入框里各自开了话题，所以回合开始时把本话题之后到的、自成一个话题的用户消息也作为“可能的补充”提供给主管（`offerFresh`），和插话一样由 `topic_attach` 决定。

## 3. 接口

- `POST /threads/{id}/messages` 加 `topic_id`（可选）：必须是这个线程的话题；不填就开新话题。
- `GET /threads/{id}/topics`：话题列表（标题、状态、当前结论、`first_seq`、最新一条消息的摘要、消息数、它的任务（完整的 Task）、`updated_at`），按 `updated_at` 倒序，最多 200 个。
- `GET /threads/{id}/messages?topic=<id>`：某个话题的消息（`topic=` 空串取没有话题的旧消息）。
- `PATCH /topics/{id}`：`{title?, status?}`，用户改标题、关闭和重新打开。
- `POST /messages/{id}/move`：`{topic_id}`，撤销并入：只能移回它自己开的话题（`topic_id` 等于消息 id），移回后它没被回复，主管会单独处理。
- `GET /topics/{id}`：读一个话题（推送链接用它找到线程）。
- `GET /tasks/{id}/talk`：主管 ↔ 子 agent 的沟通（§5）。
- 设备令牌（`/device/ws`）照旧按前缀过滤：`message.*` 已经允许；`topic.updated` 不发给设备。

## 4. 推送（R11）

`notify.message`：消息和任务状态的推送，消息或任务有话题时 tag 用 `topic-<id>`（同一话题的通知互相替换），链接是 `/#topic=<id>`。审批照旧。

## 5. 主管 ↔ 子 agent 的沟通（R12）

`GET /tasks/{id}/talk` 按事件顺序列出（`events.task_id` 加索引）：

| 来源 | 显示 |
|---|---|
| `run.queued`（非 git） | 主管 → agent：派发 / 返工 / 验收 / 交付，正文是完整的提示词 |
| `run.message` | 主管或你 → agent：追加指令 |
| `run.update` 状态以“进度：”开头 | agent → 主管：进度 |
| `trigger.fired`（`ask:` 开头） | agent → 主管：提问（`quote` 是原话） |
| `run.ended` | agent → 主管：结果（总结），失败时带原因 |
| `task.reviewed` | 验收 → 主管：通过 / 不通过和理由 |
| 助手消息 `kind: report` | sous → 主管和你：汇报 |

每条：`{seq, ts, from, to, kind, title, text, run_id, agent}`。`from` / `to` 是 `supervisor`、`user`、`agent`、`reviewer`、`sous`。

## 6. 界面

- `ChatView` 多一个视图开关（话题 / 时间线，记在 localStorage），话题视图是新组件 `TopicList`：
  - 顶部“需要你处理”：`wb.inbox` 里要用户批的审批和收件箱任务，点开任务详情；没有时不显示。
  - 话题卡片 `TopicCard`：按 要你处理（话题有 `attention` 态的任务）> 进行中（有未结束的任务、正在回答、或者开着）> 已结束（关闭，默认折叠）排，同组按 `updated_at`；未读点（`localStorage` 记每个话题看过的最新 seq）。
  - 展开的卡片：任务块（每个任务一行状态 + “主管 ↔ agent 的沟通”展开 `TalkLog`）、话题里的消息（汇报收成一行）、话题内的输入框；卡片头上有改标题、关闭 / 重新打开。
  - 全局输入框发的消息开新话题；`message.moved` 之后在原位置留一行“已并入「T」· 撤销”。
  - 用户消息下面：未回复且没看到是“排队中”，看到了是“已看到”。
- 打开卡片时按需拉 `messages?topic=`；话题列表按 `topicsRev`（`topic.updated`、`message.*`、任务事件）去抖重拉。
- `#topic=<id>` 打开聊天栏并展开那个话题。
- `TalkLog` 组件在 `TaskView` 里也有一份（执行记录上方）。
- “需要你处理”默认收起，只显示一行数量。
