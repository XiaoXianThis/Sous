# 任务交付流程评估：固定的“计划 → 编码 → 验收 → push → PR”是否合理

- 日期：2026-11-06
- 基线：`origin/main` = `c52c782`（读代码前已 fetch，本地与远端一致）
- 范围：只做分析和方案，不改代码。文中 `文件:行号` 都相对 `apps/core/internal/`，前端相对 `apps/web/src/`。
- 结构：§1 结论、§2 现状（流程写死在哪里）、§3 耦合点、§4 方案、§5 与现有机制的交互、§6 配置与迁移、§7 推荐与优先级、§8 待用户确认。

---

## 1. 结论

1. **用户的判断成立。** 现在“代码任务 = 必须是 git 仓库 + 必须建 worktree + 验收通过且有 diff 就必须 commit、push、开 PR”，这条链路在 Hub 和 Node 两侧都写死了，**没有任何配置项能关掉其中一步**。唯一不走这条链路的是运维任务（`kind: ops`），但它只能派到服务器角色的机器，而且不验收，不能拿来做“写进本机项目目录”。
2. **问题的根源是两件事绑在了一起**：一是“在哪里干活”（隔离的 worktree，还是项目目录本身），二是“干完之后怎么交付”（不提交、本地提交、push、开 PR）。现在两者都由 `kind == code` 一个字段推出来，所以“非 git 项目”和“不想马上提交”这两类需求都无从表达。
3. **固定流程中有几部分应该保留**：计划和验收两步本身没有问题；git 项目默认用隔离 worktree、sous 永不合并主分支（决策 0009）、push 走权限审批、effects 队列的幂等推进，这些都应该保留。需要改的只是验收之后的交付，以及“非 git 项目在哪里干活”。
4. **读代码时发现的两个现有问题**（与本议题直接相关，未实测）：
   - 有路径的项目里的 **general 任务也会建 worktree，但永远不交付、也不清理**：router 对有登记路径的项目一律给 `Path`（`router/router.go:79-81,106`），`startRun` 把它作为 `RepoPath` 下发（`task/engine.go:635`），Node 见到 `RepoPath` 就建 worktree（`node/run.go:186-195`）；验收通过后只有 `kind == code` 才交付（`task/advance.go:220`），`cleanup` 和 `cleanupNode` 也只处理 code 任务（`task/advance.go:367`、`:415`）。结果是 agent 的改动留在一个用户看不到的 `sous/...` 分支上，worktree 越积越多。
   - **非 git 目录登记成项目路径后，任务一派发就失败**：`workspace.PrepareTaskOn` 先跑 `git rev-parse --show-toplevel`，失败就报错（`workspace/workspace.go:72-75`）。code 和 general 任务都会走到这里。demo-th 这类项目在 sous 里实际上无法执行。

## 2. 现状：流程写死在哪里

### 2.1 一个代码任务的完整路径

| 阶段 | 在哪里决定 | 写死的行为 |
|---|---|---|
| 建任务 | `task/engine.go:252-257` `Create` | 只要给了非 `misc` 的项目，`kind` 默认就是 `code`。只有 `code / general / ops` 三种，没有交付相关字段（`TaskCreatedPayload`，`engine.go:305-308`）。 |
| 计划 | `inbox → planned`（`engine.go:301-304`） | 由主管和用户整理，没有写死额外步骤，**这一步没有问题**。 |
| 路由 | `router/router.go:88-90,110-112` | code 任务必须有“登记了机器和路径的项目”。不检查路径是不是 git 仓库。 |
| 准备目录 | `node/run.go:175-197` `prepare` | 有 `RepoPath` 就 `PrepareTaskOn`：必须是 git 仓库（`workspace/workspace.go:72`），建 worktree 和 `sous/<slug>-<id>` 分支；有 origin 就 fetch 并从 `origin/<base>` 开分支（`workspace.go:139-166`）。只有 `KindOps` 用 `PrepareInPlace`（`run.go:180-185`）。 |
| 给 agent 的提示 | `task/engine.go:684-690` `workPrompt` | 写死“不要 push，也不要创建 PR……sous 会在验收通过后自己 push 并开 PR”。恢复说明也假定有 git（`advance.go:133-135`）。 |
| 收集改动 | `node/run.go:164` → `workspace.Diff` | 改动必须相对 git 的 base commit（`workspace.go:276-291`），`Base == ""` 时没有 diff。 |
| 验收 | `task/advance.go:193-239` `review`、`:242-270` `verdict` | reviewer 只对 code 任务读 diff。没配验收模型时自动通过（`:243-245`）。 |
| **验收后的分叉** | `task/advance.go:219-228` | `Pass && Kind == code && DiffStat != ""` → **无条件** `queueNext(deliver)`；否则直接 `done`。没有第三条路。 |
| 交付路由 | `task/engine.go:613-621` | deliver Run 的 agent 固定为 `sous`，钉在有分支的那台机器上。 |
| 交付执行 | `node/deliver.go:19-115` `deliver` | 依次：`workspace.Commit` → 检查有提交 → 按 `git-remote` 类别审批 push → `workspace.Push` → `workspace.PullRequest`。**三步串在一个函数里，不能只做其中一部分。** |
| push | `workspace/deliver.go:54-64` | 必须有名为 `origin` 的远端，否则报错。 |
| PR | `workspace/deliver.go:69-114` | 必须有 `gh` 命令；只支持 GitHub。 |
| 交付结果 | `task/advance.go:154-189` `afterDeliver` | 成功 → `done` + `pr_url`；有冲突 → rebase（最多 `MaxRebases=2`），仍冲突 → `blocked`；取消或失败 → `blocked`（改动留在分支上）。 |
| 收尾 | `task/advance.go:366-381` `cleanup` | 删 worktree、保留分支，只处理 code 任务。 |
| 重新派发 | `task/engine.go:492-496,511-516` `Dispatch` | 有 `pr_url` 的 done 任务不能再派发；最后一个 Run 是 deliver 时，重新派发只重做交付。 |
| 合并后部署 | `task/workbench.go:523-` `CheckMerges` | 只看 `done && pr_url != "" && kind == code` 的任务（`:554`），用 `gh` 查 PR 状态。 |
| 前端阶段 | `web/src/lib/status.ts:151-181` `stages` | code 任务的阶段固定为“计划 / 执行 / 验收 / PR（/ 部署）”，靠 `next === 'deliver'` 和 `status_reason` 以“正在交付”开头来判断是否在 PR 阶段。 |

### 2.2 并发名额和审批

- Hub 侧 `nodehub/nodehub.go:475-481` `Busy` 把这台机器上所有 queued/running 的 Run 都算作占用，**包括 deliver**；Node 侧 `node/run.go:125-130` 拿到 `d.sem` 才分流到 `deliver`，所以交付也占一个工位。（`work/specs/e2e-review-2026-11/assessment.md` 的 4.3 已经指出过，目前**还没修**。）
- push 审批：`node/deliver.go:51-93` 用 `policy.Classify` 得到 `git-remote`，再经 `run.judge` 按权限模式决定（决策 0019）。等审批期间 deliver Run 的状态一直是 running，**工位一直被占着**。
- worktree 内的编辑属于 `EditWorktree` 类别，在所有模式下都直接放行（`policy/policy.go:62-63`）。这条规则成立的前提是 worktree 是隔离的、可以丢弃。**这个前提对后文的“就地写入”方案很关键。**

## 3. 耦合点

| # | 假设 | 落在哪一层 | 位置 |
|---|---|---|---|
| C1 | 项目路径一定是 git 仓库 | Node / workspace | `workspace.go:72-75`；扫描也只找 git 仓库（`workspace/scan.go:20-24`） |
| C2 | 有路径就要隔离 worktree（与 kind 无关） | Hub 路由 + Node | `router.go:106`、`engine.go:635`、`run.go:186` |
| C3 | 改动只能用 git diff 描述 | Node / workspace | `workspace.Diff`（`workspace.go:276`） |
| C4 | 验收通过且有 diff，就一定交付 | Hub 任务引擎 | `advance.go:220-221` |
| C5 | 交付 = commit + push + PR，不可拆分 | Node | `node/deliver.go:35-106` |
| C6 | 远端一定叫 origin | workspace | `workspace/deliver.go:55`、`workspace.go:139` |
| C7 | 托管平台一定是 GitHub，并且装了 `gh` | workspace + Hub | `workspace/deliver.go:70`、`CheckMerges` 用 `gh.pr_state` |
| C8 | agent 被告知“sous 会 push 并开 PR” | Hub 提示词 | `engine.go:687-689`、`rebasePrompt`（`engine.go:702-710`） |
| C9 | “完成”的 code 任务要么有 PR，要么没有改动 | Hub + 前端 | `doneReport`（`advance.go:324-338`）、`Dispatch` 拒绝规则（`engine.go:494`）、`status.ts:174-177` |
| C10 | 清理只针对 code 任务 | Hub | `advance.go:367`、`:415` |

**对外部依赖的假设集中在 Node 的 `workspace/deliver.go` 和 `node/deliver.go`**：GitHub、`gh` 登录、origin 远端和凭据都在这里。Hub 只通过 `pr_url` 字段和 `CheckMerges` 间接依赖 GitHub。这是好事：交付策略的分支逻辑可以放在 Hub 的引擎里，Node 只需要把 commit、push、PR 拆成能单独调用的步骤。

## 4. 方案

先把概念拆成两个**正交**的维度，后面的方案都基于它们：

- **工作区（workspace）**：`worktree`（现状：隔离的 git worktree + 任务分支）或 `in_place`（直接在项目目录里改，不建分支）。
- **交付（delivery）**：`none`（不提交，改动留在工作区）、`commit`（只在任务分支上本地提交）、`push`（提交并 push 分支，不开 PR）、`pr`（现状：提交、push、开 PR）。

合法的组合：

| | none | commit | push | pr |
|---|---|---|---|---|
| worktree（git 项目） | ⚠ 改动只在 worktree 里，清理前必须保住（见 4.A） | ✅ | ✅ | ✅（现状） |
| in_place（git 项目） | ✅ 改动直接出现在用户的 checkout 里 | ❓ 要在用户当前分支上提交，见 §8 | ✗ | ✗ |
| in_place（非 git 项目） | ✅（demo-th 就是这一格） | ✗ | ✗ | ✗ |

### 方案 A：交付档位可配置（只针对 git 项目）

**内容**

- 增加交付档位 `delivery ∈ {pr, push, commit, none}`，按“任务 > 岗位 > 项目 > 全局 > 内置默认 `pr`”的顺序解析，和 `mode`、`agent` 的解析顺序一致（`engine.go:587,629`）。
- `review()` 的分叉（`advance.go:219-228`）改成：
  - `pr` / `push`：和现在一样 `queueNext(deliver)`，在 `CreateRun` 里带上档位；
  - `commit`：同样排一个 deliver Run，但只执行 `workspace.Commit`，不 push。完成后任务进入 `done`，汇报里写明“改动在本地分支 `sous/...`（N 个提交）”；
  - `none`：worktree 方式下不建议开放（`cleanup` 会删掉 worktree，未提交的改动就丢了），所以在 worktree 模式下把 `none` 当作 `commit` 处理，或者直接不允许选。
- `node/deliver.go` 按档位拆成三步，每一步都可以停下：`Commit` → (`push` 及以上) 审批 + `Push` → (`pr`) `PullRequest`。push 审批的逻辑不变。
- 提示词（`engine.go:687-689`）按档位生成，例如 `commit`：“提交即可，sous 不会 push”。
- 前端：`stages` 的最后一段按档位改名：“PR / 推送 / 提交”；任务里显示交付档位。

**改动面**：`event/task.go`（payload 加 `Delivery`）、`store/tasks.go` + 迁移（`tasks.delivery`、`projects.delivery`、`personas.delivery`）、`task/engine.go`（`Input`、解析、提示词）、`task/advance.go`（`review`、`afterDeliver`、`doneReport`）、`task/tools.go`（`task_create` / `task_update` 的参数）、`task/workbench.go`（项目字段）、`proto`（`RunStart.Delivery`）、`node/deliver.go`、`web` 的建任务表单、项目设置和 `status.ts`。契约 `docs/api/core-hub-http.md`、`core-hub-node.md` 要同步。

**优点**：改动集中，复用现有的 deliver Run、审批、幂等推进和恢复逻辑；默认值保持 `pr`，完全向后兼容。
**缺点**：解决不了非 git 项目。
**风险**：低。注意 `Dispatch` 现在“有 `pr_url` 才拒绝重新派发”（`engine.go:494`），`commit` 档位完成后用户再派发会在同一分支上继续做，这个行为是期望的，但要写进文档。
**向后兼容**：是。新字段为空表示“继承”，所有现存项目最终落到内置默认 `pr`。

### 方案 B：就地写入（in_place），覆盖非 git 项目

**内容**

- 项目加 `workspace` 字段：`worktree` / `in_place` / 空（自动：路径是 git 仓库就用 worktree，否则 in_place）。
- Node 的 `prepare`（`run.go:175-197`）对 `in_place` 的 Run 走 `PrepareInPlace`（ops 已经在用），不建分支。
- **怎么记录产出、怎么回滚：影子快照。** Node 在 `<sous home>/snapshots/<project>/` 维护一个**放在项目目录外**的 git 仓库，用 `git --git-dir=<影子目录> --work-tree=<项目目录>`：
  - 每个 work Run 开始前 `add -A && commit`，记作 `Base`；
  - 结束时和 `Base` 做 diff，得到和现在格式完全一样的 diff 和 stat。`workspace.Diff`、`sendDiff`、reviewer 读 diff（`advance.go:250-261`）都不用改；
  - 回滚 = 把项目目录恢复成 `Base` 时的样子（`checkout Base -- .` 加上删除 Run 期间新增的文件）。
  - 项目目录里**不会出现 `.git`**，也不需要 origin，满足 demo-th“不提交 git、不配 origin”的要求。
  - 需要排除规则（`node_modules/`、`.venv/`、`dist/`、大文件），并设一个快照大小上限；超过上限就不建快照，见下面关于权限的说明。
- 交付：`in_place` 只允许 `none`。验收通过后直接 `done`，汇报里列出改动的文件和 stat，并提供“回滚这次改动”的操作。
- 回滚由用户触发（页面按钮或主管工具），作为一个 `purpose: rollback` 的 sous Run 执行，按 `Irreversible` 类别交给用户审批。执行前检查目录当前状态是否等于这个任务最后一次 Run 的结束快照：如果之后有人（用户或别的任务）又改过，就拒绝自动回滚，只给出 diff。
- **权限**：worktree 里的编辑之所以能自动放行（`policy.go:62-63`），是因为可以丢弃。`in_place` 下只有在**快照建好了**的前提下才保留这条放行；快照失败（目录太大、磁盘不够）时，要么拒绝启动，要么把 `EditWorktree` 降为询问用户。
- **同一目录的互斥**：两个 in_place 任务同时改一个目录，diff 会混在一起，回滚也会互相破坏。Hub 在 `tryStart` 里检查：同一台机器、同一路径上已经有处于 running / waiting_approval / review 的 in_place 任务时，新任务保持 queued，并写明“等待目录：任务 X 正在使用”。这是一个**目录锁**，不占机器工位，任务一离开活动状态（done、blocked、failed、cancelled）锁就释放。

**改动面**：`event` / `store`（项目 `workspace` 字段）、`router`（允许 code 和 general 走 in_place）、`task/engine.go`（`tryStart` 加目录锁、提示词、恢复说明不再假定有 git）、`proto`（`RunStart.Workspace`）、`node/run.go`（`prepare`）、新增 `workspace/snapshot.go`、`node`（rollback Run）、`policy`（in_place 的放行前提）、`cleanup`（in_place 不删目录，只按保留期清理影子快照）、`web`（项目设置、回滚按钮）。还要新增一条决策记录（补充 0009）。

**优点**：非 git 项目第一次能完整走“执行 → 验收 → 落地”；diff、验收、汇报都沿用现有格式；回滚有依据。
**缺点**：改动最大；影子快照对大目录和二进制文件不友好；就地写入天然有“半成品留在用户目录里”的问题（见 §5.3）。
**风险**：中。主要风险在权限放行和回滚的破坏性，都靠“快照成功才放行”和“状态一致才回滚”两条规则兜底。
**向后兼容**：是。`workspace` 为空时，git 仓库照旧走 worktree；非 git 路径现在直接失败，改了之后才能用，不影响已有行为。**旧版本 daemon 不认识 in_place**，会照旧建 worktree 然后失败，所以 Node 要在 inventory 里上报能力，router 不把 in_place 的 Run 派给不支持的 Node。

### 方案 C：先暂存、后按需交付（“待推送”状态）

**内容**

- 不新增档位：**`commit` 档位本身就是“暂存”**。验收通过后改动已经在任务分支上提交，任务进入 `done`，同时记一个交付子状态 `delivery_state = pending`，表示可以随时推送或开 PR。
- 子状态取值：`none`（这个档位不需要交付）/ `pending`（已提交、未推送）/ `delivering` / `delivered`（有 `pr_url` 或已 push）/ `failed` / `discarded`（用户确认不要了）。投影里新增一列，由 `task.status`（或新事件 `task.delivery`）维护。
- **谁来触发**：
  - 用户：任务详情页的“推送 / 开 PR”按钮，和项目页的“推送全部待推送”；
  - 主管：新工具 `task_deliver(task_id, level)`，要做作用域检查（`checkScope`），和其他任务工具一样；
  - 都转到同一个 `Engine.Deliver(ctx, id, level)`：把 `next` 设为 deliver 并入队，复用现有的 deliver Run、审批和 rebase。
- **为什么不用新的任务状态**：
  - `done` 现在的含义已经是“验收通过”而不是“已合并”（PR 由用户合并），下游依赖（`engine.go:550`）也只看 done。在 `done` 下加子状态，语义不变，前端的状态表、`final()`、`active()`、归档规则都不用动。
  - 如果新增一个非终态（比如 `ready`），`active()`、`final()`、`ArchiveProject`、依赖判断、前端统计都要改，而且容易出现“依赖它的任务该不该开始”的歧义。
- **避免遗漏**：
  - 工作台的“待你处理”列出 `pending` 的任务，和现在“等你评审合并的 PR”放在一起；
  - 跟进（`trigger`）在任务 pending 超过 N 天（默认 3 天）时提醒一次；
  - 归档项目时，如果有 pending 的任务，要先让用户确认（`ArchiveProject` 现在只拒绝有活动任务的项目）；
  - 主管的环境（`Environment`）里列出 pending 的数量，用户问“还有什么没提交”时能答上来。
- **避免歧义**：
  - “重新派发”（再做一轮工作）和“交付”（只推送）是两个不同的入口；`Dispatch` 里“最后一个 Run 是 deliver 就只重做交付”的隐式规则（`engine.go:514-516`）改成显式的：只有 `delivery_state = failed` 时才走这条路；
  - 交付时分支已被删除：`PrepareTaskOn` 能从已有分支重建 worktree（`workspace.go:95-101`），分支不在了就交付失败，`delivery_state = failed` 并写明原因，不会卡在 delivering。

**改动面**：在方案 A 的基础上，加 `store`（`tasks.delivery_state`）、`task`（`Deliver`、`task_deliver` 工具、`Environment`、归档检查）、`trigger`（提醒）、`task/routes.go`（`POST /tasks/{id}/deliver`）、`web`（按钮、待你处理列表）。

**优点**：直接回应“不是每个任务做完都要立即提交”；不新增任务状态，没有状态机层面的歧义；交付和工作解耦，可以批量推送。
**缺点**：需要一套“提醒”机制，否则 pending 会被遗忘。
**风险**：低到中。主要是批量交付会一次占满工位（见 §5.1），必须和“deliver 不计并发”一起做。
**向后兼容**：是。旧任务的 `delivery_state` 由数据推出：有 `pr_url` 的是 `delivered`，其余为空，当作 `none`。

### 方案 D（不推荐）：让 agent 或主管每次自己决定怎么交付

- 做法：去掉 sous 的交付步骤，在提示词里让 agent 按需 commit / push / 开 PR；或者让主管在建任务时用自然语言写上交付方式。
- 不推荐的原因：push 和开 PR 就会落到 agent 的工具调用里，失去 `deliver` 里的统一审批和摘要；中断恢复时容易重复 push（`recoveryNote` 现在专门叮嘱过不要重复）；rebase 和冲突处理、`CheckMerges` 都依赖 `pr_url` 这个结构化字段。
- 值得保留的一点：**主管可以在建任务时填 `delivery` 字段**（方案 A 的任务级覆盖），这比写进自然语言可靠。

## 5. 与现有机制的交互

### 5.1 并发和工位

- **deliver 不计并发**，这是方案 C 的前提：Hub 的 `Busy`（`nodehub.go:475`）不统计 `purpose = deliver`，Node 的 `execute()`（`run.go:125-133`）在拿 `sem` **之前**就分流到 `deliver`。rebase 要启动真正的 agent，继续计数（和 e2e 评估 4.3 的结论一致）。
- 为了避免批量交付时同一仓库上的 git 操作互相踩，Node 侧给 deliver 一把**按仓库的互斥锁**，不用全局信号量。
- push 审批期间 deliver Run 一直是 running。不计并发以后，它不会再挡住别的任务。
- in_place 的目录锁（方案 B）**不占工位，也不进 `Busy`**，只在 `tryStart` 里检查，所以不会和工位计数形成循环等待。锁按任务状态推导，不单独存储，任务进入任何非活动状态就自动释放，不会泄漏。

### 5.2 失败、重试和中断恢复

- `MaxWorkRuns` 和 `MaxRebases` 不变。`commit` 档位没有 rebase；`push` 档位没有 PR，也就查不到冲突，不会触发 rebase。
- 中断恢复（`advance.go:108-127`）：deliver Run 中断后重新开始，`Commit` 和 `Push`（`--force-with-lease`）本身是幂等的；`PullRequest` 会先查已有的 PR（`workspace/deliver.go:86-87`）。按档位拆分以后，每一步都要保持这个性质，**在 Hub 里按 `delivery_state` 判断走到了哪一步**，不要让 Node 自己猜。
- in_place 的恢复说明不能再提 `git status` 和 `git log`（`advance.go:133-135`），改成“检查目录里现有的文件”。sous 可以把影子快照的 diff stat 附在恢复说明里。

### 5.3 验收结论

- **worktree**：验收不通过 → 重做 → 3 次后 blocked。改动始终在隔离分支上，**用户目录不受影响**。
- **in_place**：验收不通过时，**改动已经落在用户目录里**。规则：
  - 重做（rework）在现有改动上继续，和 worktree 一致；
  - 最终 blocked、failed 或用户取消时，**不自动回滚**（可能有用户想保留的部分），汇报里写清楚“改动已写入目录，可以回滚到任务开始前”，并附上回滚操作；
  - 没有配置验收模型时会自动通过（`advance.go:243-245`）。in_place 下这意味着没有任何检查，改动就当作完成了，汇报里要标出“未经验收”。
- 非 git 项目的 diff 来自影子快照，reviewer 拿到的信息和 git 项目一样。快照建不了时，reviewer 只能看 agent 的总结，验收能力会明显下降，所以 §4 方案 B 才要求“快照失败就不启动或降级权限”。

### 5.4 审批

- `commit` 档位：sous 在自己的 worktree 和分支上提交，不碰远端，**不需要审批**（现在 `Commit` 也是不审批直接执行的，`node/deliver.go:36`）。
- `push` / `pr`：保持现在的 `git-remote` 审批。
- 回滚：按 `Irreversible` 交给用户，不交给审批 agent。
- `in_place + commit`（在用户当前分支上提交）会改动用户自己的分支，如果开放这个组合，应当按 `git-local` 之类的类别询问用户。**暂不建议开放**，见 §8。

### 5.5 其他受影响的地方

- `cleanup` / `cleanupNode`：要改成按“工作区”判断而不是按 `kind`。这样同时修掉 §1 第 4 条的 general 任务 worktree 泄漏：general 任务在 git 项目里也应该有交付档位，否则就不该建 worktree。in_place 不删目录。`pending` 的任务删 worktree 前必须已经提交到分支，`commit` 档位保证了这一点。
- `CheckMerges`（`workbench.go:523`）：只对 `pr` 档位有意义。项目同时设置“合并后自动部署”和非 `pr` 档位时，保存项目要给出提示。
- `Dispatch` 的拒绝规则（`engine.go:494`）不变：已开 PR 的任务不能重新派发。
- 前端 `stages`（`status.ts:151`）：不要再靠 `status_reason.startsWith('正在交付')` 判断阶段，改为读 `next` 和交付档位。

## 6. 配置放在哪里、迁移与默认值

| 层级 | 字段 | 存储 | 说明 |
|---|---|---|---|
| 全局默认 | `default_delivery` | `settings` 表（和 `default_mode` 同一处，`engine.go:44`） | 内置默认 `auto`（见下） |
| 项目 | `delivery`、`workspace` | `projects` 新列，`project.put` 事件新字段 | 项目设置页里编辑 |
| 岗位（可选） | `delivery` | `personas` 新列 | 例如“审查”“调研”岗位默认 `none` / `commit` |
| 任务 | `delivery` | `tasks` 新列，`task.created` / `task.updated` 新字段 | 建任务表单、`task_create` / `task_update` 工具 |

- **解析时机**：
  - 工作区（worktree / in_place）在**第一次 work Run 时**确定并记在任务上，之后不能改，因为中途切换会丢改动；
  - 交付档位在**验收通过时**解析，这样用户在任务执行期间改主意（比如“这次先别推”）仍然生效。
- **`auto` 的含义**（推荐作为内置默认）：
  - 项目有 origin 远端 → `pr`（和现在一样）；
  - git 仓库但没有 origin → `commit`。现在这种情况会在 push 时报“仓库没有名为 origin 的远端”然后 blocked（`workspace/deliver.go:55-56`），改了之后体验更好；
  - 不是 git 仓库 → `in_place` + `none`。
  - 判断依据：Hub 已经有项目的 `RepoURL` / `RemoteKey`，以及扫描上报的 `ProjectNode.Git`；手动登记的路径由 Node 在 prepare 时上报实际情况。
- **迁移**：只在 `migrations` 末尾追加新版本（加列，默认值 `''`）；事件 payload 的新字段都是 `omitempty`，旧事件重放得到空值，也就是“继承”。不往迁移里插数据，和现有约定一致。
- **向后兼容**：所有现存项目和任务解析到 `auto` → 有 origin 的仓库得到 `pr`，行为不变。唯一的行为变化是“无 origin 的 git 仓库”从“交付失败、受阻”变成“本地提交、完成”。这一点要告诉用户（§8）。
- **Hub 和 Node 版本错开**：Node 在 inventory 里上报 `features: ["delivery-steps", "in-place"]`。Hub 只把 `commit` / `push` 档位的 deliver Run 和 in_place 的 Run 派给支持的 Node；旧 Node 只接 `pr` 档位，和现在一致。

## 7. 推荐方案与优先级

**推荐：A + C 一起做（交付档位 + 待推送子状态），B 随后做（就地写入），D 不做。** 实施顺序：

| 优先级 | 内容 | 理由 |
|---|---|---|
| P0 | deliver 不计并发（`Busy` 和 `sem` 两处） | 现有问题。方案 C 的批量交付依赖它，否则会制造新的排队卡死。 |
| P0 | 修 general 任务在 git 项目里建 worktree 后既不交付也不清理（`advance.go:220,367,415`） | 现有的状态泄漏，改动量小。在交付档位落地之前，至少先在汇报里告诉用户分支名，并纳入 `cleanup`。 |
| P1 | 方案 A：`delivery` 档位、解析链、`review()` 分叉、`node/deliver.go` 拆成三步、提示词、前端阶段名 | 直接回应“不是所有任务都要立即 push 和开 PR”，改动集中，风险低，完全向后兼容。 |
| P1 | 方案 C：`commit` 档位兼作“待推送”，`delivery_state`、`Engine.Deliver`、按钮和 `task_deliver` 工具、待你处理列表 | 和 A 共用同一套数据，一起做成本最低。 |
| P2 | 方案 B：`in_place` 工作区、影子快照、目录锁、回滚、权限前提 | 解决非 git 项目（demo-th）。改动最大，需要一条新的决策记录，建议在 A/C 稳定之后单独做。 |
| P3 | `auto` 默认值的自动判断、pending 超期提醒、岗位级 `delivery`、归档前确认 | 体验优化，不影响正确性。 |

**推荐理由**：

- 问题的核心是“验收之后要不要、要多少交付”，A 和 C 正好对准这一点，不需要动工作区和权限模型；
- 把“暂存”实现成 `commit` 档位加一个子状态，不新增任务状态，避免状态机出现歧义，也不影响依赖判断；
- B 牵涉到权限放行的前提（`EditWorktree` 自动放行）和对用户目录的破坏性操作，应该单独评审、单独落地，不要和 A/C 混在一个改动里。

**明确保留的部分**：

- 计划和验收两步不变；
- git 项目默认使用隔离 worktree；
- sous 永不合并主分支（0009）；
- push 和开 PR 必须经过权限审批；
- 交付是 sous 自己执行的结构化步骤，不交给 agent（方案 D 不做）；
- effects 队列推进和处理函数幂等的约定。

## 8. 待用户确认

1. **内置默认值**：用 `auto`（有 origin 才开 PR），还是保持 `pr` 不变、只有显式配置才改变行为？`auto` 会让“无 origin 的 git 仓库”从受阻变成完成，这是一个可见的行为变化。
2. **是否开放 `push`（只推送、不开 PR）**：它主要服务于没有 `gh` 或者不在 GitHub 上的仓库。不开放也可以，相当于这类仓库只能用 `commit`。
3. **in_place 是否允许用在 git 仓库上**（直接改用户的 checkout，不建分支）？如果允许，要不要再开放“在用户当前分支上提交”？它会改动用户自己的分支，建议先不开放。
4. **in_place 下的编辑权限**：快照成功时编辑照旧自动放行（推荐），还是在 in_place 下一律询问用户？后者更安全，但会非常打扰。
5. **影子快照的上限和排除规则**：默认上限设多少（建议 200 MB 或 2 万个文件）？超出上限时拒绝启动，还是降级为“编辑询问用户、不提供回滚”？
6. **待推送提醒**：多久提醒一次（建议 3 天提醒一次，之后不再提醒）？要不要有“过期自动 discard”？建议不要。
7. **岗位级 `delivery`**：要不要加？例如“审查”岗位默认 `none` 或 `commit`。本任务就是一个例子：岗位要求“只出文档、不建 PR”，现在仍会走 PR 流程。
8. **验收失败后的 in_place 改动**：默认保留并提供回滚（推荐），还是默认自动回滚？
9. **决策记录**：方案 B 会改变 0009 的前提（交付不再总是 PR，也不总在 worktree 里），是否新增一条决策（比如 0022“交付档位与就地写入”）来补充 0009，而不是修改它？
