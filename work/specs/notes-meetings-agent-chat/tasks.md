# 任务

逐步说明见 [execution.md](execution.md)，编号对应那里的步骤。顺序：记事本 → 会议 → 直接对话。

## A. 记事本

- [x] A1. 数据：迁移、事件、投影、读取；store 单测
- [x] A2. 接口：`/notes`、发消息带 `note_ids`、`/device/notes`；httpapi 单测
- [x] A3. 命令行：IPC `note.add`、`sous note`
- [x] A4. web：类型、api、状态、记事本抽屉、“发给…”、入口和快捷键；单测
- [x] A5. 浏览器验收（在 C6 里一起做）

## B. 会议

- [x] B1. 数据：迁移、事件、投影、读取、会议消息归到 `meeting_topic`；store 单测
- [x] B2. 开会和会议的接口（含简报）
- [x] B3. 主持人的回合：`prompts.Meeting`、只读工具、`Fixed` 模型、整理不写记忆；单测
- [x] B4. 写纪要：`prompts.Minutes`、`checkMinutes`、重写一次；单测
- [x] B5. 发出纪要、叫醒主管、`minutes_auto_post`
- [x] B6. `meeting_propose`
- [x] B7. `thread_read` / `thread_search` 的 `thread` 参数；单测
- [x] B8. 契约和架构文档
- [x] B9. web：会议视图、会议列表、开会对话框、纪要卡片和 `[#n]` 跳转、提议卡片、设置开关；单测

## C. 直接和子 agent 对话

- [x] C1. 协议和 Node：`purpose: chat`、`outcome: changed`、接不上会话就失败；端到端测试
- [x] C2. Hub：`chat_from`、dm 不开话题、`Engine.DM`、`afterChat`、`POST /threads/{id}/dm`；单测
- [x] C3. 主管：私聊不算待回复、不进历史、Tail 里的“用户的私聊”；单测
- [x] C4. 契约、架构文档、决策状态
- [x] C5. web：对象切换、私聊消息、Run 行的“私聊”、已送达；单测
- [x] C6. 三批一起的端到端浏览器验收，更新两个 app 的 `AGENTS.md`

## 收尾

- [ ] 真实模型验收：主持人的提问质量、纪要是否忠实、主管看到私聊提示后会不会重复 agent 已经做过的事
