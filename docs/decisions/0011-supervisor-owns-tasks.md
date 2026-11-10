# 0011 第一期由主管自己维护任务，不单独设置 extractor

- Status: Accepted
- Date: 2026-11-03

## Context

最初的设计里，每条用户消息都先交给 extractor（一个便宜的模型）提炼任务，再交给主管确认。这样每条消息都多一次模型调用和一层延迟；而在第一期的对话场景里，主管本身就能判断要不要创建任务。

## Decision

- 第一期由主管在对话中直接调用 `task_create` / `task_update` 维护任务。
- 模型角色改为 supervisor、reviewer、approver、inspector 四个（0020 又增加 `compactor`，用来整理对话的摘要）。0007 里提到的 extractor 角色，第一期不启用。
- 等到会议模式或长段转写需要批量提炼任务时，再引入 extractor。

## Alternatives

- 保留 extractor：主管的上下文更干净，但成本和延迟都更高，第一期收益不明显。

## Consequences

- 主管的系统提示词里，需要写清楚什么时候该创建任务、什么时候只是聊天。
