<!-- wokspace-layout: 1 -->
# AGENTS.md

This repository is a **wokspace**: its top level is a workspace for agents and humans, not a project in any one language. Read this file in full before doing any work. Spec: https://wokspace.wxian.cc

## 0. Precedence

1. Explicit instructions from the user in the conversation
2. `apps/<name>/AGENTS.md`: applies only inside that app and cannot relax the rules in this file
3. This file, and the rules in each skeleton directory's `README.md`
4. Your own habits and your agent tool's defaults

Ask the user before changing this file.

## 1. Root: frozen

| Kind | Entries | Committed |
|---|---|---|
| Required | `AGENTS.md` `.gitignore` `.gitattributes` `apps/` `build/` `docs/` `work/` | yes (`build/` only its README) |
| Optional | `.github/`, `.gitlab-ci.yml` (CI), `LICENSE` (open source), `.gitmodules` (submodules) | yes, after the user approves |
| Local | `.git/`, plus the agent, editor, and OS files listed in `.gitignore` | no |

- Never create any other file or directory at the root.
- When the user approves an optional entry, add it to the whitelist at the top of `.gitignore` in the same change.
- In `.gitignore`, only append ignore rules below the whitelist section. Changing the whitelist section, or `.gitattributes`, needs approval.
- Never delete root files you did not create. Report them to the user.
- At the root, run only read-only commands, git commands, and `wok` commands. Run install, build, test, scaffold, and init commands inside the project they belong to.
- If a command creates files at the root by accident, move or delete them right away and say so in your reply.

## 2. Skeleton: frozen

```
apps/                      independent projects
build/                     final build output (only the README is committed)
docs/                      long-lived facts; docs/README.md is the project home page
├── architecture/          how the system is built
├── decisions/             decision records
├── guides/                how-to guides
└── api/                   contracts between apps
work/                      everything else
├── agent/                 agent material that is not tied to one agent tool
│   ├── rules/             detailed rules
│   ├── skills/            reusable procedures
│   ├── configs/           shared agent configs
│   └── memory.md          lessons learned
├── specs/                 requirements, designs, tasks
├── scripts/               reusable tool scripts
├── tests/                 tests across apps
│   ├── e2e/
│   ├── integration/
│   └── fixtures/
├── assets/                shared assets and asset sources
├── infra/                 deployment and infrastructure across apps
├── tmp/                   scratch space (not committed)
├── cache/                 intermediate output (not committed)
└── logs/                  logs (not committed)
```

- Never add, delete, or rename skeleton directories. Ask the user if you think one is needed.
- Every skeleton directory has a `README.md` with its rules. **Read it before you first write a file in that directory.**
- Changing the "Rules" section of a skeleton README needs approval. Keep its other sections (lists, indexes) up to date yourself. Never delete a skeleton README.
- Inside skeleton directories you may organize freely, with at most two levels of subdirectories below a leaf skeleton directory. Category directories in `docs/` have no subdirectories. `work/tmp/`, `work/cache/`, and `work/logs/` have no limit.
- Do not create empty directories or placeholder files such as `.gitkeep`.

## 3. Where things go

| What | Where |
|---|---|
| An app's project files, tool configs, `.env*`, `CHANGELOG.md`, `Dockerfile`, assets, unit tests | inside `apps/<name>/`, following the language's conventions |
| Tests across apps | `work/tests/` |
| Requirements, designs, tasks | `work/specs/<slug>/` |
| TODOs and ideas that belong to no spec | `work/specs/backlog.md` |
| One-off scripts, debug output, drafts, experiments | `work/tmp/<task-slug>/` |
| A script needed a second time | move it from `work/tmp/` to `work/scripts/` |
| docker compose, deployment, CI helpers across apps | `work/infra/` |
| Project introduction, `CONTRIBUTING.md`, `SECURITY.md` | `docs/` |
| Final build output | `build/<app>/` |
| `Makefile`, `justfile`, `Taskfile` at the root or in `work/` | not allowed; use app task tables (section 5) and `work/scripts/` |

Project files (`package.json`, `Cargo.toml`, `go.mod`, `pyproject.toml`, ...) may only appear in `apps/<name>/`, or in subdirectories of `work/scripts/` and `work/tests/`. The latter are unpublished helper projects: run their commands in their own directory and document them in the nearest README.

## 4. Apps

- **Before working in `apps/<name>/`, read its `AGENTS.md`.**
- Ask the user before creating an app. Use a kebab-case name.
- Create the app with `wok new <name>`. Without wok, create `apps/<name>/AGENTS.md` from the template in `apps/README.md` and add the app to the list there.
- Then initialize the app with its own toolchain inside `apps/<name>/`, fill in its task table (section 5), and confirm the commands with `wok ls <name>`.
- Inside an app, follow the language's and framework's conventions. The directory rules of this repository do not apply there.
- When you change an app's commands, stack, or dependencies, update its `AGENTS.md` in the same change.
- Apps never import each other's source or reference it by relative path. The only allowed dependencies are:
  - consuming another app's build output in `build/<app>/`, or its published package;
  - talking over a contract in `docs/api/`. Code generated from a contract lives in the app that uses it.
- Record dependencies in both apps' `AGENTS.md` and in the list in `apps/README.md`.
- Intermediate output (`node_modules/`, `target/`, `dist/`, `.venv/`) stays inside the app and is ignored.

## 5. Tasks

Each app's `AGENTS.md` has one task table. Humans, agents, and `wok` all read it.

```markdown
| task | command | windows |
|---|---|---|
| dev | npm run dev | |
| build | npm run build -- --outDir "$WOK_OUT" | |
```

- `task`: a lowercase name. The standard tasks are `install`, `dev`, `test`, and `build`. Add others freely (`lint`, `fmt`, ...).
- `command`: runs with the app directory as the working directory, through `sh` on macOS and Linux and `cmd` on Windows. Write `|` as `\|`.
- `windows`: optional. Used instead of `command` on Windows.
- Leave `command` empty to use the command that `wok` detects from the app's project files. `wok ls <name>` shows what will run.
- `$WOK_ROOT` (repository root), `$WOK_APP` (app name), and `$WOK_OUT` (`build/<app>/`) are replaced on every platform before the command runs, and are also set as environment variables. Wrap them in double quotes, since paths may contain spaces.
- A `build` task puts its final output in `$WOK_OUT`.

From anywhere in the repository:

```
wok dev <app> [app...]          start one or more apps; several run in parallel
wok build <app>... | --all
wok test <app>... | --all
wok install <app>... | --all
wok run <app> <task> [-- args]
wok ls [app]
```

Without wok, run the same command inside `apps/<app>/` and replace the `$WOK_*` values yourself.

## 6. File lifecycle

- Each task uses its own `work/tmp/<task-slug>/`. When the task ends, move anything valuable to its proper place, then delete the whole directory.
- When a spec is done (every box in `tasks.md` is checked), distill its lasting conclusions into `docs/` and keep the spec as it is.
- `docs/` only says what is true now and why. No progress reports or plans.
- If your tool writes specs into its own directory (such as `.kiro/specs/`) and that directory is not linked to `work/specs/`, copy the final spec to `work/specs/<slug>/`.

## 7. Working in parallel

- When several agents work at once, each changes only the apps or specs in its own task. Before changing shared files (this file, skeleton READMEs, `docs/api/`), say so, and keep the change small.
- Put git worktrees you create in `work/tmp/worktrees/<branch>/` and delete them after merging or abandoning. Tool-managed worktree locations (for example under `.claude/`) are fine too, since they are ignored.

## 8. Naming and platforms

This repository must work on Windows, macOS, and Linux.

- File and directory names are lowercase kebab-case ASCII with no spaces. Names fixed by a language or tool are exempt (`README.md`, `AGENTS.md`, `Cargo.toml`, Java and C# class files, ...).
- Never use Windows reserved names: `con` `prn` `aux` `nul` `com1`–`com9` `lpt1`–`lpt9`, with or without an extension.
- Names never end with a dot or a space. No two names in one directory differ only in case.
- Keep relative paths in the repository under 120 characters.
- Prefer commands that the toolchain provides on every platform (`npm run`, `cargo`, `go`). Do not write shell-only commands such as `rm -rf` or `cp` in docs.
- Write scripts in a language the project already uses. If a script must depend on a shell, provide both `.sh` and `.ps1`.
- Use relative paths with `/`. Never hard-code absolute paths.
- `.gitattributes` manages line endings. Do not convert them yourself.

## 9. Large files and secrets

- Before committing a file larger than 10 MB, ask the user. The user decides whether to use Git LFS.
- Secrets, certificates, and tokens never enter the repository, including `docs/` and `work/agent/configs/`. Use placeholders in example configs.

## 10. Before you finish

Run `wok check` at the root. Fix every problem you caused and report the rest to the user.

Without wok, the repository must be a git repository. Run `git status --porcelain --ignored` and check by hand:

1. Root entries: nothing outside section 1 appears, including lines starting with `!!` (ignored).
2. Skeleton: no skeleton directory was added, deleted, or renamed, and every skeleton README is still there.
3. New files follow sections 3 and 8, nesting follows section 2, and there are no empty directories.
4. This task's `work/tmp/<task-slug>/` is gone.

## 11. Never

- Add root entries, change the skeleton, change the `.gitignore` whitelist section, or edit this file without approval.
- Use the root as a project, or put project files anywhere section 3 does not allow.
- Reference another app's source.
- Write progress or plans in `docs/`.
- Commit the contents of `build/`, secrets, `.env` files, or files that belong to one agent tool or editor.
