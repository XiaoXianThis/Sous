# 设计

## 原则

远端仓库是唯一的事实来源；每台机器的 checkout 只是它的一个副本。sous 只通过 git（fetch、分支、PR）在机器之间传递代码，合并只发生在 PR 上（0009）。进度只在 Hub 上（任务、Run、事件），不在机器之间同步状态。

## 1. 从远端开分支（R1）

`workspace.Prepare` 在建 worktree 之前：

1. `git fetch --prune origin`（30 秒超时；失败时退回本地 HEAD，并在 Run 里发一条 status 说明基于本地代码）。
2. 基准用 `origin/<默认分支>`（`git symbolic-ref refs/remotes/origin/HEAD`，没有时用任务的 `base_branch`）。用户在任务上指定了基准分支时用 `origin/<那个分支>`。
3. `sousbase` / `sousbaseref` 记录真实的基准提交和 `origin/...` 引用，rebase Run 和 diff 照旧以它为准。

不切换、不改动用户的工作区：worktree 是独立目录。ops 任务（`PrepareInPlace`）不变。

已实现（`workspace.PrepareTaskOn` / `startPoint`）：fetch 时设 `GIT_TERMINAL_PROMPT=0` 和 ssh `BatchMode`，凭据缺失直接失败而不是卡住 Run；分支用 `--no-track` 建，不跟踪 `origin/main`；从哪里开始写进 `Prepared.Note`，Run 作为一条 status 发出来。本地分支领先远端（有没推送的提交）时，任务不包含它们，Note 里写明提交数。仓库没有 `origin` 时静默用本地 `HEAD`（只在本机的项目，本地就是事实来源）。

## 2. 位置状态（R2）

`repos.scan` 的每个仓库多报告：

```json
{"branch": "main", "head": "<sha>", "upstream": "origin/main", "ahead": 0, "behind": 3,
 "dirty": 2, "untracked": 1, "fetched_at": 1793790000000}
```

- 数据来自 `git status --porcelain=v2 --branch`（不联网，便宜），扫描时顺带算。`fetched_at` 读 `.git/FETCH_HEAD` 的修改时间。
- daemon 每 10 分钟在后台 `git fetch` 一次各项目位置（只 fetch，不改工作区），让 `behind` 有意义；node.json 的 `auto_fetch: false` 关掉。
- Hub 存在 `project_nodes` 上（新列，迁移追加），工作台的位置标签显示“落后 3 / 改动 2”，项目弹窗的详情里列全。主管的 `Environment` 每个位置一行。

## 3. 同步和存档（R3、R4）

两个新的 `node.request`，命令写死在 Node 上，逐条过策略（和新建 / clone 项目一样）：

- `repo.sync {root}`：`git fetch`；工作区干净且能快进时 `git merge --ff-only @{u}`；否则返回原因（有改动 / 已分叉），什么都不改。
- `repo.stash_wip {root}`：在一个临时 worktree 里从当前工作区建提交（`git stash create` + `git commit-tree`，不动用户的工作区和暂存区），推到 `sous/wip/<机器>-<时间>`，返回分支名。

页面在位置的菜单里提供“同步”和“存成 WIP 分支”；主管也有对应的工具，但存档只在用户要求时用。

## 4. 并行任务的重叠（R5）

- 代码 Run 结束时 Hub 已经有 diff。记录每个任务改动的文件列表（从 diff 解析，存到任务上）。
- 派发新 Run、或 Run 产生新 diff 时，和同项目其他未关闭任务的文件列表比较；有重叠就给主管一条提醒（`trigger` 事件，进主线），列出重叠的文件和任务。主管决定加 `depends_on` 还是继续；不自动阻塞。

## 实现时的调整

- 位置状态存在 `repo_scans.git`（迁移 9），读项目时按机器和路径接到 `project_nodes` 上，不单独存一份。
- 同步和存档是用户在页面上点的固定命令（和文件编辑一样），不再逐条过权限策略；主管没有对应的工具，只在环境说明里看到每个位置落后、未推送、未提交的情况。
- 存档不用 `git stash create`（它不含新文件）：用临时索引 `GIT_INDEX_FILE` 做 `read-tree HEAD` + `add -A` + `write-tree` + `commit-tree`，用户的索引不受影响。请求名是 `repo.wip`。
- 重叠不在任务上存文件列表：验收前从两个任务最近一次有 diff 的 Run 的 diff 文件里解析，按任务对只提醒一次（`trigger` 的 key `overlap:<a>:<b>`）。

## 5. 不做的事

- 不在机器之间直接同步文件（rsync、Syncthing 之类）：会绕过 git 的历史和冲突处理。
- 不替用户合并或 rebase 他们自己的分支；sous 只管它建的 `sous/*` 分支。
- 不把 WIP 推到主分支或用户的分支上。
