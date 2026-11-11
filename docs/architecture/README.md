# architecture

How the system is built: what each app is responsible for, how apps relate, data flow, deployment.

## Rules

- Describe the current state only. History and trade-offs go to `docs/decisions/`.
- One topic per document, kebab-case file names, such as `overview.md` or `data-flow.md`.
- No subdirectories.
- Prefer Mermaid diagrams inside Markdown, so agents can read, edit, and diff them.

## Documents

- [workbench.md](workbench.md)：项目、预设、任务和机器，以及工作台界面
- [supervisor-context.md](supervisor-context.md)：主管每轮发给模型的内容，长对话的分段、摘要和查回原文
- [dispatch.md](dispatch.md)：工作台的“现场”，你、主管、agent 和机器之间怎么流转
- [system-map.md](system-map.md)：系统模块、连线与实时状态（设置里的“系统状态”）
