# 0009 代码任务以 PR 交付，sous 永不合并主分支

- Status: Superseded by 0022
- Date: 2026-11-03

## Context

代码任务在 worktree 里完成并通过验收后，必须定下改动怎么交付；多个 Run 同时改同一个仓库时，还可能产生冲突。

## Decision

- 验收通过后，默认 push 分支并创建 PR。这一步属于 git-remote 类操作，按权限模式审批。
- sous 永远不直接合并到主分支，由用户自己评审并合并。
- PR 出现冲突时，主管派一个新的 Run 去 rebase。
- 同一个任务的多次 Run 复用同一个 worktree 和分支。

## Alternatives

- 验收通过后自动合并：出错的代价太大，而且用户失去了最后一道把关。
- 只把分支留在本地：用户要自己去找分支、push、开 PR，额外占用注意力。

## Consequences

- 每台 Node 上的 git 和 `gh`（或者对应的托管平台 CLI）都要先登录好。
- PR 等待用户合并，在 PWA 首页显示为“待你处理”。
