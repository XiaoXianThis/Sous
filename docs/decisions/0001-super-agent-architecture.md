# 0001 参考 Super-agent 的常驻架构，用 Go 自研

- Status: Accepted
- Date: 2026-11-03

## Context

作者同时推进多个项目，需要一个长期运行、随叫随到的私人助理：讨论需求、维护任务、调度多家编程 agent、执行命令、多端对话和通知。调研过 OpenClaw、Hermes Agent、Multica、WorkBuddy、OpenAI dots、DeepSeek Super-agent 等方案：商业产品只能调度自家模型；开源方案要么偏对话（OpenClaw、Hermes），要么偏任务看板（Multica），都不能完整覆盖需求，也不能完全掌控。

## Decision

自研 sous，用 Go 编写，架构参考 DeepSeek Super-agent：

- 事件日志是唯一的事实来源；会话、任务、审批都是由事件推导出的视图。
- 大脑（Hub）与执行端（Node）分离：Hub 常驻服务器，Node 在各台电脑上执行。
- Router 把任务分派给上下文互相隔离的 Worker 并发执行。
- 主动性来自 Trigger（定时、超时、外部事件），而不只是响应用户消息。
- 独立的看门狗负责心跳、重启和断点恢复。
- 只服务一个用户，不做多人和团队功能。

## Alternatives

- 以 OpenClaw 或 Hermes 为底座再改造：调度编程 agent 的能力弱，代码体量大，难以完全掌控。
- Multica 加一个秘书层：两套系统要拼接，数据模型割裂。
- 直接用 DeepSeek Super-agent 原型：只是研究原型，只接 GitHub，接口还会变。

## Consequences

- 需要自己实现渠道、存储、调度等基础设施，前期工作量较大。
- Go 编译出单个二进制，常驻内存小，能交叉编译到 Windows、macOS、Linux。
- 事件溯源让跨端连续、崩溃恢复、复盘都依赖同一套机制。
