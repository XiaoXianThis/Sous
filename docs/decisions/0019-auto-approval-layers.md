# 0019 自动审批分层：确定性规则、审批 agent、硬上限

- Status: Accepted
- Date: 2026-11-05

## Context

0005 的自动审批把几乎所有需要审批的操作交给一次审批模型调用，只有不可逆操作直接给用户。用起来有三个问题：

- 太依赖模型：推 PR 分支、删 `node_modules` 这类每次都一样的小事也要等一次模型调用，慢、花钱，模型偶尔还会误判。
- 模型看到的太少：只有任务标题、命令和类别，看不到这个任务里之前的审批，也没法拒绝，只能“批准”或“交给用户”。
- 底线有缺口：分类只看命令的第一个词，`cat ~/.ssh/id_rsa`、`echo x >> ~/.zshrc`、`env rm -rf x`、`find -delete`、`bash -c '…'`、`echo $(rm -rf x)` 都会被归成只读或普通命令，在默认模式下也直接放行。

## Decision

自动审批分三层，越靠前越确定，越靠后越灵活；安全判断总是先于“聪明”。

1. **Node 的确定性规则**（`internal/policy`，Hub 不能改）：
   - `Classify` 补上绕过口子：解开 `env`、`nohup`、`timeout`、`xargs` 和 `sh -c '…'` 看里面真正的命令；检查 `$(…)`、反引号和 `<(…)` 里的命令；`>` 重定向写到工作目录之外算 `outside-worktree`；`find -delete / -exec`、`sed -i`、`sort -o` 按写入处理；`git remote add`、`git config <设置>` 不再算只读；新增 `terraform`、`kubectl`、`helm`、`python -m …`、`node --test` 的细分。
   - `Assess` 给每个请求算**高风险信号**（硬）和**提示**（软）。硬信号包括：读写凭据和密钥、向外发送数据、远程登录、混淆或下载后执行、写入持久化位置（cron、启动项、shell 配置）、改动 git 的远端或钩子或全局配置、绕过检查（`--no-verify`、关闭 TLS 校验）、提权、特权容器、改 GitHub 权限或合并 PR、从非默认来源安装依赖、push 到受保护分支。
   - `Judge` 把模式表和评估合在一起：有硬信号的请求**一律交给用户**，不经审批 agent，连表里本来直接放行的读和改也一样（完全开放模式除外，它只受底线规则约束）。
   - 自动模式下，**常规操作**不再调用审批 agent：`git push [-u] <remote> <非受保护分支>`、`gh pr create/view/…`、删除 `node_modules`、`dist` 等构建产物和缓存。记录成一条“常规操作”的放行记录。
   - `node.json` 的 `policy.extra_hard`（追加硬信号，Go 正则）、`policy.routine_disabled`（常规操作也交给审批 agent）。从 Hub 远程编辑 node.json 只能收紧：不能删掉已有的 `extra_hard`，不能把 `routine_disabled` 改回 `false`。
2. **审批 agent**（Hub，`task/approve.go`）处理剩下的请求，现在能 `allow`、`deny`、`ask_user` 三选一，并给出 `confidence`；`allow` 要有 `high`（开了 `allow_medium` 且请求符合条件时 `medium` 也可以），`deny` 在 `high` 或 `medium` 时生效，`low`、`ask_user` 和无效的结论都转给用户。`deny` 的理由会回给 agent，让它换办法，不打扰用户。它能看到任务、agent 收到的说明、风险信号，以及这个任务里最近的审批（含用户的决定）。提示词按操作类别写了判断标准和“永远不批准”清单。
3. **硬上限**（Hub，代码里写死，不看模型怎么说）：
   - `irreversible` 和 `floor` 类别不交给审批 agent（和 Node 的判断再锁一道）。
   - 这个任务里用户已经答复过**同样的操作**（类别、标题、说明都相同）：照用户的答复办，不再问模型。
   - 一个 Run 里审批 agent 最多批准 150 个操作，之后都交给用户。
   - 同一个操作被审批 agent 拒绝 3 次后，再遇到同样的操作就交给用户（agent 在反复重试，用户应该知道）。
   - 没有审批模型、调用失败三次、返回的不是约定的 JSON：都交给用户。

## Alternatives

- 全部交给模型、靠提示词约束：模型被网页或文件里的指令带偏时没有第二道锁。
- 全部写成规则、不要审批 agent：规则覆盖不了“这条命令对这个任务合不合理”。
- 让审批 agent 自己学习用户的习惯：难以审计，也容易被一次误点批准带歪。现在只在同一任务里复用用户对**完全相同操作**的答复。

## Consequences

- 在自动模式下用户被打扰的次数明显减少（常规操作、同一操作重复、被拒绝后换办法），而硬信号一旦命中，不管模型怎么想都要用户点头。
- 在默认模式下，命中硬信号的“只读”命令（比如 `cat ~/.ssh/id_rsa`）也会问用户，比 0005 的表更严。
- 硬信号按命令文本匹配，宁可多问：`ssh`、`nc`、`.pem` 之类的词出现在别的位置也会触发。这仍然是安全网，不是隔离（0010）。
- 交付时 push 任务分支（`git push -u origin <分支>`）在自动模式下由 Node 直接放行，不再经过审批 agent；强制 push（`--force-with-lease`）不是常规操作，仍然交给审批 agent。
