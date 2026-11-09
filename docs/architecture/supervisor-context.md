# 主管的上下文

主管每一轮调用模型时发什么，以及一条很长的对话（尤其是主线）怎么一直聊下去。代码在 `apps/core/internal/supervisor`（`context.go`、`compact.go`、`summary.go`），决策见 [0020](../decisions/0020-long-thread-context.md)。

## 一轮请求

```mermaid
flowchart LR
  S["系统提示 + 工具定义<br/>（不随轮次变化）"] --> H["段首块<br/>画像记忆快照 + 摘要<br/>（换段时才变）"]
  H --> M["这一段的原文消息<br/>（只往后追加）"]
  M --> T["最新的用户消息 + 本轮环境<br/>（每轮都变）"]
```

- 系统提示只按线程种类和项目变化（`prompts.Supervisor`）。
- 本轮环境（`prompts.Tail`）接在最新那条用户消息后面，作为单独的一段文本：时间、机器和任务、召回的相关记忆、这一段里新增或删除的画像记忆。它不存进消息表，下一轮换成新的，所以前面的内容逐字节不变，能命中提示缓存。
- 已回答的消息超过 4000 字时，只发开头和结尾各 1500 字，中间提示用 `thread_read` 看全文。
- `provider.Request.CacheAt` 标出 Anthropic 的缓存断点（段首块、上一轮的末尾、请求末尾），系统提示总是缓存；其他供应商按前缀自动缓存，忽略这个字段。

## 谁叫醒主管

主管的一轮由对话里的输入开始：用户消息，或者 `trigger.fired`（存成 `kind: trigger` 的用户消息）。触发来自定时任务、跟进扫描（`trigger`），以及任务引擎（决策 [0025](../decisions/0025-supervisor-loop.md)）：

```mermaid
flowchart LR
  A["子 agent<br/>（sous mcp：进度 / 提问 / 结果）"] -- run.ask --> H["Hub"]
  E["任务引擎<br/>汇报、受阻、等机器、换了机器"] -- "trigger kind task" --> W["对话的 worker"]
  H -- "trigger kind task" --> W
  W -- "等 20 秒合并" --> S["主管的一轮"]
  S -- "run_message / task_* / notify / stay_silent" --> E
```

- kind `task` 的触发先等 `TaskBatch`（20 秒）再开一轮，期间的进展合在一起；空闲的对话收到用户消息后等 `Gather`（Hub 设 1.5 秒）再开始，几条连发的消息一起看；正在跑回合的对话立刻收到信号。
- 用户上次说话之后，只靠触发开的回合最多 `MaxAutoTurns`（8）轮，之后静默回复，并在主线提醒用户一次。
- 汇报本身是给用户看的助手消息，主管在历史里看得到；触发只是一句指向它的话，加上“接着推进、notify 还是 stay_silent”的说明。

## 话题

一条对话的消息按话题整理（决策 [0027](../decisions/0027-chat-topics.md)，代码在 `supervisor/topics.go`、`store/topics.go`）。话题只是消息和任务上的标签，上下文仍然是整条对话的。

```mermaid
flowchart LR
  P["pendingTopics<br/>各话题未回复的消息"] --> K["选最早等着的话题 T"]
  K --> R["请求：全部历史<br/>去掉别的话题还在等的消息<br/>用户消息带【话题#N】"]
  R --> L["回合的每一步"]
  L -- "调用模型前 / 执行工具前读新消息" --> A["absorb：T 里的补充直接插入<br/>自成新话题的提供给 topic_attach"]
  A --> L
  L --> Y["回复带 topic_id = T"]
```

- 一条用户消息被“同一话题里 `reply_to` 不小于它的 seq 的回复”覆盖才算回复过；没有话题的回复按它 `reply_to` 指向的消息算话题（旧数据）。一轮结束后还有别的话题在等，worker 接着跑下一轮（`turn` 返回 true）；什么都没变时不重复，避免空转。
- 【话题#N】的 N 是话题第一条消息的 seq，标题会改，所以只写在本轮环境里（话题标题、当前结论、它的任务），历史里只有编号，前缀照样只往后追加。
- 插入的新消息追加在最后一个工具结果末尾（`addNote`），不入库；模型返回了有副作用的调用（不在 `readOnlyTools` 里的，含 MCP 工具）而这时有新消息要插入时，这批调用都回“未执行”，等模型看完再决定。
- `topic_attach`、`topic_update`、`topic_close` 由回合自己处理（和 `stay_silent` 一样），排在工具列表末尾、顺序固定。`topic_attach` 只接受本轮提供过的消息。
- 回合开始和插入时发临时事件 `message.seen`，界面据此显示“已看到”和正在处理的话题。

## 分段和整理

```mermaid
sequenceDiagram
  participant W as 线程的 worker
  participant S as 主管模型
  participant C as 整理模型（compactor）
  participant L as 事件日志
  W->>S: 回合（估算超过 85% 时先整理）
  S-->>W: 回复
  W->>W: 估算或上一轮实际输入超过 60%？
  W->>S: 静默回合：只开放记忆工具，把要折叠的内容写进记忆
  W->>C: 旧摘要 + 要折叠的消息
  C-->>W: 新摘要（程序校验决定和待办没有丢）
  W->>L: thread.compacted（新的一段）
```

- 段存在 `thread_segments`，由 `thread.compacted` 投影而来。每轮读最新的一段和 `covers_up_to` 之后的全部消息。
- 折叠范围：至少保留最近 10 条用户消息，而且保留的原文不少于窗口的 25%、不多于 40%。切在用户消息前面；回复跟着它回答的那条消息一起折叠；还没回答的消息不折叠。手动整理不要求 25%；模型报超长后只保留 4 条用户消息。
- 要折叠的内容超过窗口的 35% 时分块，逐块写记忆、更新摘要。
- 整理、手动整理、改摘要都在线程自己的串行 worker 里执行（`Supervisor.enqueue`），不会和回合同时进行。
- 失败时这一轮从最老的消息开始略去，下一轮再试；连续失败 3 次在对话里提示用户一次。
- 线程第一次进入这套机制时自动建第 1 段（原因 `start`）：太长的旧对话从最近能放下的地方开始，更早的部分没有摘要，但能搜到。

## 摘要

固定栏目（`prompts.SummarySections`）：已做的决定、待办和承诺、用户的偏好和纠正、进行中的话题、已完成或作废、其他背景。每条末尾带来源消息的编号 `[#seq]`。“已做的决定”和“待办和承诺”的条目只能移到“已完成或作废”，`missingItems` 按编号或开头文字核对，遗漏时让模型重写一次，仍然遗漏就由 `restore` 补回原栏目。

## 查回原文

- `thread_search {query, limit?, scope?}`：在当前对话里按子串匹配搜（中文按两字词），返回编号、时间和片段；主线里 `scope: "all"` 搜全部对话。
- `thread_read {seq, before?, after?}`：读一条消息的全文和前后几条。

## 窗口和角色

- 窗口是主管模型角色上的 `context_window`（设置 → 模型，默认 128000）。
- 估算偏高：中日韩文字每字按 1 token，其他文字每 4 字符按 1 token；回合结束后的判断还会参考上一次调用的实际输入。
- 摘要由 `compactor` 角色生成，没单独选模型时跟主管一样；写记忆的静默回合用主管的模型。两者的用量都记在 `compactor` 名下。
