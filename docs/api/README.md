# api

Contracts between apps. This is the only source-level agreement apps share.

## Rules

- A contract may be prose or a machine-readable schema (`openapi.yaml`, `*.proto`, JSON Schema, ...). When a schema exists, the schema wins.
- Code generated from a schema lives in the app that uses it, not here.
- When a contract changes, update the provider and every consumer in the same task. Record breaking changes in `docs/decisions/`.
- Kebab-case file names, starting with the provider app's name, such as `server-http.yaml`. No subdirectories.

## Contracts

| contract | provider | consumers |
|---|---|---|
| [core-relay](core-relay.md) | core（sous-relay） | core（sous）、web |
| [sous-envelope](sous-envelope.md) | core（internal/trust） | core、web |
| [core-brain-rpc](core-brain-rpc.md) | core（大脑） | web、core（sous daemon） |
| [core-hub-http](core-hub-http.md) | core（大脑，经 core-brain-rpc） | web、desktop |
| [core-hub-node](core-hub-node.md) | core（大脑，经 core-brain-rpc 的 link） | core（sous daemon） |
| [core-daemon-ipc](core-daemon-ipc.md) | core（sous daemon） | core（sous CLI）、desktop |
| [core-sandbox-cli](core-sandbox-cli.md) | core（sous-sandbox） | desktop |
| [core-desktop-sandbox-dispatch](core-desktop-sandbox-dispatch.md) | core（独立本机 Hub） | web、desktop |
| [core-cloud-work](core-cloud-work.md) | core（独立云 Hub） | web、core（专用云 worker） |
| [core-cloud-capacity](core-cloud-capacity.md) | core（独立云 Hub） | core（容量控制器、托管 worker）、web |
| [core-external-mcp.md](core-external-mcp.md) | core（托管 Hub） | 外部 MCP 客户端、Web 授权页和撤销界面 |
