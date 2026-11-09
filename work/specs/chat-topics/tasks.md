# 任务

- [x] 1. 数据：迁移（`topics` 表、`messages.topic_id` / `task_id`、`tasks.topic_id`、`events.task_id` 索引），事件 `topic.updated`、`message.moved`、`message.seen`，隐式开话题和重新打开的投影；store 单测
- [x] 2. 任务带话题：`task_create` 取回合的话题、子任务继承、汇报给没有话题的任务开话题；汇报、叫醒、提问带话题
- [x] 3. 主管：按话题分轮（`pending`）、历史里的话题标记、【本轮环境】里的话题、话题工具、插话、副作用前复核、`message.seen`、连发等待；单测
- [x] 4. 接口：发消息带 `topic_id`、话题列表、按话题取消息、改话题、撤销并入、沟通记录；推送按话题合并；httpapi / task 单测
- [x] 5. 提示词：话题和插话的说明（不引入默认值）
- [x] 6. 前端：类型、api、reduce（`topic.updated`、`message.moved`、`message.seen`）、话题视图（需要你处理、卡片、未读、卡片内输入框、并入和撤销）、时间线开关、汇报收成一行、排队中 / 已看到、`#topic=` 路由、`TalkLog`（卡片和任务详情）；单测
- [x] 7. 端到端（部分）：Hub 内的集成测试覆盖两个话题分轮、插话、并入和不并入、副作用前复核、连发提供、话题工具和重新打开（`supervisor/topics_test.go`）、REST 往返和撤销并入（`httpapi/topics_test.go`）、任务和汇报进话题、沟通记录（`task/topics_internal_test.go`）；浏览器验收用开发数据的副本 + mock 主管（新话题、话题内回复、关闭、时间线切换、`#topic=` 链接、任务详情里的沟通记录、手机布局）
- [x] 8. 文档：决策记录、`docs/api/core-hub-http.md`、`docs/architecture`、两个 app 的 `AGENTS.md`
- [ ] 9. 真实模型验收：用真实主管模型看它会不会按时 `topic_update` / `topic_close`、插话时并入是否合理；用 Hub + daemon + 假 agent 跑一遍带 `ask_supervisor` 的任务，看沟通记录
