# 审批摩擦实施与自测

日期：2026-11-09。按 [plan.md](plan.md) 的 S0 → S1 → S2a/S2b → S3 → S4 → S5 分批实现并运行局部测试；[report.md](report.md) 为历史样本来源。改动留在任务 worktree，未提交、推送、合并或创建 PR；没有操作真实服务器、keychain 或系统配置。

## 实施范围

| 阶段 / P | 实际改动 | 边界与保守取值 |
|---|---|---|
| S0 / P8、P10 | 严格 decision/confidence 枚举与最终单对象 JSON；全历史 SQL 统计、事务内 pending/Run/额度检查、并发单次决策 | high 默认不变；floor/irreversible 不交 AI；allow_once 不作持续授权 |
| S1 / P1、P5 | 固定 mvdan.cc/sh/v3 v3.12.0；共享 argv/重定向/执行替换/脚本/cwd 视图；窄 Python 固定文件文本载荷识别；根与 cwd 分离，控制流保留可能目录 | floor/extra_hard 先匹配原文（含原 argv）；256 KiB/4096 AST/16 层；不支持 shell/结构、解析失败、预算耗尽回退旧分类与全文 Assess，至少 ExecOther；旧递归也有上限 |
| S2a / P2、P3 | 识别 cd/export/赋值/env/timeout 后的多个脚本与内联程序；结构化 facts/evidence 贯通协议、事件、新 migration、投影、模型输入；完整原始摘要与启动快照；审批前合并实际脚本效果 | 每脚本16 KiB、4文件、正文32 KiB、哈希读取1 MiB；树外路径仅 stat；不是全文就标 incomplete；status 不冒充作者证明；读不到不当作安全证据 |
| S2b/S3 / P4、P7 | Hard 与 HardRead 分区；ssh 只认命令，安装来源只看安装节点；新增观察命令完整参数白名单；配置字段投影、敏感路径与链接检查、输出/流/JSON 脱敏 | 真实私钥/.env/shadow/外发/持久化写仍 Hard；普通 machine.look 不获配置例外；server 已知编辑/删除/服务/不可逆、未知脚本仍人工；DNS 仍 ExecOther |
| 独立小步 / P6 | 会话绑定的默认内建 MCP 三工具精确 schema 校验后直接 allow_once，并记录 policy 审计 | 目前仅支持可确认身份的 Claude 会话；off/自定义服务、模糊名称、命令/路径/写入字段、其他工具不享例外；原 IPC token/schema 校验保留 |
| S3 / P2 | 审批专用 inspect/read_file 小循环，Hub 从存储绑定机器/Run/审批，node 再核验活跃和待审批；工具/模型累计预算、持久化执行去重、审计、最终 note 的“查过” | remote_admin 和本地回退开关有效；每审批4工具/64 KiB、每Run40工具、每工具20秒/16 KiB，累计最多6模型调用，最后一次无工具；单轮循环120秒 |
| S4 / P9 | 最新显式用户决定优先；只有 allow_always + 完整 operation_key 可复用，当前只答 allow_once；允许前重验脚本及身份 | 绑定任务/机器/agent/角色/模式/物理树/cwd/实际 Git 分支/原参数/实际启动命令和环境/策略/原始全文脚本摘要；未知/间接依赖/不完整/敏感路径不生成允许复用键；拒绝兼容旧精确匹配 |
| S5 / P8、P10 | medium 独立事实门槛及 shadow 事件；Hub 本地 allow_medium、max_ai_allows_per_run 配置与范围校验 | 未取真实 hub.db 观测：medium 默认关闭，额度默认150，仅可配置150–300；不实际提高到300。node 尚不证明 bounded_reversible，install_source 保持 unknown，故 exec/install 不会因 medium 获准 |

Node 本地回退开关：`policy.legacy_shell`、`policy.hard_read_disabled`、`approval_inspect_disabled`；远程编辑不能改变。API 说明见 `docs/api/core-hub-node.md`、`docs/api/core-hub-http.md`。前端未改动。

## 16 条逐项对比

“改前”沿用报告的现场/旧策略判定；“改后”由固定编号 `TestApprovalSamples/sample-01` 至 `sample-16` 的完整受控夹具验证。报告命令是节选，含省略号与替换路径；本次不是历史全文重放。Allow 是 node 直接放行，AskAI/AskUser 是审批路由，不能写成真实模型必然批准。#12 的策略表故意仍为 ExecOther，最终免审批由 node 会话测试证明。

| # | 改造前 | 改造后（受控复现） | 误报修复与必须保留的拦截证据 |
|---|---|---|---|
| 1 Keychains 列名 | ExecOther + 凭据 Hard → AskUser | Read + HardRead → AskAI | 只列名字不再当导出；绑定 inspect 只返回目录名。内容读、私钥与普通 machine.look 例外仍拒绝；`TestApprovalReadWritePartitions`、`TestApprovalInspectionScopeAndProjection` |
| 2 grep -i + go install | Install + 错误来源 Hard → AskUser | Install → AskAI | grep 的 -i 不再污染安装；真实 pip 自定义源仍 Hard。`TestApprovalDataExemptionsCannotHideExecution` |
| 3 zshrc PATH | ExecOther + 持久化 Hard → AskUser | Read + HardRead → AskAI | 引号里的 pipe 不拆；审批 reader 只输出 PATH 投影、过滤 token/执行表达式；写配置仍人工，普通/链接别名读取不获例外。`TestApprovalInspectionScopeAndProjection` |
| 4 Python 写签名文档 | Irreversible + 凭据/GitHub Hard → AskUser | ExecOther → AskAI，附程序证据 | 仅固定字面路径/静态数据形态消除正文误报；真实 subprocess/私钥读取、floor 字样仍拦。`TestApprovalDataExemptionsCannotHideExecution`、`TestApprovalFloorIsNeverDelegated` |
| 5 Python 改 YAML 注释 | OutsideWorktree + 凭据 Hard → AskUser | ExecOther → AskAI | 窄固定 replace/rep 语法不把数据词当命令；未知代码/动态目标不获豁免，树外写和 server 编辑仍人工。`TestApprovalSamples/sample-05`、分区及 server 负例 |
| 6 SSH Host 字段 | ExecOther + 凭据 Hard → AskUser，现场用户拒绝 | Read + HardRead → AskAI | 仅精确 config 投影；私钥/ProxyCommand 不跟读，最新用户拒绝继续优先。`TestApprovalInspectionScopeAndProjection`、`TestAllowAlwaysRequiresCompleteIdentityAndLatestUserAnswer` |
| 7 probe/heredoc/printf 数据 | OutsideWorktree + 两个假 Hard → AskUser | ExecOther → AskAI | 数据不触发凭据/持久化，probe 仍需评估；printf 管道给 shell 保持 Hard/未知执行。`TestApprovalDataExemptionsCannotHideExecution` |
| 8 cd + timeout + 脚本 | AI 因无全文拒绝 | ExecOther → AskAI，证据可完整取得；安全夹具经 read_file 后 high 允许 | 前缀后定位、完整摘要、改变4 KiB之后内容也使原许可失效；危险脚本效果传到 server 人工边界。`TestApprovalEvidenceSample08And09`、`TestApprovalInspectionRoundTrip`、`TestApprovalScriptEffectsReachServerGuard` |
| 9 env -u 导出脚本（两次拒绝） | AI 缺全文拒绝；导出审慎要求合理 | 完整证据 + 精确首部缺变量退出证明时 AskAI；真实导出仍 AskUser | 仅支持导出前必定 exit 的固定 guard，不执行脚本或 keychain。无 guard、guard 前有未知命令、密码未证明删除/恢复或 shell hooks 时不豁免。`TestSample09RequiresProofBeforePrivateKeyExport`、`TestApprovalInspectionSample09GuardRoundTrip` |
| 10 全盘 find | OutsideWorktree → AI 拒绝范围过大 | OutsideWorktree → AskAI，假模型继续拒绝 | 合理拒绝保留，无全盘扫描。`TestApprovalSample10ScopeDenialPreserved` |
| 11 dig 查询 | ExecOther → AI 基于“库在本地”错误前提拒绝 | ExecOther → AskAI，可读 node 配置的无秘密 hub 地址/本地库存在性投影 | 取证手段和提示词修复前提；不保证网络请求必然批准，不加入 readonly，不读实际配置秘密。`TestApprovalInspectionScopeAndProjection` |
| 12 Sous MCP 两报告工具 | 未知 ExecOther → AskAI | 绑定内建 report_progress/report_result/ask_supervisor → 审计后 allow_once | 精确三名与 schema；未知/相似名称、自定义服务/off、execute/edit/path/command、错误身份不获例外。`TestBuiltinSousPermissionSample12`、`TestBuiltinSousIsAllowedOnceAndAudited`，原 sousmcp 测试 |
| 13 cd core 后输出到 ../../work | OutsideWorktree → AskAI | ExecOther → AskAI，输出在树内 | 只修 cwd 误判；goreleaser 仍需审批。真正越界、失败分支、子shell及未知 cwd 保守。`TestShellFallbackAndCWD` |
| 14 多行 commit message | OutsideWorktree → AskAI | EditWorktree → Allow；server → AskUser | 消息保持单 argv；--no-verify 仍 Hard，消息/正文里的 floor 仍 Deny。`TestApprovalFloorIsNeverDelegated`、`TestApprovalDataExemptionsCannotHideExecution` |
| 15 server grep 正则 | ExecOther → readonly Deny | Read → readonly Allow | 引号里的 pipe 保留；真实替换、写入/远程动作仍拒绝。隔离 Hub/node 端到端：`TestMachineLookServerSamples15And16` |
| 16 journalctl -u ssh | ExecOther + 远程登录假 Hard → Deny | Read → readonly Allow | ssh 参数不是 ssh 命令；rotate/vacuum/sync/未知参数拒绝。`TestReadOnlyCompleteArguments`、`TestMachineLookServerSamples15And16` |

这些测试没有把合理拒绝改为直接允许：#9 的实际导出仍人工，#10 保留拒绝，#11/#13 继续 AskAI；其余宽化均对应数据槽位、参数白名单或有本地权限核验的通知/取证例外。floor、秘密/外发与 server 副作用另有跨角色/模式负例；这证明所覆盖的回归边界，不能证明任意 shell/程序都安全。

## 验证与限制

验证命令在 `apps/core/` 执行（Go 缓存定向到任务临时目录）；包含本地监听的测试在允许隔离监听的执行环境下运行。测试日志为任务临时输出，不入库；使用假模型、假 ACP agent、隔离临时 Git 仓库与配置/脚本，没有真实秘密、真实 keychain 或线上动作。

| 验证 | 结果 |
|---|---|
| `go build ./...`、`go vet ./...` | 通过 |
| `go test ./...` | 通过，含16条编号夹具、Hub/node取证、恢复/任务/工作台/ACP/MCP等端到端 |
| `go test -race ./internal/task ./internal/nodehub ./internal/node` | 最终完整重跑通过：task 82.689s、nodehub cached、node 101.035s；无数据竞争报告 |
| `go test ./internal/policy -run=^$ -fuzz=FuzzShellConservativeFallback -fuzztime=5s -parallel=2` | 通过，16201次输入执行，无命令执行 |
| `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...`；Linux 同命令 | 交叉构建通过，不等于对应系统运行测试 |
| `go mod tidy` | 通过，固定依赖与校验记录同步 |
| 根目录 `wok check`、`wok ls core`、`git diff --check`、`git status --porcelain --ignored` | 布局通过、任务表一致、空白检查通过，无新增根项/系统配置/构建产物 |
| 前端 bun 命令 | 未执行：apps/web 没有改动 |

race 复核修正了一处原端到端测试的时序断言：TaskDone 与 GitState 是两个事件，测试需等第二个投影。另有一次 macOS 假 agent 子进程在启动阶段触发 `ThreadSanitizer: CHECK failed: tsan_rtl.cpp:94`，随后恢复测试超时；没有数据竞争报告，记录环境失败后重跑，不修改产品逻辑来迎合它。另一次重跑出现假 agent initialize EOF，导致验收重试测试超时，也保留失败记录再完整重跑。

未启用 P8/300 额度所需的真实观测：方案要求至少7天且200条审批、至少10条 medium 候选复盘、至少3个不同 Run 真正撞到150；本任务未访问真实 hub.db，不能声称已满足或有量化收益。

已知限制：静态策略不是执行沙箱；沿用原只读集合的全量审计不在本次范围。配置投影扩大了审批模型的信息接收范围，但仅为已授权字段，脱敏不能保证识别任意编码秘密。证据/身份重验缩小变化窗口，ACP 实际执行前仍存在 TOCTOU。复用刻意保守，复杂脚本/间接依赖、旧 node、缺少实际启动环境、未完成证据均不复用；shell 格式变化也可能导致键不命中。未知安装源保留 unknown；Python 仅支持受控固定文本写入，历史完整程序未取得。Windows/Linux 只有交叉构建，不冒充真机运行；真实模型/部署版本未验证。
