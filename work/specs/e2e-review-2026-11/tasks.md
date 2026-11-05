# 端到端测试后的修复任务

按报告的建议拆成可勾选的任务；P0 先做，完成一项勾一项。

## P0：信任问题

- [ ] 识别 agent 额度/限流/不可用回合（`usage limit`、`quota`、`rate limit`、`Upgrade your plan`、`monthly usage limit`），Run 标记失败，UI 给出“换 agent/重试/去管理”
- [ ] ops 任务增加部署验收：要求部署命令退出码或独立健康检查通过，否则 blocked；不再只凭 `end_turn` 完成
- [ ] 代码任务空 diff（非只读）判定失败/受阻，不进入 reviewer 重试
- [ ] 审批拒绝即结束 Run 并把任务置 blocked；同任务同指纹不重复请求；提供显式重试/换 agent/换机器
- [ ] `deliver`/`rebase` 不占工作并发（或独立高优先级池）；UI 区分“排队交付”和“等待机器”
- [ ] 任务行显示最后一次实际执行 agent/机器；任务投影加 `last_run_agent`/`last_run_node`
- [ ] 阶段条“部署”由 `deploy_state` 驱动，只在 ops 任务 done 且健康检查通过时画绿

## P1：体验与可运维性

- [ ] 审批卡中文化：按 option.kind 映射“允许一次/始终允许/拒绝”；常见标题本地化；作用域说明
- [ ] 审批详情：项目/机器/agent、完整命令、脚本正文/前 N 行、文件路径与 diff 摘要、风险命中片段
- [ ] 复核“混淆或下载后执行”高风险误报，补回归用例
- [ ] 收件箱按项目分组、默认隐藏归档、批量标记、稍后提醒、跟进去重
- [ ] 统一状态词表；顶部统计改成“运行中/排队/验收中/待你处理”并加说明
- [ ] 新建任务：按类型过滤机器/项目/agent；归档/服务器/离线/未安装选项置灰并说明
- [ ] “登记已有仓库”排除 server 节点，按项目/远端分组，修复截断与按钮裁切
- [ ] 项目创建保证首个 commit；无远端时提示“不能 push/PR/部署”并隐藏相应阶段
- [ ] 任务页顶部放主操作（重试/取消/换机器/查看 diff）；失败/受阻任务一键重试
- [ ] 错误分类与中文文案（agent_quota / rate_limit / not_logged_in / unavailable）；原始错误折叠
- [ ] 运行日志保存脱敏后的工具输出/退出码/错误
- [ ] 连续失败/无进展检测：3~5 次自动暂停并发 `run.stuck` 通知
- [ ] 离线缓存最近 overview/线程；中文离线提示；手机 Enter 行为与 placeholder 一致
- [ ] 每角色默认 effort（reviewer/approver 低一档）；usage 按项目/任务归因
- [ ] 可访问性：提高 `--faint`/`--muted` 对比度、修正 aria-label、允许缩放；CI 跑 axe
- [ ] 代码分割：设置/终端/Markdown/高亮懒加载，首包目标 < 300 KB gzip

## P2：规模化与打磨

- [ ] 全局搜索/命令面板（项目、任务、线程、机器）
- [ ] 固定/排序项目；全局“运行中”抽屉
- [ ] 记忆增加来源/时间/过期标记；supervisor 增加项目归档/删除工具或 UI 指引
- [ ] PWA：PNG badge、shortcuts、screenshots、在线状态
- [ ] 节点名称来源统一；项目位置用“开发机/部署目标/远端”标签 chips
- [ ] 真实 GitHub/GitLab、真实 Web Push、多操作系统验收
