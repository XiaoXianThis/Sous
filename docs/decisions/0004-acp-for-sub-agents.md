# 0004 子 agent 统一走 ACP

- Status: Accepted
- Date: 2026-11-03

## Context

第一期要接入 Claude Code、Codex、Kiro、Cursor、OpenCode 五个编程 agent，要求能布置任务、实时看到进度、审批它们发起的操作。

## Decision

- Node 实现一个 ACP（Agent Client Protocol）客户端，用这一个适配器接入全部五个 agent。
- ACP 的流式更新映射成 sous 的 Run 事件，ACP 的权限请求映射成 sous 的 Approval。
- 如果某个 agent 的 ACP 支持不稳定，Worker 接口允许退回到它自己的 JSON 流模式或伪终端（PTY）适配器。
- 每个 Run 在单独的 git worktree 里执行。

## Alternatives

- 每个 agent 单独写一个适配器：要写五套代码，审批和进度的语义也不统一。
- 全部用伪终端驱动：输出要靠解析文本，很脆弱，也拿不到结构化的审批请求。

## Consequences

- 每个 agent 在三个平台上的 ACP 启动方式和可用性，都要在开工时逐一实测，结果记录到 `work/specs/sous-mvp/`。
