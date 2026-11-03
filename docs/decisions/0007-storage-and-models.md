# 0007 存储用 SQLite，模型多协议、默认 DeepSeek

- Status: Accepted
- Date: 2026-11-03

## Context

sous 只服务一个用户，Hub 部署在一台服务器上。模型需要能自由替换。

## Decision

- Hub 存储使用 SQLite（WAL 模式）：事件、视图、记忆都放在一个文件里，备份就是复制这个文件。
- 模型层支持三种接口格式：OpenAI Responses、OpenAI Chat Completions、Anthropic Messages。每个 provider 可以配置 `base_url`、`api_key`、模型名。
- 按角色分配模型（supervisor、extractor、reviewer、inspector），默认全部使用 DeepSeek。

## Alternatives

- PostgreSQL：给单个用户用太重，还要另外运维。
- 只支持 OpenAI 兼容格式：Anthropic 的原生能力（比如缓存、思考块）会丢失。

## Consequences

- 内部需要一套统一的消息格式和工具调用格式，三种协议各写一个适配器。
- Store 接口保持抽象，以后要换 PostgreSQL 时不影响上层。
