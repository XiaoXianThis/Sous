# sous

你的副主厨：一个长期运行、随叫随到的私人 AI 助理。你和它讨论需求，它自己维护任务、调度 Claude Code / Codex / Kiro / Cursor / OpenCode 等编程 agent 去干活，需要时找你审批，完成后汇报，并在手机、电脑、网页上随时可用。

> sous 取自法餐后厨的 sous-chef（副主厨）：你是主厨，它替你指挥各个岗位。

> This file is the repository home page. GitHub shows `docs/README.md` automatically, so the root has no README.

## Layout

This repository is a [wokspace](https://wokspace.wxian.cc).

```
AGENTS.md        rules for agents; the entry point for every agent
apps/            independent projects, each in its own language's layout
build/           final build output (not committed)
docs/            long-lived documentation (this directory)
work/            specs, scripts, tests, assets, infrastructure, scratch space
```

## Commands

```
wok ls                 list apps and their tasks
wok dev <app>          start an app
wok build --all        build every app into build/
wok check              check the layout
```

## Documentation

- [决策记录](decisions/README.md)
- [接口契约](api/README.md)
- [架构](architecture/README.md)

当前进行中的设计见 `work/specs/sous-mvp/`。

设计上主要参考 [DeepSeek Super-agent 原型](https://github.com/deepseek-ai/dsh-superagent-prototype)：概念对照和值得借鉴的思路见 `work/agent/rules/super-agent-reference.md`。

## Agent setup

Files that belong to one agent tool are ignored by git, so each person sets them up locally. Shared standard versions live in `work/agent/configs/`. Run the commands below at the repository root.

- **Tools that read AGENTS.md** (Kiro, Codex, Cursor, GitHub Copilot, ...): nothing to do.
- **Claude Code**: create `CLAUDE.md` containing the single line `@AGENTS.md`.
- **Gemini CLI**: set its context file name to `AGENTS.md`.
- **Kiro specs (optional)**: Kiro writes specs to `.kiro/specs/`. Link it to `work/specs/` before Kiro creates its first spec, and specs become part of the repository.

  macOS / Linux:

  ```sh
  mkdir -p .kiro
  ln -s ../work/specs .kiro/specs
  ```

  Windows PowerShell (a directory junction needs no admin rights):

  ```powershell
  New-Item -ItemType Directory -Force .kiro | Out-Null
  New-Item -ItemType Junction -Path .kiro\specs -Target (Resolve-Path work\specs)
  ```

  Without the link, agents copy final specs to `work/specs/` as `AGENTS.md` requires.
