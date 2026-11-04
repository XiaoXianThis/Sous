# specs

Requirements, designs, and tasks: documents about work in progress.

## Rules

- One directory per spec: `work/specs/<slug>/`, with a kebab-case slug.
- Inside it, add `requirements.md`, `design.md`, and `tasks.md` as needed. This matches Kiro's spec layout.
- `tasks.md` lists tasks as checkboxes. The spec is done when every box is checked.
- When done, distill lasting conclusions into `docs/` and keep the spec as it is.
- TODOs and ideas that belong to no spec go to `backlog.md`.
- No subdirectories inside a spec directory.

## Specs

| spec | apps | status |
|---|---|---|
| [sous-mvp](sous-mvp/) | core, web | 设计评审中 |
| [agent-admin](agent-admin/) | core, web | 已实现，部分 agent 待实测 |
| [m5-workbench](m5-workbench/) | core, web | 已实现并验收（含真实模型），Linux 真机待验收 |
| [workbench-single-page](workbench-single-page/) | web | 已实现并验收（浏览器） |
| [multi-machine-sync](multi-machine-sync/) | core, web | 已实现并验收（浏览器） |
