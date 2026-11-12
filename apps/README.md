# apps

Each subdirectory is an independent app that follows its own language's conventions.

## Rules

- Ask the user before creating an app. Use a kebab-case name.
- Create it with `wok new <name>`. Without wok, create `apps/<name>/AGENTS.md` from the template below and add the app to the list.
- Before working in an app, read its `AGENTS.md`.
- Apps never reference each other's source. Allowed dependencies are listed in the root `AGENTS.md`, section 4.

## Apps

| app | purpose | stack | depends on |
|---|---|---|---|
| core | 中继 sous-relay + 每台电脑的 daemon / CLI（大脑机器上运行大脑） | Go, SQLite | none |
| web | PWA：聊天、任务、审批、设置；部署到 Vercel | React | core（契约 core-relay、sous-envelope、core-brain-rpc、core-hub-http） |
| desktop | 桌面版：托盘 + 打开 sous 页面，打包并管理本机的 sous daemon | Go, Wails v3 | core（build/core 的 `sous`；契约 core-daemon-ipc） |
| site | 官网 + 文档站 | Astro, Tailwind CSS | none |

## App AGENTS.md template

`wok new` copies the block below, replacing `<name>`. Edit it to change what new apps start with.

```markdown
# <name>

- Purpose:
- Stack:
- Depends on: none

## Tasks

Commands run in `apps/<name>/`. Leave a command empty to use what `wok` detects; `wok ls <name>` shows the result.

| task | command | windows |
|---|---|---|
| install | | |
| dev | | |
| test | | |
| build | | |

## Conventions
```
