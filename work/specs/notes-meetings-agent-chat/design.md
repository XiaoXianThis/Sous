# 设计

决策：[0028](../../../docs/decisions/0028-direct-agent-chat.md)（直接和子 agent 对话）、[0029](../../../docs/decisions/0029-meetings.md)（会议）。逐步的执行说明见 [execution.md](execution.md)，那里是唯一的权威版本；本文只写定下来的设计和理由。

## 1. 记事本

- 表 `notes`、`note_sends`，事件 `note.put` / `note.deleted` / `note.sent`，只推给登录会话。
- 发送不另设接口：`POST /threads/{id}/messages` 的 `context.note_ids`，Hub 在同一个事务里追加 `note.sent`。`MessageContext.Empty()` 要把 `note_ids` 算进去，否则只带笔记的上下文会被丢掉。主管看到的消息里不提笔记（`DescribeContext` 不描述 `note_ids`）。
- 命令行：`sous note "<文字>"` → daemon 的 IPC `note.add` → Hub 的 `POST /api/v1/device/notes`（只能新建）。
- 界面：图标栏的“记事本”打开右侧抽屉（`Dialog`，宽 400px），快捷键 ⌘⇧N / Ctrl+Shift+N。手机上在“对话”页顶栏放一个记事本按钮。

## 2. 直接和子 agent 对话

- 单独的接口 `POST /threads/{id}/dm {content, to, topic_id?}`，由任务引擎处理（`internal/task/dm.go`）。`httpapi` 不依赖任务引擎，所以不放进 `postMessage`。私聊不支持附件。
- 对象 `to` 是三选一：`{run_id}`（正在运行的 Run）、`{task_id}`（任务的 agent）、`{consult: true}`（这个项目的顾问）。只能在项目对话里用，对象必须属于这个项目。
- 投递：
  - Run 正在运行或已暂停：`nodehub.MessageAs(…, ActorUser)`，等同现在的追加指令。agent 的回应出现在 Run 的实时输出里，私聊块里显示“已送达”。
  - 任务没有正在运行的 Run：开一个新的 Run，用途 `chat`，带最近一个工作 Run 的会话（`resume_session`），`continue_prompt` 就是用户的这句话。每句话一个 `chat` Run；`chat` Run 还在跑时，后来的话作为追加指令。
  - 顾问：调用现有的 `Engine.Consult`，回答照旧是 `kind: consult` 的消息。
- `chat` Run 在 Node 上和工作 Run 一样准备目录（同一个任务共用一个目录）、占用工位，不同点：不判断 `no_action`；接不上会话就直接失败，不发完整的任务提示；结束时上报 `outcome: changed`（目录指纹变了）或空。
- 任务状态：开始 `chat` Run 时任务转成 `running`（原因“用户在私聊”），原来的状态记在 `runs.chat_from`。结束时：有改动 → 任务转成 `done`，`git_state` 设为 `unmerged`，发一份汇报（叫醒主管）；没有改动 → 回到 `chat_from`，不发汇报。agent 的最终回复存成 `kind: dm` 的助手消息。
- 消息：用户的话存成 `role: user, kind: dm`，`source` 带 `run_id`、`task_id`、`agent`、`project_id`。`kind: dm` 不开新话题、不重新打开话题、不算待回复、不进主管的历史。
- 主管知情：本轮环境里加“用户的私聊”一节，按 agent 和任务汇总条数和最近时间。

## 3. 会议

- 会议是 `kind: meeting` 的线程。`thread.created` 的 payload 加 `parent_thread_id`、`parent_topic_id`、`project_id`、`meeting_topic`、`brief`、`host`；会议状态用 `meeting.closed` / `meeting.reopened` 事件。
- **一个会议只有一个话题**：开会时 Hub 先生成开场消息的 id，作为 `meeting_topic` 写进 `thread.created`；之后会议里的消息不带 `topic_id` 时一律归到它。这样话题机制（0027）不会把会议拆散。
- **简报放进系统提示**（`prompts.Meeting`）：简报在开会时就定下、不再变化，放进系统提示能命中缓存，也不会在会议里显示成一条奇怪的用户消息。
- 主持人：同一个 `Supervisor` 的线程 worker。线程是会议时：系统提示用 `prompts.Meeting`；工具只开放 `project_read`、`consult`、`web_search`、`web_fetch`、`memory_search`、`thread_search`、`thread_read`、`current_time`，调用其他工具一律返回错误；整理时跳过写记忆的静默回合；`host` 设了模型时用 `models.Registry.Fixed` 得到的 provider。
- 不做 `meeting_invite` 工具。请顾问用已有的 `consult`；请任务的 agent 由用户在会议输入框里切换对象（复用第 2 节）。
- 主管工具 `meeting_propose {question}`（只在非会议线程里提供）：存一条 `kind: meeting_proposal` 的助手消息，界面上显示成带“开会”按钮的卡片，由用户点击后开会。
- 纪要：`POST /meetings/{id}/close` 在会议的 worker 里执行。输入是会议当前段的摘要（如果有）加上段内的全部消息，太长时先手动整理一次。`compactor` 按 `prompts.Minutes` 写五个栏目，程序检查栏目齐全、“结论和决定”和“行动项”的每条都带存在的 `[#seq]`，不合格重写一次，仍不合格就照原样保存，并在末尾注明有几条缺出处。
- 纪要先存成草稿（`minutes.drafted`）。用户确认（或打开了“纪要直接发出”）后 `minutes.posted`：父话题里多一条 `kind: minutes` 的助手消息，并追加一个 `trigger.fired`（kind `meeting`），立即叫醒主管。续会后再散会生成新版本，作为一条新的纪要消息发出，界面把旧版本折叠成“已被第 N 版取代”。
- 回查：`thread_read` / `thread_search` 加 `thread` 参数，只能是当前线程，或者当前线程里有纪要消息指向的会议。

## 4. 兼容和缓存

- 主管历史里不放私聊和会议内容，只放纪要消息；私聊的提示在本轮环境里，前缀照样只往后追加（0020）。
- 旧客户端不认识的 `kind`（`dm`、`minutes`、`meeting_proposal`）按普通消息显示。
