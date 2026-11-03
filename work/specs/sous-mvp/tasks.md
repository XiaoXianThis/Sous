# sous MVP：任务

按里程碑排列。每个里程碑结束时，都有一个能实际跑起来、能验收的成果。

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

- [ ] 逐一实测五个 agent 在 macOS、Windows、Linux 上的 ACP 启动方式、“每个操作都询问”的启动参数、登录状态的检测方法、能否把长命令转到后台，结果写进 design.md
- [ ] proto：Hub ↔ Node 协议，与 `docs/api/core-hub-node.md` 保持一致
- [ ] auth：配对码和设备令牌，存入系统密钥存储
- [ ] node：连接、心跳、上报机器和 agent 清单（含登录状态）、断线时缓存事件、重连后补发
- [ ] workspace：`~/.sous/worktrees/` 下 worktree 的创建、命名、清理；一般任务的 `~/.sous/scratch/` 目录
- [ ] 上传 diff（`run.diff`），Hub 端保存到 `runs/<run_id>.diff`
- [ ] 临时性故障的自动重试
- [ ] worker/acp：会话管理、session/update 和 permission 的映射
- [ ] worker/jsonstream、worker/pty、worker/shell 三个退路实现
- [ ] `sous` 命令行：daemon / pair / status / service install
- [ ] web：机器页面、Run 卡片（实时进度）
- [ ] 验收：在网页上让 Mac 上的 Claude Code 修改一个仓库，全过程实时可见

## M3 核心闭环

- [ ] task：状态机、子任务、依赖、代码任务和一般任务；主管的任务工具；线程后续消息转发给正在执行的 Run
- [ ] router：手动指定、按优先级、排队后等机器上线再派发
- [ ] web：任务看板和列表、项目与路由优先级的配置界面
- [ ] policy：操作分类、三档模式、底线规则（Node 本地执行），子 agent 以“每个操作都询问”的方式启动
- [ ] 审批流：approval 事件、AI 审批 agent（approver）、界面上的审批队列
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
