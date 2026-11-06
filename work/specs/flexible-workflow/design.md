# 设计

## 原则

流程交给主管判断，安全交给 Node 的确定性规则。代码里只保留两类东西：主管能调用的工具（每个参数都显式），和 Node 上不能被 Hub 放宽的策略。任何“没说就用 X”的地方，要么改成必填参数，要么改成主管提示词里的判断方法。

## 1. 去掉预设（R1）

- `workspace.startPoint`：删掉 fallback 顺序。调用方必须给出起点：`{branch, from: "local" | "remote"}`。`local` 用本地分支的 HEAD（含未推送的提交），`remote` 先 fetch 再用 `origin/<branch>`。分支不存在、仓库没有提交时报错，由主管处理。
- 任务记录起点：`base_branch`、`base_from`、`base_commit`，任务详情里显示。
- `rebasePrompt` 里 `base == "" → "main"` 的兜底删掉。
- `workPrompt` 不再写死“不要 push、不要创建 PR、sous 会开 PR”。按任务的工作位置生成说明：worktree 时说明“当前目录是任务分支的 worktree，提交到这个分支，不要 push、不要切换分支，交付由 sous 按用户的要求处理”；就地时说明目录和“改动会被快照，可以撤销”。
- `project_create`：`layout` 改成必填，去掉“默认 wokspace”的描述、环境说明（`tools.go` 里两处）和主管提示词里的同类说法。

## 2. 任务属性（R4）

`tasks` 表新增列（迁移）：

| 列 | 取值 | 说明 |
|---|---|---|
| `place` | `worktree` / `in_place` / `scratch` | 在哪儿做 |
| `review` | `none` / `model` / `agent` / `user` | 验收方式 |
| `on_fail` | `rework` / `ask` | 验收不通过时 |
| `base_branch`、`base_from`、`base_commit` | | 起点（`place = worktree`） |

- `kind` 保留，只作为标签（显示、统计、默认岗位的提示）；`task.Create` 不再从项目推断流程，`place` 和 `review` 必填。旧数据迁移：代码 → `worktree` + `model` + `rework`；一般 → `scratch` + `model` + `rework`；运维 → `in_place` + `none`。
- **路由**（`task.route`）：机器角色变成偏好而不是规则。任务指定了机器就用那台；否则 `in_place` 用项目在哪台机器上登记的目录（运维标签优先部署目标），`worktree` / `scratch` 优先开发机。服务器上的严格本地策略不变，由 Node 按自己的角色执行，不看任务标签。
- **就地执行**：
  - 同一台机器的同一个目录，同一时间只跑一个就地任务（排队），worktree 任务不受影响。
  - git 仓库里就地改：开始前记录 HEAD 和 `git stash create` 的快照，diff 和撤销都基于它。工作区有用户自己未提交的改动时，主管要先问用户。
  - 没有 git 的目录：影子 git，`GIT_DIR=~/.sous/shadow/<目录哈希>`、`GIT_WORK_TREE=<目录>`，开始前 `add -A` + `commit` 一次快照，结束后 diff。撤销 = 把快照里的文件恢复并删除新增的文件（走策略审批，属于写入）。
- 就地执行的 `workdir` 必须是项目登记的目录或它的子目录（Node 检查）。

## 3. 验收（R5）

`Advance` 在工作 Run 成功后：

| `review` | 动作 |
|---|---|
| `none` | 任务完成，汇报 agent 的总结 |
| `model` | 现有的 reviewer 模型看 diff（`verdict`） |
| `agent` | 新建一个 `purpose = review` 的 Run，派给审查岗位，在同一个 worktree / 目录里只读加跑测试（策略用只读档位，放行测试命令按 `Classify` 的测试类），输出 `{pass, reason}` |
| `user` | 任务进入“要你处理”，用户在任务详情或对话里给结论 |

- 没配验收模型时不再自动通过：`model` 退化成 `user`，并在汇报里说明。
- 不通过：`on_fail = rework` 自动返工，`MaxWorkRuns`（3）仍是硬上限；`ask` 或到达上限时任务受阻，给主管发一条【跟进】，由主管决定返工、换 agent 还是问用户。
- 主管新增工具 `task_review(task, by, note)`：对已完成的任务追加一次验收（比如用户说“跑一遍测试给我看”）。

## 4. 任务终点和 git 工具（R6、R7、R8）

### 终点

- 工作 Run 通过验收（或不验收）后，任务直接“已结束”，`status_detail` 写“改动在 `<branch>`，未合并”或“已就地修改”。删除 `PurposeDeliver`、`PurposeRebase`、`afterDeliver`、`MaxRebases`。
- worktree 跟着分支走：任务结束不清理；`git_merge` 合并后、或用户让主管丢弃分支时清理（`cleanup` 改成由这两处调用）。`cleanupNode` 的离线补清理改为按“分支已合并或已丢弃”判断。
- 工作台的项目卡上显示“N 个分支未合并”，任务的 badge 加“未合并”。

### 主管的 git 工具

Node 新增一个 `git.op` 请求（`docs/api/core-hub-node.md`），只执行写死的命令，参数全部显式：

| 工具 | 参数 | Node 做什么 |
|---|---|---|
| `git_status` | project, node | 实时状态：当前分支、detached、上游及领先 / 落后、未提交的文件数、最近活跃的分支、远端默认分支（仅供参考）、各任务分支相对起点的提交数 |
| `git_commit` | task, message?, squash | 在任务的 worktree（或就地目录）里 `add -A` + commit；`squash` 时 `reset --soft <base_commit>` 后一次提交。message 不填时由 agent 写的总结生成 |
| `git_merge` | task, into, squash | 把任务分支 rebase 到 `into`，然后只快进合并。`into` 在用户的主 checkout 里检出时，要求那边工作区干净，在主 checkout 里 `merge --ff-only`；否则 `git fetch . <task>:<into>`（只快进）。有冲突就停下，返回冲突的文件 |
| `git_push` | project, node, branch, remote? | `git push <remote> <branch>`；不支持 force，受保护分支更不行 |
| `git_pr` | task, base, title?, body? | push 任务分支并 `gh pr create --base <base>` |
| `git_undo` | project, node, target | 撤回最近一次 sous 做的、还没推送的 commit 或合并：检查目标分支不在上游里，`reset` 回操作前记下的提交 |
| `git_discard` | task | 删除任务分支和 worktree（要用户确认） |

- 每个操作都过 Node 的策略：`git_push` / `git_pr` 是 git-remote；`git_merge` 改动用户的主 checkout 时算 `outside-worktree` 写入；按权限模式审批。force push 到受保护分支是硬信号，这里根本不提供。
- 每次移动分支后，Node 把操作前后的提交记进 `~/.sous/gitops.jsonl`，供 `git_undo` 使用（已经在任何远端分支上的不能撤回）。
- 工具调用等这个 Run 结束（最多 `GitWait`，5 分钟，包括等用户批准），主管拿到结果用一句话汇报；没等到时由 `afterGit` 在对话里发一条汇报。
- 冲突：`git_merge` 返回冲突后，主管可以派一个工作 Run（`purpose = work`，提示里写明“把分支 rebase 到 `<into>` 并解决冲突”），完成后再 `git_merge`。不再自动 rebase。
- 复杂操作（cherry-pick、拆 commit、改历史）主管建一个就地任务交给 agent，并在对话里说明要做什么。
- 主管每次 git 操作后用一句话汇报：“已把任务 A 合入 `feature-x`（快进，1 个提交），没推送”。

### 判断和习惯

写进主管提示词（见 §7），不写进代码：起点和目标按用户的话 → 当前对话里提到的 → `git_status` 的实时状态判断；状态和任务起点不一致（比如用户已经切了分支）、detached、看起来是别人的分支、两台机器当前分支不同，都要问。用户的说法有歧义时问一次，用 `memory_save` 记成带项目名的习惯（如“Sous 项目：‘提交推送’= 合到当前分支后推送”）。

## 5. 部署触发（R9）

- 项目的部署配置加 `branch`（必填才能开自动部署）和 `deployed_commit`。
- daemon 的后台 fetch（现在每 10 分钟）顺带报告项目各远端分支的 HEAD；Hub 发现部署分支的 HEAD 不等于 `deployed_commit` 就建运维任务，任务完成后更新 `deployed_commit`。
- sous 自己 `git_push` 推了部署分支时立即检查一次，不等下一轮 fetch。
- 删除 `CheckMerges` / `watchMerges` 的 PR 轮询；`DeployedSince` 的去重改成按提交。

## 6. 只读档位和 `project_read`（R2、R8）

- Node 策略新增 `readonly` 模式（`internal/policy`）：只放行 `Classify` 归为读的操作（读文件、列目录、grep、`git log/show/diff/status` 等），其余一律拒绝，不问用户、不问审批 agent，拒绝理由回给 agent。Hub 不能把只读 Run 改成别的模式；只读 Run 也不能请求升级。
- `project_read`（主管工具）：`{project, node?, op: read | list | grep, path, pattern?, range?}`。Node 用写死的实现（Go 读文件，不起 shell），路径必须在项目目录内（解析符号链接后再检查），单次结果最多 4 万字，二进制文件不读。

## 7. 顾问会话（R2、R3）

- 新的 Run 用途 `purpose = consult`，属于项目，不属于任务（`runs.project_id`）。没有单独的表：一个项目当前的顾问就是它最新的、没有关闭（`runs.consult_closed = 0`）的 consult Run（`store.Consultant`）；会话 id 记在 `runs.session`（`run.turn` 和 `run.ended` 带来），换话题或转成任务时追加 `consult.closed` 事件把它关掉。
- 运行在项目登记目录（主 checkout），不拍快照，Node 强制 `readonly` 档位。不占机器的并发槽位（Node 不取信号量，Hub 的 `Busy` 不计它）。
- **空闲和恢复**：一轮回答结束后发 `run.turn`（完整回答和会话 id），然后等下一个 `run.message`；空闲超过 `consult_idle_minutes`（node.json，默认 30）就结束 agent，`run.ended` 的 `stop_reason` 为 `idle`、带 `session_id`。下一个问题新建一个 consult Run，`resume_session` 指向那个会话；agent 不支持恢复时 Node 新开会话，Hub 在提示里带上之前几轮回答的摘录。daemon 重启时，存着的顾问 Run 以 `idle` 结束，不在中途恢复。
- **工具 `consult`**：`{project, question, new_topic?, machine?}`。同一个项目的提问由 `consultLock` 串行（主管一轮里的多个工具调用是并行的），所以两个问题会进同一个会话。主管等回答（最多 `ConsultWait`，10 分钟）：回答写成一条 `kind: consult` 的助手消息（`source` 是 Run、agent、项目、机器，`reply_to` 是这一轮回答的用户消息），同时作为工具结果交给主管，并提醒它不要复述。没等到时，回答到了由 `lateAnswer` 照样写进对话。
- 在主管的历史里，顾问的消息拼在那一轮的回复前面，带“项目顾问的回答，用户已经看到原文”的标记；它不算主管对用户消息的回复（`unanswered`），但算这一轮说过话（`hasStep`），所以主管可以不再补充文字就结束这一轮。
- **转成任务**：`task_create` 的 `from_consult` 取走当前的顾问会话（`takeConsultant`）：记下 `handoff {node_id, agent, session_id, note}`（`note` 是最近几轮回答的摘录），停掉还在等问题的顾问，关闭它；任务的机器和 agent 默认就是顾问的。任务的第一个工作 Run 带 `resume_session`，提示前面附上讨论摘录；`worktree` 任务的工作目录和顾问不同，agent 加载不了会话时 Node 就开新会话（摘录保证不丢上下文）。
- 顾问的 token 用量由 agent 自己计；巡检对它不做特殊处理。

## 8. 主管提示词

替换 `prompts.go` 里“派活”一节中与新设计冲突的部分：

- 讨论还是派活：问题、想法、“你觉得 / 能不能”是讨论；指令明确且范围清楚才建任务；拿不准按讨论处理，最后问一句；讨论收敛时提议“拆成这几个任务，开始吗？”。用户说“先别做”就停。
- 读项目：一两个文件的事用 `project_read`；需要通读代码、连续追问时用 `consult`；顾问的回答已经显示给用户，不要复述。
- 建任务时按场景选 `place`、机器、`review`、`on_fail`，并在派发时一句话说明：“交给后端岗位，在 MacBook 的 worktree 里做，基于 `feature-x`（本地，含 2 个未推送的提交），做完由审查岗位跑测试。”
- git：照用户的话用 git 工具；不预设分支，先看 `git_status`；拿不准就问；第一次遇到有歧义的说法问一次并记住；每次操作一句话汇报；不要自作主张自动合并或推送。
- 删掉“sous 永远不合并 PR；合并后才部署”和“新建项目默认 wokspace”。

## 9. 页面

- 对话里的顾问消息：左侧色条加头像（agent 图标）和“顾问 · 项目名”，可折叠；唤醒中、回答中的状态。
- 任务详情：显示工作位置、起点、验收方式；结束后显示分支和“未合并 / 已合并 / 已推送 / PR 链接”。
- 项目卡：“N 个分支未合并”；项目详情列出这些分支。
- 去掉“PR 待合并”作为默认终点的文案；`status.ts` 里加“未合并”。
- 新建任务的页面表单（如果有）同步新增字段。

## 10. 接口和事件

- 事件：`task.created` 带新属性；新增 `task.git`（任务分支的去向）、`project.deployed`、`run.turn`、`consult.closed`；git 操作的结果在 `run.ended` 的 `git` 里（不另设事件）。删除 `PurposeDeliver` 相关的状态文案。
- Hub ↔ Node：`git.op` 请求和结果、`checkout.state` 增加分支 HEAD、`project.read` 请求、Run 的 `idle` 状态和 `consult` 用途、只读策略档位。
- 页面接口：任务详情返回新字段；顾问会话的列表和结束；git 操作历史。
