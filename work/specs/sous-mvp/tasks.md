# sous MVP：任务

按里程碑排列。每个里程碑结束时，都有一个能实际跑起来、能验收的成果。

## 当前状态（接手先读这里）

- **进度**：M0、M1、M2 的代码已完成，测试和浏览器验收通过（M2 用假 agent 验收）。下一步是 **M3 核心闭环**，从“task：状态机……”那一项开始。
- **等用户操作的事**：部署到 agent.wxian.cc；真实设备上验收 Passkey 和 PWA；在一台电脑上登录任意一个编程 agent（推荐 `claude`，然后 `/login`），再做一次真实 agent 的端到端验收。
- **本地开发**：`wok dev core` 启动 Hub（需要 `apps/core/.env` 里的 `DEEPSEEK_API_KEY`，没有就把 `roles.supervisor` 改成 `mock/echo`）；`wok build web` 构建前端；客户端用 `go run ./cmd/sous` 或 `wok build core` 的产物。
- **先读**：`work/agent/memory.md`（踩过的坑）、`apps/core/AGENTS.md`、`apps/web/AGENTS.md`、`work/agent/rules/super-agent-reference.md`。

## M0 骨架

- [x] 初始化 wokspace，创建 core、web、desktop 三个 app
- [x] 写决策记录 0001～0008、需求、设计
- [x] 第一轮评审：修正 12 个问题，补充决策记录 0009～0011，写入 Super-agent 参考文档
- [x] 用户确认 spec 和 `docs/api` 契约初稿
- [x] `apps/core`：初始化 Go module，搭好包结构，填写任务表（install/dev/test/build）
- [x] `apps/web`：初始化 Svelte + PWA，填写任务表
- [x] 加上 `.github/workflows`：push 和 PR 时跑测试（同步更新 `.gitignore` 白名单）
- [x] event、store：建事件表并做迁移，实现单一写入者的追加写入、订阅、视图投影，带单元测试
- [x] effects：副作用队列（事务内入队、提交后执行、退避重试、连续失败 3 次才报告）
- [x] 配置加载（hub.yaml + 环境变量；node.yaml 随 M2）

## M1 能聊天

- [x] provider：统一消息格式，以及 openai-chat、openai-responses、anthropic 三个适配器（流式输出 + 工具调用）
- [x] 实测 DeepSeek 的工具调用，确定各角色的默认模型（可用模型为 `deepseek-v4-pro`、`deepseek-flash`；主管用 v4-pro，思考模式下的工具循环正常）
- [x] auth：Passkey 注册和登录，会话管理，`sous-hub admin passkey-reset`（完整的 Passkey 流程待在真实设备上验收）
- [x] channel/web：REST 接口 + `/ws` 事件流（支持 since 断点续传；流式片段只推送、不入库）
- [x] supervisor：线程串行队列和批次合并（`reply_to`）、主线、上下文组装、提示词分层、工具循环（内置 current_time）
- [x] supervisor：发言策略（`stay_silent`）、memory 和 notify 工具；每轮自动带上“关于你”的记忆和相关记忆
- [x] memory：SQLite 存储，中文按两字片段、英文按整词做子串匹配并打分（个人规模足够；trigram FTS 对两字中文词召回太差，已放弃）；界面可查看、搜索、编辑、删除
- [x] web：登录、线程列表、流式对话
- [x] `work/infra/`：Hub 的部署文件（systemd、每日备份 timer、Caddy）和部署指南 `docs/guides/deploy-hub.md`
- [ ] 部署到 agent.wxian.cc（用户操作）
- [ ] 验收：手机和电脑的浏览器都能登录、对话，两边的历史保持一致

## M2 能干活

- [x] 实测五个 agent 在 macOS 上的 ACP 启动方式、权限模式、登录检测，结果写进 design.md 第 7 节
- [ ] 在 Windows 和 Linux 上实测；确认能否把长命令转到后台
- [x] proto：Hub ↔ Node 协议，与 `docs/api/core-hub-node.md` 保持一致
- [x] auth：配对码（6 位、10 分钟、一次性）和设备令牌（Hub 只存哈希）
- [ ] 设备令牌存进系统钥匙串（目前在 `~/.sous/node.json`，权限 0600）
- [x] node：连接、心跳、上报机器和 agent 清单（含登录状态）、断线时缓存事件、重连后补发（内存里；daemon 进程重启会丢，见 M4）
- [x] workspace：`~/.sous/worktrees/` 下 worktree 的创建和命名、一般任务的 `~/.sous/scratch/` 目录
- [ ] worktree 的自动清理（任务关闭后；放到 M3 交付流程里）
- [x] 上传 diff（`run.diff`），Hub 端保存到 `runs/<run_id>.diff`
- [ ] 临时性故障的自动重试（Node 侧 agent 启动失败重试；模型调用的重试 M1 已有）——推迟到 M4
- [x] worker/acp：会话管理、认证重试、权限模式切换、session/update 和 permission 的映射、取消、追加指令
- [ ] ~~worker/jsonstream、worker/pty、worker/shell 三个退路实现~~ 五个 agent 都支持 ACP，暂不需要
- [x] `sous` 命令行：pair / daemon / status / agents / service / unpair（`service install` 尚未在真机执行过）
- [ ] Windows 上以当前用户身份常驻（Windows 服务以 SYSTEM 运行，读不到 agent 的登录；目前指南里用计划任务代替）
- [x] web：机器面板（配对、改名、删除、agent 状态）、对话里的 Run 卡片（计划、工具调用、审批按钮、取消、diff 和日志查看）
- [x] supervisor：`run_agent` 工具；每轮提示里带上机器和 agent 列表
- [x] 验收（假 agent）：在网页上让 DeepSeek 主管派活 → 审批 → agent 在 worktree 里改代码 → 查看 diff；取消；用户的工作区不受影响
- [ ] 验收（真实 agent）：等用户登录一个 agent 后再做

## M3 核心闭环

- [ ] task：状态机、子任务、依赖、代码任务和一般任务；主管的任务工具；线程后续消息转发给正在执行的 Run
- [ ] router：手动指定、按优先级、排队后等机器上线再派发
- [ ] web：任务看板和列表、项目与路由优先级的配置界面
- [ ] policy：操作分类、三档模式、底线规则（Node 本地执行），子 agent 以“每个操作都询问”的方式启动
- [ ] 审批流：AI 审批 agent（approver）、首页的审批队列（approval 事件和 Run 卡片里的审批按钮 M2 已有）
- [ ] notify/webpush：VAPID 自动生成、订阅、按类型开关、免打扰、点击跳转
- [ ] reviewer 基于完整 diff 验收和打回
- [ ] 交付：push 分支并创建 PR（`run.deliver`），有冲突时派 Run 去 rebase
- [ ] web：首页只显示需要用户处理的事
- [ ] 验收：requirements.md 里的验收场景 1、3、6

## M4 自主与长期运行

- [ ] trigger：cron、超时、跟进
- [ ] 巡检 agent：规则预筛 + 模型判断 + 执行动作
- [ ] usage 记录和用量页面
- [ ] 进程看门狗：服务管理器的重启配置、进程内存活检测、启动时回放、Node 重连对账、ACP session/load 续跑、恢复说明、只补发未读的追加消息
- [ ] 数据保留：归档已关闭超过 90 天的任务的 Run 日志和 diff
- [ ] `sous chat`：终端聊天（经由本地 IPC 转发）
- [ ] update：检查 GitHub Releases、下载并替换、界面上的更新按钮
- [ ] 加上 `.github/workflows` 发布流程：打 tag 后用 goreleaser 构建并发布
- [ ] 验收：requirements.md 里的验收场景 2、4、5

## 收尾

- [ ] 把长期有效的结论整理进 `docs/architecture/`，并更新 `docs/README.md`
