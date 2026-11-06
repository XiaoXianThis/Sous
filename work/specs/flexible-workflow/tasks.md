# 任务

- [x] 1. 去掉预设：`workspace.startPoint` 删掉 fallback 顺序，起点改成必填的 `{branch, from}`；任务记录 `base_branch` / `base_from` / `base_commit`；`rebasePrompt` 删掉 main 兜底；`project_create` 的 `layout` 改成必填并删掉三处“默认 wokspace”的说法；单测覆盖“本地有未推送的提交”和“分支不存在”
- [x] 2. 任务属性：迁移加 `place`、`review`、`on_fail`，旧数据按设计 §2 转换；`task.Create` 不再推断流程，`task_create` 工具参数同步；`kind` 只作标签
- [x] 3. 路由和就地执行：`task.route` 把机器角色改成偏好；就地任务按“机器 + 目录”排队；git 仓库里就地改的快照和 diff；没有 git 的目录用影子 git 做快照、diff、撤销；Node 检查就地目录必须在项目登记的目录内
- [x] 4. `workPrompt` 按工作位置生成说明，去掉写死的“sous 会开 PR”
- [x] 5. 验收：`Advance` 按 `review` 分流；`agent` 验收的 Run（只读加测试）；`user` 验收进入“要你处理”；没配模型时退化成 `user`；`on_fail` 和 `MaxWorkRuns` 上限；`task_review` 工具；单测覆盖四种验收方式和不通过的两种处理
- [x] 6. 任务终点：删掉 `PurposeDeliver`、`PurposeRebase`、`afterDeliver`、`MaxRebases` 和 `node/deliver.go` 的自动交付；任务结束不清理 worktree，清理改由合并和丢弃触发；`cleanupNode` 按分支状态判断
- [x] 7. Node 的 `git.op`：status、commit（含 squash）、merge（rebase 后只快进；主 checkout 检出时要求干净）、push（无 force）、pr、undo（只撤回没推送的，读 `gitops.jsonl`）、discard；每个操作过策略；单测用临时仓库覆盖每个操作和冲突、脏工作区、已推送不能撤回
- [x] 8. 主管的 git 工具：`git_status`、`git_commit`、`git_merge`、`git_push`、`git_pr`、`git_undo`、`git_discard`；`git.op_done` 事件；每次操作的一句话汇报
- [x] 9. 部署触发：部署配置加 `branch` 和 `deployed_commit`；daemon 的后台 fetch 报告分支 HEAD；部署分支有新提交时建运维任务；sous 推送部署分支时立即检查；删除 `CheckMerges` 的 PR 轮询
- [x] 10. 只读策略档位 `readonly`：只放行读操作，其余直接拒绝并把理由回给 agent；Hub 不能放宽；策略单测
- [x] 11. `project_read`：Node 用 Go 实现 read / list / grep，路径限制在项目目录内（解析符号链接后），大小上限，跳过二进制
- [x] 12. 顾问会话：`consult` 用途的 Run、`consult_sessions` 表、`idle` 状态和空闲超时、`session/load` 恢复（不支持时带摘要新开）、`consult` 工具、顾问消息写进线程；逐个实测各 agent 的会话恢复，结果记进本目录
- [x] 13. 转成任务：`task_create` 的 `from_consult`；就地任务沿用顾问会话并切换策略；worktree 任务先实测原会话能否改到 worktree 路径，不行就带交接摘要新开
- [x] 14. 主管提示词：讨论和派活的判断方法、读项目的两种方式、建任务时选属性并说明、git 的判断方法和习惯记忆；删掉“永不合并 PR”“默认 wokspace”；用 mock provider 的回合测试覆盖“提问不建任务”“明确指令建任务”“有歧义时提问”
- [x] 15. 页面：顾问消息的样式和状态；任务详情的新字段和分支状态；项目卡“N 个分支未合并”；`status.ts` 加“未合并”；去掉“PR 待合并”的默认终点文案
- [x] 16. e2e：讨论（连续追问、空闲后恢复）→ 转成任务 → 不验收直接完成 → 用户说“合并推送” → 合入当前分支并推送 → 部署分支有新提交触发运维任务；另跑一条没有 git 的目录：就地修改、看 diff、撤销
- [ ] 17. 真实模型验收：主管在真实对话里区分讨论和派活、遇到歧义会问、git 操作汇报准确（结果见下面“验收记录”）
- [x] 18. 文档：更新 `docs/architecture/workbench.md`（任务、代码从写到部署）、`docs/api/core-hub-node.md`、`docs/api/core-hub-http.md` 和事件列表；core 和 web 的 `AGENTS.md`（如有命令或依赖变化）

## 实现时的取舍

- 顾问会话没有单独的表，用 consult Run 加 `consult.closed` 表示（见 design §7）；git 操作的结果放在 `run.ended` 里，没有单独的 `git.op_done` 事件。
- 主管的工具调用是并行的，同一个项目的 `consult` 由 `consultLock` 串行，否则两个问题会开两个会话（真实模型验收时发现）。
- 页面上点“部署”建的运维任务写明 `place: in_place`、`review: none`：这是按钮本身的含义，不是替主管定的默认。

## 还没有实测

- 任务 12、13 要求逐个实测真实 agent（claude、codex、cursor、opencode、kiro）的 `session/load`，以及顾问会话转成 `worktree` 任务时能否在另一个工作目录里加载。自动测试只覆盖了假 agent（支持和不支持恢复两种）；真实 agent 不支持或加载失败时，Node 会开新会话、提示里带着讨论摘录，不会失败，但会丢掉 agent 自己的上下文。

## 验收记录（2026-11-06，DeepSeek v4.1 flash 做主管，agent 是假 agent）

- 提问“登录怎么做的、记住我怎么做比较好”：主管用 consult 讨论，没有建任务，最后问“要我动手吗”。发现并修复：并行的两个 consult 开了两个会话（加了 consultLock）；顾问 Run 被放到了杂项卡片上（按 project_id 归类）。
- “开始做”：建了 worktree 任务，起点是当前分支 feature-x（local，含未推送的提交），不是 main；带上了顾问会话。验收模型正确地判定假 agent 没有实现，返工 3 次后受阻。
- “加 hello.txt，不用验收”：主管选了就地执行、不验收，先看了 git_status。
- “提交推送”：只把任务的文件提交到 feature-x，再推送 feature-x（等用户批准），汇报准确。发现并修复：就地任务原来没法提交（加了 CommitInPlace）。
- 没测到：有歧义时主管是否会问（这次它直接推了没有上游的 feature-x；之后在提示词里把“推送远端还没有的分支”列为要先问的情形，还没用真实模型复测）；真实 agent 的会话恢复。
- 之后修正：聊天里就地任务的提交显示成“提交任务的改动”（原来写成“提交任务分支”）。
