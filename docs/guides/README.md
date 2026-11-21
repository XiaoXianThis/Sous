# guides

How-to guides for people: setting up a development environment, using, deploying, troubleshooting.

## Rules

- Only steps that work today. Update them when they change.
- Commands for a single app belong in its `AGENTS.md`. Guides cover flows across apps or for end users.
- Give both Windows and macOS/Linux commands when they differ.
- Kebab-case file names, no subdirectories.

## Guides

| guide | for |
|---|---|
| [deploy.md](deploy.md) | 部署 0.3：服务器上的中继、Vercel 上的页面、首次设置，从 0.2 迁移，发布签名 |
| [connect-machine.md](connect-machine.md) | 在电脑上安装 sous 客户端、配对、注册成系统服务 |
| [macos-signing-and-permissions.md](macos-signing-and-permissions.md) | macOS：用固定签名身份让文件访问授权跨重建有效，给 sous 开完全磁盘访问权限 |
| [supervisor-benchmark.md](supervisor-benchmark.md) | 主管能力、编排差与版本升级的自动评测和读分 |
| [local-workspace.md](local-workspace.md) | 在桌面版或命令行使用独立本机工作区 |
| [local-sandbox.md](local-sandbox.md) | Apple Silicon 上的 Microsandbox 本机执行、工作台待授权派发、审阅写回和故障恢复 |
| [hosted-workspace.md](hosted-workspace.md) | 独立单所有者云工作区部署预览、HTTPS、Passkey 初始化及持久存储 |
| [ec2-worker.md](ec2-worker.md) | EC2 按需 worker 的独立驱动、镜像、权限、对账与真实云验收 |
| [external-mcp.md](external-mcp.md) | 已注册外部 MCP 客户端的托管项目授权、草稿和撤销 |
