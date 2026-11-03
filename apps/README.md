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
| core | 服务端 Hub + 客户端 daemon / CLI | Go, SQLite | none |
| web | PWA：聊天、任务、审批、设置 | Svelte | core（契约 core-hub-http） |
| desktop | 托盘 + 窗口，内嵌 web | Go, Wails v3 | web（build/web）；core（契约 core-daemon-ipc、core-hub-http） |

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
