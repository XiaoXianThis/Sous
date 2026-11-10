# 主管状态声明核验报告

日期：2026-11-10。分支：`sous/core-id-vs-282s2raqfh`。仅修改 `apps/core`；未提交、未推送、未切分支、未创建 PR。没有运行真实 agent 或对真实服务执行部署、重启、git 写操作。

## A：按 ID 核验（已实现）

- `internal/supervisor/dispatch.go:18` 识别独立的 26 位 ULID；每个 ID 必须出现在本轮成功工具回执正文，或在 tasks 表中存在。失败工具回显的错误参数不能充当存在证据；找不到的 ID 所在句子改成“未能确认（未找到该任务：…）”。查询失败也明确标为未确认。
- `internal/supervisor/dispatch.go:80` 每次重新读取当前任务状态。已派出、已出、已启动、已安排、开工、正在跑、dispatched、started 等肯定声明，必须同时有 Task running、最新执行 Run running、started_at 和未暂停；不满足时在句后附真实状态和原因。任务“已完成”也要求 Task done。缺少 ID 的派发声明最多要求模型纠正一次，此后直接改写；无 ID 的明确任务完成声明也会改写。
- `internal/store/runs.go:653` 查询最新工作／审查／rebase／旧交付 Run，包含已结束的 Run，避免旧活跃 Run 掩盖新一轮排队或失败；时间相同按 rowid 排序。仅凭 queued 不承认启动。
- 同一工具回执中的 Run ID 单独按 Run 核验，不误报为缺失任务；Task Run 的启动声明还需其所属 Task running。其他工具返回的非任务 ULID只作为存在证据，不能凭空证明任务在执行。
- `internal/supervisor/supervisor.go:389` 去除整轮 dispatched 布尔豁免：每一步文字先核验，再发送 delta、落库；修正后的文字也用于同轮后续模型请求。Thinking 仍流式发送，正文要等本次模型响应结束后统一发送，这是阻止假声明提前展示的代价。
- 日志沿用 `supervisor: unverified dispatch claim`，带 thread、reply_to、step、task_id、verdict、status；关联 Run 时增加 run_id。真实状态附注由代码先去除再重算，重复核验不叠加；否定词只约束本次匹配，不能用“尚未完成”掩盖前面的肯定派发声明。

## B：工具回执进入历史（已实现）

- `internal/event/event.go:159` 的 ToolNote 新增可选 `result`；`internal/supervisor/toolnote.go:26` 保存成功结果首行，先脱敏，最多 400 字，错误仍走独立 Error 字段。
- 存储沿用已有 JSON：`internal/store/project.go:52`、`:72` 写 messages.tools；`internal/store/read.go:159` 解码。因此无需修改表或追加 migration，旧事件和旧消息可继续读取。旧记录没有 Result 时，历史明确显示“未保存结果，不能据此确认执行成功”。
- `internal/supervisor/toolnote.go:94`、`internal/supervisor/supervisor.go:640` 把 `【本轮工具】工具名 → 结果／失败原因` 接到 assistant 历史；step、最终回复、包含回执的 silent／错误消息均保留证据，silent 原因和模型错误正文仍不回放。
- `internal/supervisor/context.go:106` 与压缩 transcript 复用同一回执文本，计入预算。`internal/supervisor/compact.go:348` 在模型摘要后由代码保留带原消息／调用编号的回执，不依赖摘要模型自行遵守。
- 回执存档最多 6000 字，保留最近的完整回执；更早部分明确标记“执行结果需用 thread_read 核对原消息，不能凭主管文字确认成功”。排序、去重和重新压缩均稳定，避免重复烘焙和存档无限增长。原始回执仍在消息／事件中，可按原消息编号查阅。
- 提示词只补充事实边界，并明确“不要只靠提示词解决”。没有修改审批策略、ACP 协议、topic 或前端 UI；后端消息 JSON 仅兼容增加 Result 字段。

## C：同类声明逐项排查

“可复现”分为新增单测已构造的状态序列和静态代码确认的可达路径；没有用真实账号验证模型一定会说出某种错话。

| 声明 | 现状代码位置及可复现性 | 本次处理／覆盖 | 未修边界及理由 |
|---|---|---|---|
| 已提交／已推送／已合并 | `internal/workspace/gitops.go:57` 无改动时返回“没有新的改动要提交”；`:211` up-to-date 时返回“不用推送”；`:220` rejected 返回错误。`internal/task/git.go:354` GitReport 区分成功、冲突、取消和失败，但原 `internal/task/tools_flex.go:249` 对已结束的失败 Run 仍返回 nil error。该错误回执可稳定构造。 | 已修 gitToolResult：失败、取消、中断为工具错误，成功和 no-op 保留原文。新增单测覆盖；B 将成功、no-op、失败的真实首行带入历史。 | A 的任务存在／启动／完成检查不证明 git 操作成功。主管仍可能把 no-op 口头说成“本次新增推送”，也可能对真实 task id 编造“已合并”；要彻底硬约束，需要按操作／分支／远端关联结构化 GitResult 和声明，属于独立动作证据机制，本次未扩展。 |
| 已部署／服务已重启／验证通过 | `internal/task/advance.go:112` 仅带 DeployCommit 的 ops 工作进入部署健康检查；`:759` 没配置 health 就直接 finish；`:785` 有检查且成功才正常 finish，失败到 healthFailed；`internal/node/health.go:25` 依据实际退出码。普通 ops 不自动检查服务，只有 install 成功而 agent 自述 completed 的路径仍可结束为 done。`advance.go:592` 自动部署无 health 时已有“未验证”提示；`:189` 仍会记录部署提交。可达性由代码确认，未真实部署复现。 | A 可挡未开始／未完成任务被说成已启动／已完成；B 保留回执；现有配置的 health 检查继续生效。 | Task done 不等于端点健康、服务重启或某个命令验证通过。无 health 的自动部署仍记录 deployed、普通运维缺少动作验收，需决定强制健康检查范围和成功定义；未擅改这些业务语义。 |
| 任务已完成，但 report_result 自述完成、测试未跑／有失败步骤 | `internal/node/sous_tools.go:108` 接收 agent 结果；`internal/node/outcome.go:76` 显式 failed 结果会失败，但非失败 result／completed 也可作为产出信号。`internal/task/advance.go:116` 根据 review 决定验收，review none 会 finish；`:574` 自动汇报复述总结；`internal/task/result.go:42` 只是解析验证文字，不校验测试执行。自述 completed + 未运行测试 + review none 的可达路径仍存在。 | A 已对 task id 检查 done，拦 running／queued／blocked／planned／review 被说成完成；B 保留回执。该状态门槛新增单测覆盖。 | 如果存储已经是 done，A 不会把它重新判失败，也不承诺测试通过。独立的测试／命令证据、失败步骤与验收要求关联，需要另定验收契约；本次不改变任务验收策略，不强制所有任务跑测试。 |
| 只有 run.queued 就声称已启动 | `internal/task/engine.go:891` 请求创建 Run 后即可转 Task running；`internal/task/tools.go:384` 已有“尚未收到客户端启动确认”文本；真正启动由 `internal/node/run.go:359` 上报，`internal/store/runs.go:124` 设置 started_at。Task running + Run queued 可稳定构造。 | 已由 A 硬约束修复，并测试无 Run、queued、failed、paused、真正 started，以及旧 Run 不得证明新 Run。工具回执中的 Run ULID也验证，不重复实现派发状态机。 | 判断是响应发布时数据库中的快照；发布后发生的异步状态变化由正常事件流更新，不能承诺未来持续运行。 |
| blocked／等待依赖被描述成在跑／已派出 | `internal/task/engine.go:702` 处理依赖失败，`:724` 等机器；`internal/task/tools.go:461` 输出状态和原因。原守卫只认工具成功，queued／blocked／planned 都可能放行。新增单测可稳定复现这些状态及“第一个任务真、第二个不存在”。 | 已由 A 按每个 ID 附当前状态和原因，B 跨轮保留回执。不改变依赖或路由策略。 | 同一句描述多个任务时会保守地核验其中所有 ID；无法可靠关联到 ID 的派发／明确任务完成声明按未确认处理。 |

## 待决定的后续范围

1. 是否建立统一的动作证据（git 分支／远端／提交结果、部署端点／进程、验证命令及退出码）再允许相应自然语言成功声明；本次没有用更长提示词代替这个机制。
2. 是否收紧无 health 的自动部署、普通 ops 和 review none 的完成语义；这会改变现有验收流程，不能作为文案修补悄悄加入。
3. 如另行同步文档，建议更新主管上下文中的工具回执、6000 字存档上限、正文延迟发布，以及 Task done 与测试通过的区别；本次未修改 docs/。

## 测试与检查

新增 `internal/supervisor/claims_test.go` 覆盖不存在 ULID、两个任务分别核验、所有要求的状态、真正启动确认、任务完成、同轮与跨轮上下文、失败回显不算回执、脱敏与存储、压缩模型漏回执、重复核验／压缩幂等和存档上限。`internal/task/git_receipt_test.go` 覆盖 Git 失败与 no-op 的回执区别；既有触发器测试改用无状态断言的回复，历史顺序测试现在断言工具回执出现在文字之间。

最终构建和测试均退出 0，完整原始输出附后。缓存来自本任务中此前的实际全量执行，包含 Node 集成／端到端包；最终受代码修改影响的包也已重新测试。使用临时 GOCACHE 是因为默认用户缓存目录受沙箱限制，全量测试在允许本地 HTTP／Unix socket 监听的执行环境运行。

`git diff --check` 和改动 Go 文件的 `gofmt -l` 均退出 0、无输出。`wok check` 有一个任务开始前就存在的根目录布局错误；README.md 是已跟踪的根文件，本次没有新增、修改或删除，也没有修改白名单或 AGENTS.md：

```text
error README.md  not allowed at the root (AGENTS.md section 1)

1 error, 0 warnings
```

没有在根目录生成文件，没有创建／更改骨架目录，没有本任务遗留的 work/tmp 目录。

### go build ./...

执行目录 `apps/core`，命令：

```sh
GOCACHE=/private/tmp/sous-core-id-go-cache go build ./...
```

退出码 0；原始 stdout/stderr 为空。

### go test ./...

执行目录 `apps/core`，命令：

```sh
GOCACHE=/private/tmp/sous-core-id-go-cache go test ./...
```

退出码 0；原始 stdout/stderr：

```text
?   	github.com/XiaoXianThis/Sous/apps/core/cmd/sous	[no test files]
?   	github.com/XiaoXianThis/Sous/apps/core/cmd/sous-hub	[no test files]
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/acp	(cached)
?   	github.com/XiaoXianThis/Sous/apps/core/internal/acp/acptest	[no test files]
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/agents	(cached)
?   	github.com/XiaoXianThis/Sous/apps/core/internal/attach	[no test files]
?   	github.com/XiaoXianThis/Sous/apps/core/internal/auth	[no test files]
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/config	(cached)
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/effects	(cached)
?   	github.com/XiaoXianThis/Sous/apps/core/internal/event	[no test files]
?   	github.com/XiaoXianThis/Sous/apps/core/internal/eventlog	[no test files]
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/httpapi	4.033s
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/hub	0.423s
?   	github.com/XiaoXianThis/Sous/apps/core/internal/ids	[no test files]
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/inspector	(cached)
?   	github.com/XiaoXianThis/Sous/apps/core/internal/ipc	[no test files]
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/mcp	(cached)
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/models	(cached)
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/node	(cached)
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/nodehub	(cached)
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/notify	(cached)
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/policy	(cached)
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/project	(cached)
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/prompts	(cached)
?   	github.com/XiaoXianThis/Sous/apps/core/internal/proto	[no test files]
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/provider	(cached)
?   	github.com/XiaoXianThis/Sous/apps/core/internal/redact	[no test files]
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/router	(cached)
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/sousmcp	(cached)
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/store	(cached)
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/supervisor	(cached)
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/task	(cached)
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/tool	(cached)
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/trigger	(cached)
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/update	(cached)
?   	github.com/XiaoXianThis/Sous/apps/core/internal/version	[no test files]
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/watchdog	(cached)
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/web	(cached)
ok  	github.com/XiaoXianThis/Sous/apps/core/internal/workspace	(cached)
```
