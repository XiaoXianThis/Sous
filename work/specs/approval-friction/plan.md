# 审批摩擦修复实施方案

核对日期：2026-11-09。依据：[report.md](report.md) 全文（439 行），当前起点 `c0af689`，分支 `sous/mged6k017v`。本次只产出方案，不修改产品代码，不提交、推送或合并。本文中的源码路径均相对仓库根；行号是实施前的定位锚点，实施时按函数名查找。

## 1. 交付目标与约束

先修复确定性的解析、目录和证据缺失问题，再调整需要结构化风险事实支撑的审批策略。目标是减少误分类、无根据的拒绝和重复询问，不把“模型觉得安全”当成权限边界。主管已决定允许引入 `mvdan.cc/sh`，允许审批 agent 按受控只读通道查看 `~/.zshrc`、`~/.ssh/config`；这些决定不再请求确认，随 P1、P2、P4 实施。

报告的 16 行样本是验收编号，不是 16 个都应该放行的误报：#9 的审慎要求、#10 的全盘搜索拒绝应保留；#12 包含两个 MCP 工具，#9 对应两次拒绝。表中规定可确定的分类与路由，模型最终批准需以证据和任务相关性为条件，不能承诺真实模型必然批准。

### 1.1 不允许放宽的边界

| 边界 | 实施必须保留的行为 | 当前源码锚点 |
|---|---|---|
| floor | 在任何解析、数据排除、MCP 特判、授权复用前，对完整原命令执行已有 floor 匹配；`rm -rf /`、格式化、磁盘写入、fork bomb、关机、下载接 shell 等仍 Deny。正文、注释、引号内命中也不得因本次优化移除。保留现有本机 floor 配置语义，不新增远程关闭路径。 | `apps/core/internal/policy/policy.go:135` `DefaultFloor`、`:175` `New`、`:211` `Classify`、`:268` `floorHit` |
| server | `open` 仍降为 `default`；`EditWorktree/Delete/Service/Irreversible` 在非 restricted 模式仍 AskUser，不能被 routine、P8、P9 或证据包改成 AI/直接允许。readonly/verify 仍按现有受限表拒绝不允许的类别。脚本内已知的删除/服务副作用也必须合并到类别。 | `policy.go:106` `EffectiveMode`、`:117` `DecideFor` |
| irreversible / floor 不交 AI | 保留 node 路由和 hub `aiNever` 两把锁。P1 消除“文档字样被误认不可逆”不等于允许真实不可逆操作。P9 不自动复用这两类的允许记录。 | `policy.go:67` `Decide`；`apps/core/internal/task/approve.go:32`、`:55` |
| 秘密与外发 | 私钥、token、真实 `.env`、shadow、钥匙串内容导出/查询、提权、持久化写入、绕检查、外发等仍 Hard；readonly 直接拒绝，auto/default 交用户。P4 只对有证明的只读配置/目录观察分流，不把凭据内容降为 HardRead。 | `apps/core/internal/policy/assess.go:68` `hardSignals`；`apps/core/internal/node/look.go:28` `secretDump` |
| 受限执行和审计 | `machine_look` 和审批 inspect 继续由发起机器 node 判断 readonly；不传入“忽略策略”。保留 `recordPolicy`、`noteRefusal`、审批事件及 `run.ended.refused`，证据、命令、理由进入日志/模型前均脱敏。 | `apps/core/internal/node/run.go:787`、`:835`、`:891`；`node/look.go:33` |
| 失败保守处理 | shell 解析失败/不支持语法/预算耗尽，回退旧分类与全文 Assess，不按部分 AST 放行；取证失败不作安全证据；旧 node 不提供结构化事实时禁用 P8/P9 新允许路径。 | P1、P2、P8、P9 的兼容规则 |

### 1.2 源码核对结论及报告修正

已逐项读取并核对 P1–P10 所引用的主要函数。当前产品代码的这些锚点与报告的 `13ff1aa` 基线一致；报告提交只增加文档。实施前再次用 `git diff 13ff1aa -- apps/core` 确认是否漂移。

| 报告项 | 核对结果 | 必须纳入方案的修正 |
|---|---|---|
| P1 / §3.1–3.2、§3.6 | 成立：`splitter` 在 `policy.go:355`，`classifyBase:462` 用 `strings.Fields`；`Assess:133–160` 对全文及 paths 匹配并再次使用 splitter。machine_look 在 `node/look.go:48–54` 固定 readonly，并非 Trigger 的另一套审批。 | 不仅换 splitter；参数、重定向、执行上下文、Assess、脚本识别须用同一解析结果。heredoc 数据不能一律当惰性文本，未引用 heredoc 的命令替换、shell stdin、`-c` 等仍会执行。 |
| P2 / §3.4–3.5 | 成立：`Approve:102` 调 `askJSON`；`task/advance.go:562` 只调用一次 provider，无工具；`approverInput:181` 截断 detail 到 4000 rune。 | `git status` 只能证明“当前改动”，不能证明“本任务产生”；4 KB 是头部而非全文。追加到 detail 尾部会被再次截掉，需结构化证据。`inspector/inspector.go:361` 也是单次 JSON 调用，不能直接当工具循环复用。 |
| P3 / §4.3 | 成立：`outcome.go:156–175` 仅首词，已用 realpath 和普通文件检查，内容经 `mask`。 | `policy.unwrap` 未导出且基于空白词元，不能直接从 node 调用；共享 AST 的安全命令视图。解释器选项须识别 `-c/-m/-e/-`，不能误当文件。 |
| P4 / §4.4 | 成立：凭据/持久化文本信号不分读写；`ssh` 可误匹配 `journalctl -u ssh`，`installSource` 的 `-i` 扫整条命令。 | 报告建议 readonly 继续拒绝全部 `~/.ssh` 与主管允许审批 agent 读取 `~/.ssh/config` 的决定不一致：仅精确配置路径、窄读、脱敏允许；私钥仍拒绝。现有 `secretDump` 只拦环境变量/`.env`，不拦配置中内嵌 token；`look` 原样返回输出且原样记 command 日志，必须修复。 |
| P5 / §4.5 | 成立：循环没有 cwd 状态；`outside` 与 `redirectCategory` 都只收一个 workdir。 | 不能把 worktree 根一起换成子目录；不确定 `cd` 不能继续按根算成安全，须失去“在树内”的证明，并处理条件分支/子 shell 的 cwd 作用域。 |
| P6 / §4.6 | 成立：无命令/可识别 kind 的工具落入 `ExecOther`；内建 MCP 在 `sous_tools.go:38–98` 绑定 Run 和随机令牌。 | 精确名单只有三个已知工具，不能 `mcp__sous__*` 全放；title 不能是唯一可信身份，不用 agent 会话级总允许规则替代 node 审计。 |
| P7 / §4.7 | 成立：`readOnly:746` 缺报告列出的新命令，`systemctl:639` 缺 list-timers；`sudo` 是 Irreversible。 | “加命令名就只读”不成立：hostname 可改名、getent 有查询范围、xxd 可写输出、journalctl 还有写入选项。采用完整参数白名单，DNS 不默认加入 readonly。 |
| P8 / §4.8 | 机制成立，收益未证实：`:109` 实际是“不是 low/medium 就 sure”，空值/拼错也能通过，而非严格 high。 | 先修 confidence 枚举校验；category 不能证明 exec-other 可逆或 outside-worktree 只读。medium 必须有 node 结构化事实，不纳入 delete 的泛化豁免。 |
| P9 / §4.9 | 成立且有额外问题：`followUser` 仅复用拒绝；AI 路径 `userPrecedent` 把 allow_once/allow_always 都按 allow 复用，键为 category+title+detail。 | 两条路径一起修，只有 allow_always 可复用允许；原始命令不能粗暴压空白（会改引号内含义）。timeout/cwd/env 等影响执行语义，不能按报告建议一律剔除。脚本哈希必须来自完整原始内容。 |
| P10 / §4.10 | 成立：全 Run 的 ActorApprover 允许上限 150；计数扫描包括已复用的允许，但本次复用会在预算检查前返回。样本最多 26，缺收益证据。 | 先修复复用绕过预算、`TaskApprovals` 最早 500 条截断及预算原子性，再考虑统一提高上限；巡检不等于权限预算。P8/P10 增量需观测门槛，不能据小样本直接上线。 |

报告数据（41 条请求、16 行样本）的现场来源不在本仓库；本次不读取真实用户秘密、不访问远端、不重新声称拿到了 hub 全局统计。以下会明确区分源码确认、验收夹具和需要线上观测的数据。

补充边界发现：`policy.go:241–245` 的树外文件删除会先归 OutsideWorktree，`classifyBase:506–515` 也先按路径返回，而 `DecideFor:126` 只看最大 category 的 Delete。实施必须保留“删除/编辑/服务/不可逆”的基础效果，不让树外类别遮住服务器的用户边界；这不是把报告已有安全保证原封不动照抄。

调用链修正：报告 §3.5/§6 把 deliver 写成现存入口，但 `node/run.go:162–166` 已直接拒绝旧 deliver/rebase Run。当前应覆盖 ACP `onPermission` 与 `workbench.permit` 两个直接 judge 调用点；固定工作空间 setup 经 `workbench.go:230` 调 permit，Git 操作经 `gitop.go:82` 的 `allow` 调 permit。不能让实施 agent 去寻找或恢复已移除的自动 deliver。

## 2. 共同实现约定

### 2.1 一次解析，多个消费者

在 `apps/core/internal/policy/shell.go` 新增纯解析入口 `AnalyzeCommand(cmd, context)`，由 `Classify`、`Assess` 及 node 证据提取共享，不把“AST 打印回字符串”后再送给旧的 `strings.Fields`。旧函数保留为 `classifyCommandLegacy`、`assessLegacy`，仅供明确标记的回退路径和差分测试。

解析结果至少包含：解析状态/失败原因、语法类型、简单命令的 argv 与原始范围、赋值、重定向的输入/输出方向、命令替换/进程替换/子 shell、heredoc 的正文及展开属性、调用脚本候选、每个节点的 cwd 集合，以及副作用事实。词元必须区分“静态字面值”“惰性数据”“待执行字符串”“含未知展开”；不能靠去引号后的文本猜角色。新类型可保持包内 AST，对 node 只导出只读视图，避免 mvdan 类型泄漏到协议。

`policy.Request.Workdir` 仍是信任根。另加解析上下文的 `CWD`，初始为 Workdir；rawInput 中的 cwd/workdir 若与 Run 不同，必须验证并合并，不能当说明字段丢掉。各子命令先算类别和事实，复合命令取最严重类别，同时保留事实并集；一个最大 category 不是完整风险证明。

建议的结构化事实 `PolicyFacts`：`version/policy_version`、`parse_status`、`role/effective_mode`、`command_digest`、`worktree_id/cwd`、`effects`（读/编辑/删除/服务/不可逆的并集）、`read_only_proven`、`writes_outside`、`sensitive_read`、`opaque_execution`、`install_source`（default/custom/unknown）、hard/soft 的稳定代码、脚本路径及完整摘要、`evidence_complete`。这些只由 node 计算，agent 的 rawInput 不能覆盖。协议里的事实与 agent 自述分开，展示文本经脱敏，摘要不含明文秘密。

`Assessment` 同时带基础 effects；`Judge` 在非 restricted 模式、routine/HardRead 之前执行 server effect guard：含编辑/删除/服务/不可逆效果即 AskUser，即使总 category 是 OutsideWorktree 也如此。Floor 仍优先 Deny；restricted 模式不得因此从 Deny 变 AskUser。`onPermission` 与 `workbench.permit` 两个 `run.judge` 调用点共用（后者也覆盖 setup 和 Git 操作）；脚本效果不明的 server 执行按保守用户路径，不能用未知 ExecOther 隐藏已知的删除/服务操作。

### 2.2 证据与事实的传递

P2 需要贯通下面这条链，不能只改 `detail`：

| 层 | 要修改的文件/函数 | 数据职责 |
|---|---|---|
| node | `apps/core/internal/node/run.go:768` `onPermission`、`:900` `ask`；新增 `node/approval_evidence.go` | 执行分类、采集有界证据、构造 facts/evidence；在发件箱持久化之前脱敏 |
| 协议 | `apps/core/internal/proto/proto.go:360` `ApprovalRequest`；`proto/workbench.go:115` 操作常量、`:223` `LookArgs` | 添加可选的版本化 facts/evidence；inspect 绑定 Run/Approval，read_file 使用专用受限操作，不开放任意 admin API |
| hub 入站 | `apps/core/internal/nodehub/conn.go:442` 审批帧处理 | 校验当前连接属于 Run.NodeID，限制结构大小；保留 node 来源标记，不从 detail 反解析事实 |
| 事件/投影 | `apps/core/internal/event/run.go:127` `ApprovalRequestedPayload`；`store/runs.go:209` 投影、`:564` `Approval`、`:584` 查询扫描；`store/migrate.go` | 追加 migration 存 facts/evidence JSON 和指纹；不得改旧 migration；通过 eventlog 写入，支持重放 |
| 审批消费 | `apps/core/internal/task/approve.go:172` `approverInput`、`:38` `Approve` | 说明、证据、规则事实分别限额；证据不得被 rawInput/detail 的 4000 rune 截断吞掉；旧版本缺字段按保守路径 |

实施时同步更新 `docs/api/core-hub-node.md`，若 HTTP 审批响应增加字段再更新 `docs/api/core-hub-http.md`；修改前先读对应 skeleton README。未来变更 Go 依赖/栈时同步 `apps/core/AGENTS.md`，本方案任务不修改它。

## 3. P1：认识引号、heredoc 和实际执行边界

追溯：报告 §0 P1、§3.1、§3.2、§3.6、§4.1。修改：`policy.go` 的 `parseInput`、`classifyCommand`、`classifyOne`、`classifyBase`、`redirectCategory`、`shellC/substitutions/unwrap` 的新解析路径；`assess.go` 的 `Assess`、`pushedProtected`、`routine` 的调用适配；新增 `policy/shell.go`、`policy/shell_test.go`。

**新依赖（已获准）**：仅引入 `mvdan.cc/sh/v3/syntax`，更新 `apps/core/go.mod`、`go.sum` 和 app 栈说明。实施 agent 选择与当前 Go 1.26 兼容的固定 v3 版本并记录版本，不能浮动 latest；不使用 shell 执行器包做分类，不执行展开、不读取用户 shell profile。没有其他必需的第三方依赖。

判定逻辑：

1. `Classify` 仍先用原文执行 `floorHit`，`extraHard` 仍对原始全文与显式 paths 生效，不能把用户本机自定义规则偷偷迁移成“忽略数据”。然后用与实际 shell 匹配的语法解析整个命令。Unix sh/bash 可用 mvdan；Windows cmd、PowerShell、fish、不支持的 zsh 语法或 shell 来源未知时使用旧路径，不假装 Bash AST 与实际执行等价。`parseInput` 对 argv 形式保留 argv 边界和真正解释器身份，不能把数组直接拼成 shell 字符串。
2. 引号内的 `| ; &&` 和换行属于同一参数；真正的 pipe/list/and/or 节点才分段。argv 解码后传给基于 argv 的分类器。命令名是动态展开或未知结构时至少 ExecOther，禁止 readonly；不把未知结构当空命令返回 Read。
3. 输出重定向 `>`、`>>`、`>|`、`<>`、`&>` 逐节点算写路径；输入重定向算读路径；`2>&1` 等 fd 复制不算写文件；只有精确的 `/dev/null/stdout/stderr` 特例。heredoc `<<`、`<<-` 是输入，不是输出；`cat > file <<'EOF'` 的正文是文件数据，类别由目标文件和所有执行展开决定。
4. `echo/printf` 的静态数据参数、`git commit -m/--message` 的消息值不跑内建硬信号；其余参数（如 `--no-verify`、重定向目标）、赋值（如关闭 TLS）及参数内部实际执行的替换仍参与。只豁免明确的数据槽位，不能因为“在引号里”就排除 URL、路径或执行参数。printf 数据若被管道送入 shell/解释器，不再是惰性数据。
5. `sh/bash -c` 的静态程序、`eval`、source/dot、shell stdin/heredoc、执行用进程替换递归分析；`eval` 本身保留 Hard。命令替换、反引号、未引用 heredoc 中的执行展开独立合并风险，单引号/已引用 heredoc 的字面文本不展开。shell heredoc 不可一律降为 ExecOther：其中真实 delete/service/irreversible 必须传到外层，server 仍交用户。重定向/pipe 把生成内容交解释器且不能静态恢复时标记 opaque，不能归 Read/ExecSafe/routine。
6. Python/Node 等非 shell stdin 程序不伪装成 shell 命令。类别至少 ExecOther，正文作为“待执行程序”进入 P2 证据。**不简单删除正文全部安全信号**：执行 API、外发、凭据读写等疑似实际代码继续保守提请用户；仅在能证明是写入固定 worktree 文件的静态文本载荷时排除载荷中的安全词。为 #4/#5 建一个范围很窄的文件写入识别器（标准库读写固定字面路径、replace 旧/新静态文本，包括报告的 `rep(path, old, new)` 形式；禁止动态 import、外部执行、网络、动态目标及非固定调用），返回确定的数据范围与写路径；任何超出识别语法的代码不做数据豁免，沿用全文硬信号并交用户。不要实现通用 Python 安全分析器，也不能让模型声称“这是文档”来清除 Hard。server 对副作用不明的 opaque 程序增加 Hard，禁止用 ExecOther 转交 AI 绕过服务/删除边界。
7. `Assess` 内建规则以执行节点、argv 角色、路径用途工作；远程登录只认实际 ssh 命令，安装来源只认安装节点，受保护 push 依据 git argv（含全局选项）解析。`routine` 不因新解析器自动扩大：先保留原来单条、无重定向/替换的条件，以免误把复杂命令当 routine。
8. 解析报错、AST 遍历/递归预算超限、未支持的结构返回明确状态，调用旧 Classify/Assess；保留旧结果的风险和 Hard，不用半个 AST 作准。若旧逻辑返回 Read/ExecSafe 但新解析已发现未知执行结构，则抬到至少 ExecOther；readonly/verify 对解析失败不新放行。初始上限：命令 256 KiB、AST 4096 节点、递归 16 层；超限不丢正文再解析。

安全性：消除的只是被证明属于数据的文本命中，实际执行节点仍参与判定；floor 和 extraHard 原文匹配不变；未知程序仍受审批且 server 保守。这里不能宣称静态分类等于隔离，现有决策 0010 的局限保留。

验证：保留 `policy/assess_test.go:178` `TestClassifyBypasses` 全部断言，再补 #2/#4/#5/#7/#14/#15/#16 的无害夹具；对每个数据豁免补相应执行上下文的保守反例。覆盖单双引号、转义竖线、多行消息、heredoc 引用/未引用、嵌套 shell、fd、输入与输出重定向、不闭合语法、预算超限、Windows 回退。检查 `Classify → Assess → Judge` 全链；新增 fuzz 只验证不崩溃/预算终止/失败不新放权，不执行生成命令。#4/#5 的节选不能直接当完整历史命令，验收记录注明受控复现的范围。

## 4. P2：证据包、受控只读取证与拒绝理由

追溯：报告 §3.4–3.5、§4.2，并包含 P3。主要修改 `node/run.go` 的 `onPermission/ask/noteRefusal/recordPolicy`，新增 `node/approval_evidence.go`；协议/投影按 §2.2；`task/approve.go` 的 `Approve/approverInput`，新增 `task/approver_loop.go`；`prompts/roles.go:22` `Approver`；`node/look.go`、`node/project_files.go` 的只读保护复用。

### 4.1 先落 node 证据包

1. 每个 AskAI 请求由 node 收集脚本的规范路径、树内/树外、完整大小、完整原始内容 SHA-256、读取时间、完整性标志和脱敏正文。自动包每文件最多 16 KiB、最多 4 文件、总正文 32 KiB；heredoc/`-c` 程序也按这个上限附上。不能截断后再算“全文哈希”，超过 1 MiB 不自动读全文/算身份，标为不完整且 P9 禁用。前后 Stat/文件标识或打开同一文件句柄检查变化，读中变更则整份证据失效。
2. Git 信息用固定 argv 的只读命令按已验证路径获取，禁用 external diff/可执行 textconv，不把路径拼成 shell。附对应文件 status、diff stat；在 Run 准备好 workdir 后（`run.go:333` `markStarted`）记录有关文件的起始摘要/状态快照，区分“启动时已修改”“本 Run 后变化”“归属未知”。重启恢复丢基线则标 unknown，不能把 untracked 或 mtime 新等同“agent 刚写”。没有必要追求凭 status 证明作者身份。
3. 树外显式路径只做有界 lstat/stat：存在、类型、大小，属主可获得则附上，不因属主缺失失败；不追踪未知 symlink 到敏感路径。只对语法确定的路径采样，最多 16 项；不把每个参数、URL 或正文词都当路径探测。
4. 证据的路径、哈希、来源标记由 node 填；模型不接受 agent 自述为来源证明。所有可见文本（rawInput、标题、脚本、错误、assessment hits、note）在 node 的 `ob.reliable` 前先 `redact.Mask`，Hub 入站再防御性脱敏。不能依赖 hub 发日志时补救，因为 outbox 已在本机持久化。
5. `approverInput` 单独渲染证据，不套 `truncate(a.Detail,4000)`。给任务/指令、风险事实、脚本正文各独立预算；完整性与失败原因总是保留，预算不够时删低优先级说明，不能把 truncated 改写成 full。

收集到可解析脚本时，按已证明可达的执行节点补充 effects/Hard，并在 node 发审批前再走一次 `judge`；不能把脚本来源为本任务当作安全豁免。能证明 `env -u` 下密码缺失且导出前必定退出的路径可据此评估，不能证明则不清除凭据导出疑点。server 的未知脚本保持用户路径；所有入口均不得把含删除/服务的脚本仅按“脚本文件在 worktree”交 AI。该步骤不执行脚本验证其安全。

### 4.2 再落审批专用工具循环

新增 `askApproval`，只供 `Approve` 使用，不改验收/其他角色 `askJSON` 的行为。参考 `supervisor/supervisor.go:284–358` 的 provider 消息/ToolResult 追加方式，不能把主管的全部 tool.Registry 暴露给审批员，也不要复用没有工具循环的 inspector。固定工具只有：

| 工具 | 具体落点与判定 |
|---|---|
| `inspect(command)` | Hub 从审批记录加载 Run.NodeID/Workdir，调用 `Nodes.Look`（`nodehub/workbench.go:76`），不让模型选择 machine、dir、RunID；在 `LookArgs` 增可选 RunID/ApprovalID。node 验证该 Run 活跃、该审批仍在 `run.approvals` 等待、来自当前 node 记录后，用本地不可伪造的 context 标记“审批取证”，运行相同的 readonly Judge 和加强后的 secretDump。`admin.do` 的 remote_admin 关闭仍生效，不新增绕行入口。 |
| `read_file(path, offset, max_bytes)` | 新增审批限定的 node.request 操作，Hub 同样绑定 Run/Approval；node 复用 `project_files.go` 的 `inGitDir/isGitName/underSame/within` 和普通文件约束，不能直接放宽 `projectPath`，它目前要求 workspace 内的 Git 根且不支持任意 `/tmp`。允许 worktree、被当前命令点名的临时脚本及精确授权的配置；每次返回脱敏文本片段、总大小、原始摘要/是否完整。不提供列表/任意临时目录扫描、写文件或 project edit 入口。 |

工具路径权限由 node 验证。审批配置读取仅放开 `~/.zshrc`、`~/.ssh/config` 等明确的配置名单和窄读方式，不放开整个家目录、整个 `.ssh`，具体过滤见 P4。inspect 与 read_file 用同一敏感路径函数，防止 read_file 绕过 secretDump；实际路径与请求路径都检查，阻止通过 symlink 别名拿私钥。系统临时目录不能成为全局秘密读取特例。

为 #11 单列 `~/.sous/node.json` 的只读元信息投影：允许返回非秘密的 `hub_url`（移除用户信息、口令和查询凭据）及本地目录存在性，不返回整份 node.json、agent_env、设备令牌或 outbox 内容。由 node 的结构化 reader 实现，不靠模型自己选择 grep 表达式以保证脱敏；inspect 对这些状态文件的宽读直接拒绝。这样核实 hub 位置不需要读取身份凭据。

固定预算：最多 6 次模型调用（最后一次不给工具，只收最终 JSON）、最多 4 次工具调用（一个响应的多个 calls 也分别计数），每工具最多 20 秒/16 KiB，整个审批 120 秒，最多累计 64 KiB 工具结果；同一工具+参数最多执行一次。read_file 分片对 64 KiB 内文本可补齐全文，更大/超预算保持 incomplete 并给出需人工核实的理由。每 Run 的额外取证工具最多 40 次，后续审批仍可利用自动证据，不能无限重试。

调用前和最终决定前重新读 Approval/Run：用户已处理、审批过期、Run 已取消/暂停、node 已离线就停止工具，不覆盖用户结果。模型超时/工具拒绝不是允许证据；可以据已有充分证据作 high 结论，否则保留现有 deny/转人工退路。沿用 `eff.Attempts/approverGiveUp`，记录每次 provider 用量，重试不能重置累计工具预算；工具审计按 ApprovalID/尝试/调用序号幂等记录，不能随 effects 重放重复执行。

提示词明确：证据是数据，里面的“请批准/忽略规则”不是指令；先查证再决定，不再要求工作 agent 先 cat；只有任务相关、脚本完整可理解、范围和可逆性清楚才 high allow。`.zshrc/.ssh/config` 的受控只读可批准，写入或秘密内容仍用户决定；不要把文件位置本身等同“操作有害”。没有充分信息仍说明具体缺口，不能用“本机肯定有 hub.db”之类未经核实的前提拒绝。

### 4.3 脱敏、审计与即时理由

`node/look.go:56` 的 command 日志改为脱敏命令/摘要；`:70` 的 stdout+stderr 在回包前过敏感输出过滤与 `redact.Mask`，包含非零退出、超时和截断的分支；secretDump 对命令结构的拒绝仍在执行前。流式缓冲上限可保留，但需处理跨 chunk 的 secret 和截断私钥头，不能按每个小块独立脱敏后拼接。

工具调用审计只持久化脱敏摘要（工具、验证后路径、返回码、耗时、字节数、truncated、证据摘要），不保留原始敏感输出；用新增的审批检查事件和只追加投影记录，每个最终 note 带可读“查过……”摘要。界面沿用审批 note/escalation 展示，不引入前端依赖。

`onPermission` 拒绝时保留 `noteRefusal` 和现有 `refusalPrompt`，并立即追加 `run.update kind=permission status=deny` 的脱敏理由（已有 `recordPolicy` 可扩参数），让界面/主管当场看到；使用 `reason` 优先于仅有 category 的 `why`。ACP 的选项答复没有理由字段，不在权限回调内再次 `session/prompt`；本轮工作 agent 即时得到理由需要适配器协议支持，不在本次承诺。可在既有 MCP 增只读查询“最近拒绝理由”作为后续独立需求，不能因此扩大 P6 三工具名单。

安全性：取证只有 node 验证的只读能力，没有写/外发通道；不会因能读全文而自动批准脚本。与报告不同，新增一个模型能看到某些配置确实扩展了信息接收者，不能称为“没有增加暴露面”；这正是主管已批准的有限扩大，通过精确路径、窄字段、脱敏与审计控制，不能把 `redact` 当绝对秘密隔离。

验证：使用 canned provider 返回“先 read_file 再 high 结论”，断言 #8/#9 不再因缺脚本重复拒绝；脚本会导出私钥/实际修改服务时仍拒绝或用户审批。验证 detail 接近上限仍保留证据、超大/变化/二进制文件标志、git 初始脏状态、断线/取消/重放/并发人工决定、预算和未知工具。用假 token、私钥块、URL 密码、配置 ProxyCommand 内嵌凭据及跨截断边界数据，断言 outbox、事件、模型消息、daemon 日志、note 均无明文值。#11 只验证 agent 可核对 node.json 的非秘密字段/存在性，不读取真实凭据、不假定必然批准网络查询。

## 5. P3：从复合命令提取正确的脚本预览

追溯：报告 §3.4、§4.3，样本 #8/#9。修改 `apps/core/internal/node/outcome.go:139–227` 的 `interpreters`、`scriptPreview`；脚本候选提取放到 P1 的共享视图；`node/outcome_test.go:82` `TestScriptPreview`。

判定逻辑：遍历执行节点，取其已解析 cwd，处理赋值前缀、单独 export、`cd`、`timeout`、`env -u/--unset` 等已识别 wrapper，然后按解释器参数语法取脚本文件。`bash -c` 取内联程序，`python -m` 是模块、`python -` 是 stdin、`node -e` 是代码，都不能把后面的普通词当路径；wrapper 不支持的参数停止推断。支持多个脚本，最多预览 4 个，顺序稳定，未收集项给出原因。

P3 的小步版本保留每文件 4096 bytes 上限及现有 `mask`，明确标记 full/truncated 和总大小，不能把“头部”写成“全文”。相对路径用 P5 的 cwd，读取边界用固定 worktree 根；cd 出树后不从该目录自动预览。保留系统临时目录这个既有范围，但只读命令明确点名的普通文本脚本，不扫描整个临时目录。realpath 必须在 worktree 或系统临时根内；`.git`、敏感凭据路径、符号链接逃逸、FIFO/设备、二进制拒绝，返回结构化原因；禁止顺着脚本中的 include/import 自动递归读任意文件。

P2 完成后 `scriptPreview` 成为证据渲染器，P3 不再维护一套独立 shell 拆分器。读取失败不影响原分类决策，只告知没有拿到证据。

安全性：这一步只增加被保护、脱敏的信息，不改变 Allow/AskAI/AskUser 判定。预览可读不代表脚本可执行；哈希、来源、全文是否完整由 P2 补足。

验证：#8 的 `cd … && timeout 180 bash …`、#9 的 `T=$PWD/…; env -u … sh …` 必须定位正确文件；补 export、空格文件名、多个脚本、动态 cwd、不存在文件、树外链接、临时脚本、非普通文件、4 KB 边界和 token 打码。`e2e.sh` 和 `export-signing-cert.sh` 是原任务临时/当时的文件，当前仓库没有它们；用临时夹具验证机制，不声称已审计历史脚本全文。

## 6. P4：把敏感位置只读与秘密/写入分开

追溯：报告 §3.2、§4.4，样本 #1/#2/#3/#6/#16。修改 `apps/core/internal/policy/assess.go:13` `Assessment`、`:28` `Note`、`:68` `hardSignals`、`:127` `Assess`、`:308` `Judge`；`node/look.go` 的 `secretDump/look`；新增 `node/sensitive_read.go`，`redact/redact.go` 及测试；`prompts/roles.go` 的相关禁止项与只读例外。

新增 `HardRead`（稳定信号码+用途/路径）、`ReadClass`（public-observation/config/credential-content/unknown）。保留 `Hard` 为不可交 AI 的真正风险，不把“只读”作为全局豁免。对整条命令使用 P1 的事实：仅全部节点只读、没有重定向写、没有不明执行/展开、没有 Hard 时，才可能归 HardRead；任何一个节点写入/外发，整条仍 Hard/原类别。

| 内容/形态 | 新评估 | auto | default | readonly |
|---|---|---|---|---|
| `/etc/crontab`、`/etc/cron.d` 的查看、`crontab -l`，以及 P7 明确的服务状态/列表 | public-observation；不因位置或参数字样触发持久化/ssh Hard | Read → Allow | Read → Allow | Read → Allow，仍经输出脱敏 |
| `ls ~/Library/Keychains` 只列名字/元信息 | HardRead；目录名不是钥匙串内容 | AskAI | AskUser | 普通 machine_look 拒绝；审批 inspect 如任务相关可列名，禁止 cat 内容 |
| `~/.zshrc` 等 shell 配置、精确 `~/.ssh/config` 的窄读 | HardRead(config)，不得把 `.ssh/config` 扩成整个 `.ssh` | AskAI，提示范围/输出要求 | AskUser | 普通 machine_look 保持拒绝；仅审批绑定的 inspect/read_file 可在本地验证的窄读规则下 Allow |
| 私钥、`/etc/shadow`、真实 `.env`、云凭据、钥匙串内容、`security find-*/dump-*/export` 等 | Hard；秘密读取或外发不降级 | AskUser | AskUser | Deny，审批 inspect 同样 Deny |
| `>> ~/.zshrc`、写 authorized_keys、安装定时任务、服务 enable 等 | Hard；保持原类别 | AskUser | AskUser | Deny |
| 不能确定读写、动态路径、不支持语法 | 原有保守信号 | 原路径 | 原路径 | Deny/原有更严格路径 |

`Judge` 的顺序为：先 `DecideFor` 与 floor/受限表硬限制；Hard 按现有逻辑处理；再 HardRead；最后 routine。HardRead 在 auto 将 Read 的 Allow 改为 AskAI，在 default 改为 AskUser；绝不能把表中 AskUser/Deny 降成 AskAI。审批 inspect 的特殊 readonly 权限由 node 核验 Run/Approval 后构造本地 `JudgeContext`（给原 `Judge` 增有默认零权限的包装函数即可），只允许表内指定的窄读；Hub 请求不能直接声明“允许敏感读”。verify 不新增配置特权。dev open 仍沿用已有显式开放语义，server open 降级不变。

`secretDump` 从单个原文 regex 扩成结构规则+原文防御过滤：继续拒绝打印环境变量、秘密文件；按 realpath 检查配置例外、拒绝私钥别名；对 shell 配置只返回需要的 PATH/工具配置行，对 ssh config 只允许 Host/HostName/User/Port 等本次任务需要的字段，隐藏所有命令/口令/令牌值。`ProxyCommand`、`LocalCommand`、IdentityFile 目标内容等不跟读；不执行 source、ssh -G 等会加载配置并可能产生其他行为的方式。包含 token 的行整行遮蔽；无法可靠切分的敏感配置读取拒绝，建议更窄的结构化 read_file。不得先把原内容写进日志再滤。

JSON 等结构数据先用 `redact.SecretName` 按字段递归遮蔽，再渲染文本并调用 Mask；只依赖文本 NAME=value 正则不够，带引号的 JSON key/value 不一定匹配。未知配置格式与敏感字段不回传原文；读文件分页不能先截掉字段名/私钥头再失去判断上下文，过滤必须基于同一份完整且有上限的原始快照，再分页已过滤内容。

工作 agent 原始工具执行输出并非由 node 执行，不能假装 `onPermission` 能改写其 stdout。AI 批准 #3/#6 时应要求命令本身是任务相关的窄读；宽泛 cat 如无法控制秘密输出仍应拒绝并给具体窄读建议。`toolOutput`（`outcome.go:128`）已有 mask，但 `run.onUpdate` 的标题、消息/思考流、`noteRefusal` 和 `permissionDetail` 仍需补发出前过滤；对流式文字以完整行/有界缓冲处理，防止 token 被跨 chunk 原样记入日志。已知模式脱敏是兜底，不能保证识别所有任意编码的秘密，因此批准的是受限配置读取，不是任意凭据读取。

修正命令信号：ssh 只识别实际命令节点；journalctl 的 `-u ssh`、systemctl 的服务名不是远程登录。`installSource` 仅检查真实安装子命令（包括 `python -m pip`）的来源参数；pip 的 `-i/--index-url` 仍 Hard，grep 的 `-i` 不参与。通过 AST 取得参数不等于信任默认源：`PIP_INDEX_URL`、npm registry 配置或来源不明要记 custom/unknown；P8 不把 unknown 视为 default。

安全性：只下调“读位置的元信息/配置字段”这类误报，秘密内容、外发、写入和提权保持用户边界；配置访问使用主管已经批准的范围。`HardRead` 出现不表示随便 Read 都能放，最严重类别、Hard 和角色优先。

验证：#1/#3/#6 路由 AskAI，#2 安装仍 AskAI 但不再误报自定义源；#16 的 ssh 假阳性消失。用 dev/server × auto/default/open/readonly/verify 做表测；测试混合读写、密钥真实读取、同名目录/链接、token 行、配置中的命令、未知安装来源、实际 pip 自定义源与 shell 展开。审批 inspect 能窄读配置而普通 machine_look 仍拒绝，二者输出/错误和日志没有假 token。

## 7. P5：cwd 与工作树根分离（在 P1 上先完成）

追溯：报告 §3.1、§4.5，样本 #13。修改 `policy/shell.go` 的语句遍历状态、`policy.go:330` `outside`、`:432` `redirectCategory`、`:498` cd 判定、`:506` 路径处理；node 的预览复用同一 cwd 视图。

判定逻辑：所有路径先按当前 cwd 解析成绝对路径，再相对固定 worktree 根判断。`cd apps/core && cmd > ../../work/tmp/x` 对执行到 cmd 的分支使用子目录 cwd，目标仍在根内。`cd` 不只支持树内：树外目录记录为树外，后续相对路径也不再以根冒充；未知路径至少 OutsideWorktree/ExecOther，禁止产生“在树内写入”的证明。

顺序与控制流不能混淆：`cd X && B` 的 B 只在成功分支执行；`cd X; B` 考虑 cd 失败仍在原目录；`||` 保留可达 cwd 集合；只有所有可能目标均安全才可证明安全。`(cd X; …)`、pipe、命令替换的 cwd 不泄漏到父 shell；`{ …; }` 同 shell 更新。`cd -`、无参数 cd、`CDPATH`、变量路径和不确定分支不猜 cwd。对 P5 可确定的字面 cd 语义做保守建模，不能确定就保留 OutsideWorktree，不要求先“按根算”再误放行。

涉及实际文件读取（P3/P2/P9）时在 node 解析 symlink、验证物理路径位于允许根内；分类阶段没有文件系统事实时不得声称 realpath 已安全。工作树中指向树外的 symlink 不允许凭词法路径拿到低风险证明。

安全性：内外边界始终是 Run 的 worktree 根，不随 cd 缩小或放大。server 的写操作仍按 `DecideFor` 交用户。

验证：#13 重放为 ExecOther（goreleaser 本身未知，故仍 AskAI），不再是 outside；子目录里重定向到真正树外仍 OutsideWorktree；补 cd 失败分支、`&&/||/;`、子 shell、两次 cd、绝对路径、带空格目录、动态 cd、symlink 及 server 组合。不能把 #13 预期错误写成直接 Allow。

## 8. P6：内建 Sous MCP 的精确免审批路径

追溯：报告 §3.1、§4.6，样本 #12。修改 `apps/core/internal/node/run.go:768` `onPermission`，`node/sous_tools.go:38` `sousServers` 记录本会话的内建服务身份，必要时在 `acp/client.go:63` 规范化工具身份；不对全部 MCP 名称做 policy 全局放行。

精确名单：`mcp__sous__report_progress`、`mcp__sous__report_result`、`mcp__sous__ask_supervisor`。先执行原 floor 与正常请求解析，再核对：当前 work Run 确实注册了内建 sous 服务、未 `[off]`、非自定义覆写命令、工具身份精确匹配；输入只有对应 schema 的文本字段，没有 command/script、文件 Locations 或写/删除 kind。参数文本不能被当成 shell 执行，也不能从中生成新工具调用。满足后直接给 allow_once，调用 `recordPolicy` 记“内建 Sous 通知工具”；不能让 ACP 缓存 allow_always 扩大后续工具范围。

当前 `ToolCallInfo` 只有 title/kind/rawInput/locations，没有可信 MCP server 字段，所以不能在 `Policy.Classify` 里仅凭标题给所有请求特殊权限。实现 node 会话绑定的 `isBuiltinSousRequest`，对支持的 ACP 适配器严格识别完整工具名及已注册的 schema；不支持/模糊标题/冲突字段保持普通审批。自定义 `sous_mcp` 命令不因名字相同自动拥有这个例外。实际执行仍由 `sousCall` 验 Run token 和仅三种 IPC method；不放行任意 MCP、任意 `run.*`、inspect 或外发。

安全性：少的是一层“向自己报告文字”的审批，不解除执行命令、读文件或 IPC 身份校验；ask_supervisor 仍受 `MaxAsks` 和超时限制。报告里的 `mcp__sous__*` 只作为概述，不能实现成前缀通配。

验证：两个 #12 工具和 ask_supervisor 的合法消息没有 Hub 审批，但有 policy 审计；相似名称、未知工具、off、自定义服务、execute/edit kind、带命令/路径、错误 token、其他 Run 仍按原策略或 IPC 拒绝。保持 `sousmcp` 的 schema 校验（空 text 等仍正常报错）和现有 MCP 端到端测试。

## 9. P7：服务器查看命令按参数形态加入

追溯：报告 §3.6、§4.7，样本 #15/#16。修改 `apps/core/internal/policy/policy.go:638` 服务 switch、`:746` `readOnly`，新增 `policy/readonly.go` 中 `readOnlyInvocation(argv)`，在基于 argv 的分类器调用。不要把所有候选名字直接塞进 map。

新判定表如下；未知选项、多个相互矛盾的动作、额外输出文件、写入/执行形态不能命中只读分支，转原类别（服务仍 Service，删除仍 Delete、sudo 仍 Irreversible），而不是强行把所有变体改成 ExecOther。

| 命令组 | 允许 Read 的具体形态 | 必须排除/保留原审批的形态 |
|---|---|---|
| hostname、who、w、last、lastb、lsblk、lscpu | 仅默认查看和显式枚举的显示选项；hostname 无位置参数或只用显示选项 | hostname 设置名字、`-F/--file`，工具未知选项 |
| getent | 本地非秘密数据库（hosts/passwd/group 等）和固定查询键 | shadow/gshadow、未知数据库；配置秘密不因 getent 间接读取豁免 |
| journalctl | 仅查询参数：`-u/--unit`、`--since/--until`、`-n/--lines`、`-b/--boot`、`-p/--priority`、`--no-pager`、`-o` 限安全文本输出；有限跟随可由 timeout 截止 | `--vacuum-*`、`--rotate`、`--flush`、`--sync`、`--relinquish-var`、`--setup-keys`、`--update-catalog` 等一切非查询动作；不是仅列三项黑名单 |
| ip | addr/address/a、route/r、link/l 的默认 show 或显式 show，可配已枚举过滤词/显示选项 | add/del/replace/flush/set、netns exec、未知对象/批处理 `-batch` 等 |
| iptables/ip6tables/nft | `-S/--list-rules`、`-L/--list`，可选精确 table/chain/显示项；nft 仅 `list ruleset`（显示选项枚举） | append/insert/delete/flush、restore、文件/批量输入、同一 argv 混入写动作；需要 root 的查看返回权限错误即可，不自动 sudo |
| ufw、nginx、crontab | `ufw status [verbose/numbered]`；`nginx -t` 仅配置检查且不准自定义执行指令；`crontab -l`（可选受控只读用户参数） | ufw enable/disable/reset、nginx -s/-g/-T 或不带 -t 启动、crontab -e/-r/安装文件；-T 会泄漏完整配置，不纳入 |
| systemctl | 原只读子命令 + `list-timers/list-unit-files/is-enabled/get-default`，全局选项先正规解析，强制禁 pager | enable/start/stop/restart/daemon-reload/set-* 等仍 Service；不得靠“含 list-timers 这个词”通过 |
| docker/podman | `stats --no-stream`、`top`，仅单纯查看容器 | stats 未受限长流、exec/run 等原 Service，特权信号不变 |
| git | `rev-list`、`shortlog`、`reflog show` 和无动作的 reflog 查看；禁 pager/external diff | reflog delete/expire、写输出/命令执行参数、未知全局配置等；原 GitRemote/Hard 不变 |
| 文本处理 | zcat/zgrep、column/tr/nl/od/strings 的只读 argv；xxd 仅读输入或 stdout | xxd -r、第二个输出文件、带写功能的选项，命令内重定向始终单独算写 |
| sous | 精确 `status`、`version`、`--help` 的显示形式 | 其他 sous 命令、配置/服务/更新/执行类动作 |
| dig/nslookup/host | **本轮不加入 readonly**；维持 ExecOther → auto AskAI，可用 P2 核实任务相关性 | 查询可能携带数据，不能称作绝对无外发。显式数据外发仍 Hard，不能以 DNS 工具名豁免 |

带子命令/参数的工具统一使用“完整 argv 被消费且无未知项”的白名单解析；不能检测到一个好选项就忽略其余参数。显示时禁 pager，执行环境不额外 source 配置。`nginx -t` 可能受本机模块/日志实现影响，readonly 是产品策略的配置检查语义，不宣称系统调用绝对零写入；不允许重定向到持久文件或变成启动服务。

安全性：改变的是明确的观察形态；server 写/删除/服务/不可逆依旧用户审批，machine_look 保持 readonly/secretDump/超时/输出上限。原 readOnly 中其他工具的全面安全审计不在本次范围，新增项不能照搬已有名字级信任。

验证：每个表项至少一条正例、一条写/执行反例、一条混合参数反例；结合 P1 检查带引号 grep 管道。`TestLookingIsReading`、`TestMachineLook` 增 server 测试；Linux 真实查询仅在隔离测试机可用时验证，不操作线上服务/防火墙。测试程序缺失/非 root 的正常错误不会导致“改 sudo 再试”。#16 必须同时去掉 ssh 假信号与 journalctl 未识别，单做 P7 不算通过。

## 10. P8：严格校验 confidence，再有条件接受 medium

追溯：报告 §3.3、§4.8。修改 `apps/core/internal/task/approve.go:97–122` `Approve`，新增 `eligibleMedium(facts, category)`；`prompts/roles.go:57–59` 的 schema/说明；`task/approve_test.go:108–149`；增量开关在新 `task.ApprovalPolicy`（`task/engine.go`）中定义，`config/config.go` 和 `hub/hub.go` 装配本机 Hub 配置，默认关闭 medium，不能由工作 agent 参数启用。

第一步立即做枚举校验，不等观测：decision 只接受 allow/deny/ask_user，confidence 只接受 high/medium/low；缺失、空值、未知值、畸形 JSON 均不能决定允许或拒绝，交用户并给出结构化原因 `invalid_verdict`。high allow、high/medium deny、low 转用户；不再以“不是 low/medium”当 high。对模型多个 JSON/混合工具与结论采用严格的一份最终结论，避免在工具调用前后取任意文本片段。

第二步的 medium allow 只在以下条件**同时成立**时生效：Review=ai、未命中 aiNever/任何 Hard 或 HardRead、非 server 受保护动作、无 soft、解析成功、facts 版本受支持、证据完整且仍有效、任务相关性模型明确允许、类别在限定表内。事实缺失/旧 node/模型声称安全都不能代替此条件。

| 类别 | medium 的额外机器事实 | 不可 medium 的情况 |
|---|---|---|
| exec-other | 已点名且完整取证的本地脚本，有保守识别出的有界、可恢复作用域；没有未知执行、网络、系统写入/持久化 | 仅凭 category、未读全文脚本、动态解释器程序、来源未知、外发 |
| outside-worktree | `read_only_proven=true`，只读取公开系统/任务相关元信息，无敏感配置/秘密、无全盘扫描、路径/输出范围确定 | 任意树外写入、家目录配置读取、动态路径、未知命令 |
| install | 来源 default 被 node 证实，显式依赖与版本/锁定信息可核对，没有未知 registry/env 覆盖或任意安装脚本 | custom/unknown、默认源只是模型猜测、包来源/安装脚本无法证实 |

不纳入 delete；“在 worktree 内可删除”不能证明数据可恢复，报告 §4.8 把 delete 纳入与其“低风险”前提不符。GitRemote、Service 一律 high；Irreversible/Floor 永不交 AI。HardRead 配置仍 high，主管允许只读不是允许低把握读取。

安全性：类别、执行权限和硬信号不变，仅在代码已经限定范围的子集里改变模型把握阈值。medium 不是安全证明，必须有 facts 的独立门槛；现有证据不足时继续 high，不为体现收益强行扩大白名单。

验证：完整枚举 decision × confidence，尤其空串/未知值不得批准；每个 eligible 条件分别置假，断言转用户；证明 delete/service/git-remote、HardRead、旧协议均不走 medium。用 fake provider 验证 #8/#9 即使 medium 仍不会仅凭脚本头部通过。先 shadow 记录“本来会因 medium 转用户、但候选规则认为可允许”的计数，不实际放行；满足 §13 的观测门槛再启用。

## 11. P9：只复用任务内明确的 allow_always

追溯：报告 §3.3、§4.9。修改 `apps/core/internal/task/approve.go` 的 `kindOfAnswer`、`userPrecedent`、`followUser`、`Approve`；按 §2.2 给协议/事件/store 增执行指纹；`node/run.go` 在等到批准后做证据一致性检查。新增 `task/approval_identity.go`，指纹生成逻辑在 node（可复用 policy 的规范视图）。

新逻辑分两种键：执行允许使用版本化、完整的 `operation_key`；拒绝保留现有保守精确匹配作为兼容路径，不能因旧记录无新键丢失已有拒绝记录。不要从漂亮 JSON/中文 detail 反向猜命令身份。

`operation_key = SHA256(规范序列化的身份数据)`，身份至少包括 TaskID、NodeID、角色/有效模式、worktree 物理身份和分支、cwd、ToolKind/规范工具身份、category、argv 或含类型的 shell AST、所有赋值/重定向/路径、影响执行的 rawInput 参数、策略版本、Hard/HardRead/Soft 事实，以及**所有被执行脚本/内联程序的完整原始内容摘要**。执行环境中的来源/权限相关覆盖也影响身份；环境不可核实、动态 source/import/间接依赖无法绑定时禁用允许复用。摘要不使用脱敏正文，否则不同秘密值可能被 mask 成同一个键；敏感原值不出 node，只发送摘要，哈希不是允许条件本身。

规范化只能去掉确定无语义的说明字段（如 description 的展示文本）、JSON key 顺序、shell AST 中不在字面值里的格式空白。保留字符串内部空白、引号/展开语义、cwd/env、timeout、wrapper 参数、输出位置；未知参数保留，未知格式禁用复用。脚本只读了头部、超过哈希预算、符号链接目标改变或执行依赖无法确定，都没有可复用 key。

`Approve` 与 `followUser` 共用以下顺序：

1. 重新确认 pending；TaskID 为空不复用；查当前节点事实/角色/模式。floor、irreversible、真实 Hard、server 编辑/删除/服务/不可逆及受限模式禁止的动作，允许不自动复用，保持当前用户边界。
2. 在同 TaskID 内查**最近的显式用户决定**，按真实 options 中选中 id 对应 kind 判断，不从 option_id 是否含 always/updates 猜。用户较新的 allow_once 代表本次授权，不继续向前找旧 allow_always 来覆盖它；较新的拒绝同样优先。
3. 最新匹配是 allow_always，且完整新 key 完全相同，才允许复用；allow_once 从两条路径都不得复用允许。决定当前请求时优先选当前 allow_once，避免把审批 agent 的一次代答扩为适配器端的泛授权。只有 allow_always 选项可用而适配器授权范围不可证明时保持人工。
4. 用户曾拒绝的同一操作仍复用拒绝（维持现有产品语义），不调用模型。最新匹配的拒绝不要被更旧的允许覆盖。
5. 所有复用允许仍经过 P10 的当前预算和最终原子决定；在 note/audit 记录来源 ApprovalID、kind、key 版本。复用不是“审批员自己看过脚本”，不能伪造取证记录。
6. node 收到允许后，对已绑定脚本/路径/关键事实重新校验摘要；变化则不执行这次允许，结束旧请求并提示重新申请，不把新的内容套进旧批准。即使这样也不是原子文件执行隔离，仍有 ACP 交还执行后的 TOCTOU 局限；动态或无法绑定的执行不允许复用，不能把本优化宣传成无竞态沙箱。

安全性：授权限于用户明确的持续授权、同任务、同机器、同实际执行内容；更严格角色/策略优先。报告建议剔除 timeout 的做法不采用；脚本哈希、执行指纹和风险 facts 缺一不可。旧历史记录没有完整 key 时只保留拒绝兼容，不新增允许。

验证：`TestApproverFollowsTheUsersEarlierAnswer` 现有 allow_once 允许复用的断言要改，不能把原有错误当必须保持的兼容。分别测试 ai/user 两路径、四种 option kind、不同 option_id 命名、最新答复优先、description 变化可复用、引号内空白/timeout/env/cwd/node/role/task/脚本变化不可复用；脚本只变在 4 KB 后也失效；直接/间接 symlink 变化、迁移重放、旧 node、server 边界、预算耗尽和并发人工决定。当前 16 个样本没有可靠的 allow_always 记录，不虚构本项节省次数。

## 12. P10：先修预算完整性，再按数据提高上限

追溯：报告 §3.3、§4.10。修改 `apps/core/internal/task/approve.go:21` 常量与 `:59–88` 的预算计算/precedent 顺序；新增 store 的精确审批统计查询，`store/runs.go:637` `TaskApprovals` 不再承担预算计数；`nodehub/nodehub.go:443` `DecideAs` 的自动批准最终校验，`eventlog.Log.Write`/store.Writer 的事务内条件检查。

核对补充：`TaskApprovals` 当前是 `ORDER BY created_at LIMIT 500`，会拿任务最早 500 条，不是完整历史。提高上限/跨 Run 重试后既会漏预算，也会漏最近用户决定。实施时分开提供：按 Run 聚合完整允许数、按任务+执行键查最近用户决定、按操作统计拒绝数、模型显示最近 10 条；不能简单把 LIMIT 改成更大的魔数。

第一步保持 150 的现有阈值，先做以下正确性修复：所有 ActorApprover 的允许（模型 high、未来 medium、用户 allow_always 复用）都在最终决定处扣同一 Run 预算；拒绝、用户手工允许、node 已明确 Allow 不计入这个 AI 上限。复用放在预算之前的现有返回必须移走。允许计数按决定唯一事件/ApprovalID 统计，不因 effects 重试重复计数；拒绝上限 3 保持，历史查询不被 500 截断。

用统一的 `DecideAs` 自动批准分支在 `eventlog.Log.Write` 的同一写事务内完成“仍 pending、预算未满、追加决定事件”的检查，通过 store.Writer 查询/投影，不能事务外先 COUNT 再写，不能直接 UPDATE 领域表绕过事件。预算规则由 Hub 的代码配置注入，不从模型或 node detail 传入。一次人工决定与自动决定竞争只允许一个生效；预算到上限返回固定类型错误，由 Approve 幂等 Escalate，绝不把许可先发给 node 再补日志。取证调用预算另计，不能混成批准数。

第二步选择报告给出的**统一提高到 300**方案，暂不做多类别额度，避免不同 category 之间转移来获得更高总预算。新增 `ApprovalPolicy.MaxAIAllowsPerRun`，默认 150，仅允许本机 Hub 配置 150–300；超出范围配置校验报错，0 不代表无限。按 §13 观测证明确实被 150 卡住后才启用 300；对每 Run 仍有总上限，巡检、stuck、重复拒绝上限不变。配置改变也不能绕过 node 的角色/floor/Hard。

安全性：预算是持续自动执行的第二道限制，不可用“巡检会发现循环”替代。先堵完整计数与重复/并发，再有界扩大，不引入按类别加总可能达到数百上千的隐含额度。

验证：149/150/151、299/300/301 边界；同任务两个 Run、单任务超过 500 条审批、重复 effect、allow_always 复用、并发两个最后名额、人工与 AI 竞争、重启后预算一致、关闭/打开增量配置。预算耗尽不调用模型/工具且有 escalation；新 Run 重新计数；默认配置仍是 150。

## 13. 实施顺序、依赖与启用门槛

下面是实施阶段，不表示本方案任务已经修改代码。可并行指独立实现流；同一个 `policy.go/approve.go` 不宜让多个 agent 同时改动，合入时由该模块负责人统一整合。

| 阶段 | 交付 | 前置条件 / 可并行项 | 为什么在这一步 |
|---|---|---|---|
| S0，先守住基线 | 固定 §14 的 16 个编号；补 floor/server/aiNever/外发/秘密读取的负例；P8 的严格枚举校验；P10 的完整统计与原子 150 预算基础 | 不依赖新解析器；policy、task/store 测试准备可并行 | 新解析器或授权逻辑不能在没有边界测试的情况下替换；confidence/计数问题应先止损 |
| S1，基础解析 | P1 共享 shell 视图、失败回退、argv 分类与 Assess 迁移；紧接完成 P5 cwd | 依赖 S0；P6 的 node 例外可单独开发，完成后独立启用 | P3/P4/P7 不能各自发明拆词；P5 必须在证据路径读取之前明确根与 cwd |
| S2a，先给信息 | P3 脚本提取与 P2 第一阶段证据包、版本化 facts/evidence、事件/迁移/输入预算；P2 的立即拒绝理由与基础脱敏 | 依赖 S1；node 采集与 hub 协议/投影可按同一契约并行，先合协议/迁移再合消费者 | 先解决确定的“看不到脚本”，不必等工具循环才有效；同时为后续规则提供可信事实 |
| S2b，恢复查看 | P7 参数白名单；P4 的 ssh/installSource/公共 cron 观察修正 | 依赖 S1；可与 S2a 并行，policy 文件统一集成 | #15/#16 两个 machine_look 根因需要解析+参数规则+硬信号一起修；不能用放宽 readonly 解决 |
| S3，受控取证 | P4 HardRead/精确配置权限+secretDump/输出过滤；P2 第二阶段只读工具循环、审计和预算 | 依赖 S2a/S2b；HardRead 路由可先存在但配置取证能力必须在脱敏/路径负例通过后启用 | 主管已批准配置读取，安全过滤是同一交付的必要前置，不能先放权限后补 token 过滤 |
| S4，稳定授权 | P9 新 key、allow_always 语义、两审批路径统一，node 允许前重验 | 依赖 S2a 的 facts/完整哈希，S0 的原子预算；需要 S3 的敏感路径事实统一 | 先确保“同一操作”可证明，再复用持续授权；与 P10 联调，避免绕过额度 |
| S5，观测后增量 | P8 medium 候选 shadow/开关；P10 150→300 开关 | 依赖 S3/S4 的稳定证据、审计、准确统计；二者单独启用，不能同时改后无法归因 | 报告没有 medium/预算实际收益数据；没有观测时保持 high 和 150，功能可完成但开关不启用 |

P6 小步可提前合入；P3 不按报告建议在 P1 前另写临时 splitter，避免重复实现并再次漏引号/heredoc。实施里可以先交付 P3 的证据改善，但必须使用已经完成的 S1 接口。P4 的两部分分阶段是为了明确哪些是误报修复，哪些需要敏感输出过滤先就位，不是延后主管已经拍板的决定。

### 13.1 观测与回退

为每条判断留下稳定 reason_code、policy/evidence 版本、原/新路由、是否 parser fallback、是否缺证据、是否用户授权复用、审批耗时/模型用量/取证次数、budget 当前值。不把完整命令或配置输出当统计标签，避免秘密泄漏和高基数。

P1 上线前做离线新旧差分：每个新结果更宽的案例必须能对应一个已证明的数据片段/只读参数规则，出现在 §14 或负例覆盖中；未知语法不得更宽。先对解析器、HardRead、取证循环设置独立本地能力开关，旧节点缺能力时走旧路径，不把 node 的“未识别字段”当已启用。回退到旧解析器可能恢复误报，但 floor、角色、aiNever 不可随开关撤下。

上线观测至少连续 7 天或累计 200 条实际审批（以较晚达到为准）。在可用的 Hub 上由有权限的操作者跑报告附录 A 的只读聚合；按 options.kind 关联统计，不能用 id 含 updates/always 近似作为 P9 的依据。本方案不要求实施 agent 为拿数据绕过机器权限。

P8：至少出现 10 条真实 `medium` 转人工候选，逐条复盘有足够机器事实且人工决定/证据复核无反例，再在单一测试项目启用；没有可核验样本就保持关闭。任何越界或敏感信息泄漏立即关闭 P8/配置取证例外并保留审计，修复后重跑负例。普通任务指令中“已经授权”不能改变开关。

P10：至少 3 个不同 Run 确实撞到 150，且复盘不是重复/空转/同操作拒绝重试，才在单一测试项目或测试 Hub 启用 300；原子计数与 500 条以上测试必须先通过。若收益样本不足则不提高，不需要再问是否允许 mvdan 或配置只读。

回退任何宽化能力时立即禁用旧 allow_always key（policy_version 变化），不删除历史审批/拒绝。schema 已追加不回滚删除，旧字段保留；如果降级到不能读取新 schema 的旧二进制，先按项目备份/兼容发布流程处理，不能直接让旧版写新库。

## 14. 16 个真实样本的验收对比基线

来源为报告 §2 的全部 16 行，编号逐条对应。改前分类/结论沿用报告现场与其 policy 实测结果；本次只核对这些机制对应的源码，没有重新访问原 outbox 或远端。报告称“原文”但表中实际有省略号与替换路径，不能把节选当完整原始命令。

实施测试在 `apps/core/internal/policy/approval_samples_test.go` 建 `sample-01` 至 `sample-16` 的表驱动受控夹具，用临时工作树替换现场路径，明确完整 argv/正文及预期事实；#8/#9 在 `node/approval_evidence_test.go` 加脚本夹具，#12 放 node permission/内建 MCP 测试，#15/#16 放 machine.look 端到端。若将来拿到授权脱敏的完整历史命令，再增加 replay 测试，不改编号、不假称已经重放历史全文。

默认对比前提：dev + auto；#15/#16 是 server + readonly。Allow 表示 node 策略直接允许；AskAI/AskUser 是路由而非必然批准。`AI 可 high 批准` 需要 fake provider 的可控结论和规定证据，真实模型可能基于任务范围给出不同决定；这不能成为修改 floor/角色边界的理由。

| # | 报告操作与改前判定 | 改后类别 / 评估 → 预期判定 | 对应改动与验收断言 |
|---|---|---|---|
| 1 | 查看 AGENTS、列 `~/Library/Keychains` 名字并 grep；ExecOther + 凭据 Hard → AskUser | 受控全只读夹具为 Read + HardRead(metadata) → AskAI；可按签名任务的必要性 high 批准 | P1/P4。仅列名不误判读密钥；混入钥匙串内容读取必须仍 AskUser/readonly Deny |
| 2 | `goreleaser --version … grep -i version; … go install …@latest`；Install + 非默认源 Hard → AskUser | Install，无来自 grep 的来源 Hard → AskAI；默认源是否可确认单独记事实，不自动变成 Allow/medium | P1/P4。保留真正 pip `-i` 和自定义 registry 的 Hard；grep 的 -i 绝不能影响别的安装节点 |
| 3 | `cat ~/.zshrc … grep -i -n "go\|path" … head`；ExecOther + 持久化 Hard → AskUser | 引号正确的 Read + HardRead(config) → AskAI；可在窄读/过滤充分时 high 批准；否则理由要求明确 PATH 字段，审批 inspect 自行窄读 | P1/P2/P4。配置位置不直接转用户；token 行不出现在证据/日志，不能要求所有宽 cat 都无条件通过 |
| 4 | Python heredoc 写签名文档，正文提及 security/gh secret；Irreversible + 凭据/GitHub Hard → AskUser | 对固定 worktree 文档写入的受支持静态载荷夹具为 ExecOther，无由载荷文字产生的 Hard → AskAI，附程序证据后可 high 批准 | P1/P2。不再因文档词语成为 irreversible；真正 shell/子进程执行、秘密读取或无法识别为数据的同类程序仍保守处理；floor 原文不豁免 |
| 5 | Python heredoc 改 `.goreleaser.yaml` 的注释，含 Keychains 路径；OutsideWorktree + 凭据 Hard → AskUser | 支持的固定文件 replace 夹具为 ExecOther，无数据词语 Hard → AskAI，不因 heredoc 行误判树外 | P1/P2/P5。必须解析到写入文件实际在树内；动态写路径或任意代码超出受支持范围则回旧评估，不宣称所有 Python 都已安全 |
| 6 | `grep -i '^\s*host\b' ~/.ssh/config; …`；ExecOther + 凭据 Hard → AskUser，现场用户拒绝 | Read + HardRead(config) → AskAI；新任务且有相关性时可 high 批准精确字段读取；同任务有效的既有用户拒绝仍跟从拒绝 | P1/P2/P4/P9。主管已准配置只读，未准覆盖现场用户的具体拒绝；私钥读取和 ProxyCommand 秘密不豁免 |
| 7 | 构建 probe，cat heredoc 写 Go 源码；printf 的测试数据含 `.ssh/.bashrc`；OutsideWorktree + 两个 Hard → AskUser | worktree 内的受控测试夹具为 ExecOther（probe 本身仍是执行），数据参数无凭据/持久化 Hard → AskAI，提供源程序/构建证据后可 high 批准 | P1/P2/P5。不是 Read/直接 Allow；同样文本若送入 shell 执行应继续合并实际副作用，不因 printf 名字免评估 |
| 8 | `cd … && timeout 180 bash …/e2e.sh …`；ExecOther → AI 拒绝“看不到全文” | 至少 ExecOther（发现真实副作用则合并更高类别）→ AskAI，已定位脚本、可读取完整相关内容；脚本确认只作用于该任务临时资源时可 high 批准 | P3/P2。没有脚本或只拿到头部不能谎称全文；server 版本若涉及删除/服务仍 AskUser。验收不真实创建钥匙串 |
| 9 | 变量前缀 + `env -u … sh …/export-signing-cert.sh`；ExecOther → AI 两次拒绝缺全文 | 至少 ExecOther → AskAI，证据明确 env 删除了密码且脚本在任何导出前退出时可 high 批准；不能证明早退则保持拒绝/用户审批 | P3/P2。分别覆盖“安全早退”“实际会导出”的夹具；不以拿到脚本就批准真实私钥导出，不声称当前已有历史脚本 |
| 10 | `cd ~/.sous; … head outbox; find / -name hub.db`；OutsideWorktree → AI 拒绝范围过大 | OutsideWorktree → AskAI，**仍应拒绝全盘 find**，可建议检查有限数据目录/配置中的 hub 地址 | P2。证据改善不改变任务范围；用纯判定或假 runner 验证，不实际遍历用户全盘；属于必须保留的合理拒绝 |
| 11 | `dig +short agent.wxian.cc …`；ExecOther → AI 以“hub.db 在本地”的错误前提拒绝 | ExecOther → AskAI；审批员能检查 node 配置中的非秘密 hub 地址/本地存在性，DNS 对任务确有必要时可 high 批准，否则按真实范围拒绝 | P2；P7 明确未把 DNS 新加到 readonly。断言模型输入具备核实手段、不再被提示词诱导假定本地库；不承诺必然批准网络 |
| 12 | `mcp__sous__report_progress` / `report_result`；ExecOther（未知操作）→ AskAI | 经过 node 会话/输入验证的内建三工具例外 → 直接 Allow（allow_once），保留 policy 审计 | P6。这行同时测两个报告工具及额外已列明 ask_supervisor；其他 mcp__sous__ 名称仍正常审批 |
| 13 | export、`cd apps/core && … goreleaser … > ../../work/tmp/…log`；OutsideWorktree → AskAI | ExecOther（goreleaser）且输出在 worktree，无 outside 误报 → AskAI，按真实发布/签名副作用与证据判断 | P1/P5。只消除路径误判，不承诺无需审批；真正路径越界和非 snapshot 发布不得借此下调 |
| 14 | `git add -A && git commit -q -m "…多行说明…"`；OutsideWorktree → AskAI | EditWorktree，无数据文本的假信号 → Allow；server 同样编辑仍 AskUser | P1。消息是一个 argv；真实 --no-verify 仍 Hard；若消息碰现有 floor，保留原文 floor Deny 而非为了消息豁免底线 |
| 15 | server machine_look 中 `grep -E '^(Port\|PermitRootLogin\|PasswordAuthentication)' /etc/ssh/sshd_config`；ExecOther → readonly Deny | Read，无 Hard → readonly Allow | P1。保留整个正则参数，server 角色不阻止只读；实际管道/替换/重定向副作用仍独立判定 |
| 16 | `journalctl -u ssh --since today … grep -c Failed`；ExecOther + 远程登录 Hard → Deny | Read，ssh 只是参数、无 Hard → readonly Allow；命令本身的权限/无日志等错误正常返回 | P1/P4/P7。同时验证改变参数为 journalctl 维护写操作不能通过，shell ssh 命令仍 Hard |

表中 #4/#5 的数据识别器如果无法覆盖取回的真实完整程序，实施 agent 必须明确记为“原文未完整重放/不支持”，保留保守判定并补充支持该固定写入形态的实现与测试，不能直接忽略整份 Python 正文来凑通过率。#8/#9 的安全结论和 #11 的网络必要性都不是本报告节选能确定的事实，验收分别验证证据可得和危险对照，不以线上模型一次输出作唯一标准。

## 15. 验证清单与交接

### 15.1 实施 agent 必跑（产品代码修改后的要求）

| 层级 | 命令与位置 | 通过标准 |
|---|---|---|
| 局部 | 在 `apps/core/`：`go test ./internal/policy ./internal/redact ./internal/task ./internal/store ./internal/nodehub ./internal/sousmcp ./internal/node` | 新增规则与 16 样本夹具、旧旁路/角色/readonly/预算/授权测试全通过；新增失败不靠删除旧测试解决 |
| 竞争 | 在 `apps/core/`：`go test -race ./internal/task ./internal/nodehub ./internal/node` | 审批重复 effect、人工竞争、预算最后名额、取消和 inspect 没有数据竞争或重复决策 |
| 全量 | 在 `apps/core/`：`go test ./...`、`go vet ./...` | 包括 app AGENTS 指定 nodehub/node/acp/proto/task/policy/workspace 影响后的全部假 agent 端到端：recovery、task、flex、workbench、review_fix、agents 等测试 |
| 跨平台 | 在有对应环境的 CI/开发机从 `apps/core/` 运行 `go test ./internal/policy ./internal/node ./internal/task` | Windows 无 Bash 语法假解析；macOS 路径/Keychains 只用临时夹具；Linux server 查询白名单与权限错误路径验证。仅交叉编译不声称运行过另一系统测试 |
| 仓库 | 根目录 `wok check`、`git diff --check`，产品改动后再 `wok ls core` | 布局、空白、任务表一致；新增依赖有固定版本、同步 app 栈说明，临时目录清理，构建产物和秘密不入库 |

命令全部从所属 app 运行；本次不涉及前端依赖或 CLI，未来若确需前端工作使用 bun。测试以假模型、假 agent、隔离临时 Git 仓库/脚本为主，不对真实服务器做删除、服务或凭据动作，不在测试里读取真实 `~/.ssh`、钥匙串或用户 token。

### 15.2 每阶段验收交付物

实施 agent 每个阶段提供：改动文件/函数、本文 P 编号、数据结构和契约变化、正反测试结果、16 样本对应行的当前状态、保守回退路径、尚未真实验证的系统/模型环境。不能把“有取证工具”写成“必然安全批准”，也不能把“未触达 150”写成提高额度有收益。

依赖汇总：P1 新增且已批准的 `mvdan.cc/sh/v3`；P2/P3/P4/P5/P6/P7/P8/P9/P10 均使用现有 Go 标准库/现有 provider、eventlog、SQLite 和 node.request，无其他新外部依赖。P2/P9/P10 涉及协议/迁移是内部契约变更，不是第三方依赖；发布必须保留旧 node 保守兼容。

### 15.3 本次方案核对的结论

源码已确认成立：P1 的拆词/全文扫描、P2 无工具与证据截断、P3 首词预览、P4 读写混同、P5 无 cwd、P6 内建 MCP 落未知类别、P7 查看命令缺失、P8 medium 转人工、P9 人工允许不复用/AI 路径混用 once 与 always、P10 150 上限。floor、server 决策表、aiNever、machine.look 的 readonly/20 秒/16 KiB 和现有审计路径均核对过，应保留。

需修正报告的部分：不是 16 行全是误报；不能用 status 证明本任务作者、不能把 4 KB 叫全文；追加 detail 会再次被截断；inspector 不是现成工具循环；secretDump 不是配置 token 输出过滤；原 confidence 实现还接受未知值；原历史查询有 500 条截断且授权复用在预算前返回；server 的类别保护还需防止树外类别遮蔽基础效果；deliver 已移除，当前是 ACP/permit 两个直接入口；DNS/hostname/xxd 等不能按名字统一当只读；allow_once/timeout 不能按报告简化处理；P4 readonly 的家目录一刀切与主管新决定应按精确配置例外修正。报告的收益数字没有全局数据支持，#8/#9 历史脚本和部署服务器版本不在仓库，均未重新验证。

本次仅文档校验：完整读报告、核对上述源码，执行 `git diff 13ff1aa -- apps/core` 无产品差异；`wok check` 返回 `Layout OK.`，`git diff --check` 通过；单独检查未跟踪的新 plan 的章节、16 行样本、表格列数和空白也通过。产品测试、历史命令全文重放和真实模型/服务器验证由实施阶段完成，不在本次冒充已完成。
