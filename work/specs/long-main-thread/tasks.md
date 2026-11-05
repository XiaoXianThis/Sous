# 任务

- [x] 1. 重排请求：系统提示去掉时间、机器任务和记忆，改成“本轮环境”拼在最新用户消息后面；加单测，确认连续两轮请求除最后一条消息外完全相同
- [x] 2. `provider.Request.CacheAt`，Anthropic 实现加 `cache_control` 断点；其他实现忽略；provider 单测
- [x] 3. `thread.compacted` 事件、`thread_segments` 表（迁移 10）、投影；按段加载历史，去掉 `HistoryMessages`；段首块（画像快照和摘要）
- [x] 4. token 估算、模型的 `context_window` 配置（页面可填）、软阈值、硬阈值、溢出三种触发；降级截断；大消息只保留开头和结尾
- [x] 5. `compactor` 角色（模型页、用量统计）；整理前的静默写记忆回合
- [x] 6. 结构化摘要的提示词、栏目解析和遗漏校验（重试一次，然后由程序补回）；单测覆盖“待办被漏掉”和“已完成移出”
- [x] 7. `thread_search`、`thread_read` 两个工具和系统提示里的说明
- [x] 8. 接口 `GET /threads/{id}/context`、`POST …/compact`、`PUT …/summary`
- [x] 9. 页面：占用环、上下文面板（摘要编辑、命中率、立即整理）、消息列表里的分隔线
- [x] 10. e2e：用 mock provider 跑 300 轮的长线程（`TestLongThread`，整理 30 次），验证没有溢出、每轮输入有上限、早期承诺在摘要里还在、能用 `thread_read` 查到原文
- [x] 11. 真实模型验收（DeepSeek，开发数据的副本，窗口设成 24000）：同一段内每次调用的缓存命中 87%–99%；手动整理生成了带编号的结构化摘要并写入 6 条记忆；整理后问最早的测试消息（#13）答对
- [x] 12. 文档：决策记录 0020（上下文分段和摘要；0019 已被自动审批占用）；更新 `docs/api/core-hub-http.md` 和事件列表；`docs/architecture` 补一节主管上下文
