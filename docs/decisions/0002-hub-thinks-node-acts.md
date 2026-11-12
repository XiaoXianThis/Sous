# 0002 Hub 只动脑，Node 才动手

- Status: Superseded by 0030
- Date: 2026-11-03

## Context

Hub 部署在公网服务器上（`https://agent.wxian.cc`），持有模型 API key 和全部事件，是最容易被攻击的一环。Node 运行在用户自己的电脑上，能执行命令、改代码。

## Decision

- Hub 可以：调用模型、联网搜索和抓取网页、调用远程（HTTP）MCP 工具、定时调度、发送通知、管理记忆和事件存储。
- Hub 不可以：执行 shell 命令、读写代码仓库、做 git 操作。所有动手的事都下发给 Node。
- Node 主动连接 Hub，电脑上不开放任何入站端口。
- Node 不盲目信任 Hub：权限模式、底线规则、允许访问的目录都在 Node 本地再校验一遍。即使 Hub 被攻破，也不能绕过 Node 的本地策略。

## Alternatives

- Hub 也能执行命令：部署更简单，但服务器一旦被攻破，就等于远程执行任意代码。
- 把 Hub 放在用户电脑上：合盖就会离线，做不到随叫随到。

## Consequences

- 所有执行类工具都要经过 Hub 到 Node 的协议转发，协议要支持流式输出和审批。
- 没有任何 Node 在线时，Hub 仍能对话、规划、排队任务，只是不能执行。
