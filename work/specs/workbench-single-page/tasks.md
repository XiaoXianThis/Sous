# 工作台单页：任务

需求见 `requirements.md`，设计见 `design.md`。每一组结束时都能跑起来、能验收。

## 当前状态（接手先读这里）

- **已实现并验收**（2026-11-04）。只改了 `apps/web`，core 没有改动。
- **测试**：`wok test web` 63 个用例全部通过，`wok run web check` 0 error，`wok build web` 通过。
- **浏览器验收**：Vite 5173 + 一个独立的无头 Chrome（CDP 9333），用真实 Hub（8787）的数据跑过桌面 1440、手机 390、浅色/深色、任务/项目/设置 sheet、`#/task=`、`#/project=`、`#/inbox`、浏览器返回、新建任务、派发、实时输出、Run 详情、审批（Run sheet 里 Allow once）和完成后的状态回写；最后在 8787 的生产构建上复验。
- **和设计不同的地方**：瀑布流用“列栈 + 估算高度”而不是 `grid-row-end` span（行优先的网格会在短列下面留大洞）；顶层 sheet 由路由驱动，Run/新建任务是本地覆盖层，没有单独的 sheet 栈。`design.md` 已同步。
- **没有直接验收的**：多台机器/离线/GitHub/部署目标 chip（当前环境只有一台在线 Mac、无远端、无部署目标），这四个有单元测试覆盖；项目卡里的审批卡没有单独触发，审批是在 Run sheet 里验证的（同一个 `ApprovalCard`）。

## 0 开工前

- [x] 用户看过 `requirements.md` 和 `design.md`，给出口令
- [x] 确认工作区未提交的 `app.css`、`ChatPanel.svelte`、`Message.svelte` 改动与本 spec 不冲突（保留原样）
- [x] 读 `apps/web/AGENTS.md`（M5 工作台约定）和 `docs/architecture/workbench.md`

## 1 纯函数和测试

- [x] `lib/status.ts`：`TaskState`、`taskStates`、`taskState()`、`taskStateDetail()`，覆盖 design §4 映射表的每一行
- [x] `lib/workbench.ts`：`projectCards(overview, inboxTasks)`、Run 到项目的归属、位置列表、排序；配单元测试
- [x] `lib/live.ts`：`Activity`、`applyLive`、`tail`、`oneLine`、`parseLogTail`、上限和合并规则；配单元测试
- [x] ~~`lib/masonry.ts`~~ 已移除：卡片改为等高网格
- [x] `lib/reduce.ts`：`runsRev`，生命周期事件自增，`run.output` 不自增；配测试
- [x] `lib/route.ts`：保留现有 hash 形式，补任务 / 项目 / inbox 的测试
- [x] `wok test web` 通过

## 2 单页骨架

- [x] `Workbench.svelte`：标题、需要你处理筛选、新建 / 登记入口、机器条、卡片列栈、空状态
- [x] 瀑布流：`columnCount` + `distribute` + `estimateHeight`，容器 `bind:clientWidth`
- [x] `Shell.svelte`：删掉五个页面的分支，主体固定为 `Workbench`；任务/项目 sheet；桌面图标栏和手机标签改成工作台 / 对话 / 设置
- [x] `app.svelte.ts`：`wb.inbox`、`wb.live`、`loadInbox`、日志种子、焦点恢复、`runsRev` 刷新节奏
- [x] 验收：`#/`、`#/projects`、`#/inbox`、`#task=`、`#project=` 都能落到工作台，旧推送链接不报错

## 3 项目卡片

- [x] `ProjectCard.svelte`：卡片布局、稳定 key、最多 4 条任务 / 2 个 agent、计数和“还有 N 项”
- [x] `Locations.svelte`：机器（Mac / PC / Linux、服务器、离线）、部署目标、远端 host；点击打开机器设置或仓库
- [x] `TaskRow.svelte`：四态 badge + 标题 + 一行摘要 + 紧凑阶段条
- [x] 内联审批：`ApprovalCard` 出现在对应 agent 卡里；失败 / 受阻任务可就地打开任务 sheet 重试
- [x] 卡片底部操作：新建任务、部署（打开项目 sheet）
- [x] 验收：场景 1、2、5、6

## 4 详情 sheet

- [x] `TaskView` 加 `embedded` prop（去掉整页 padding、返回按钮和高度）
- [x] `TaskSheet.svelte`：按 id 拉取任务，复用 `TaskView`，关闭后恢复焦点
- [x] `ProjectSheet.svelte`：位置、部署、默认岗位、说明、编辑、完整任务列表、新建任务、待审批
- [x] `ProjectSetupSheet.svelte`：新建项目、登记发现的仓库
- [x] `RunSheet.svelte`：已存事件 + 实时活动 + 审批 + 操作 + 日志 / 改动
- [x] 验收：场景 6、9（深链接和返回键）

## 5 实时输出

- [x] `startSession` 的流回调先 `applyLive`，再 `applyEvent`
- [x] `reduce.ts` 加 `runsRev`（`run.queued/started/ended/paused/resumed`），`run.output` 不触发
- [x] `Shell` 按 `runsRev` 去抖重拉 `/overview` 和 `/inbox`，删掉基于 `chat.runs` 的 `runKey`
- [x] `AgentCard.svelte`：meta 行（岗位 · agent · 模型 · 思考强度 · 机器）、2 行活动流、没有输出时的占位
- [x] live store 节流（100ms flush）、上限和清理；首次出现时读 `/runs/{id}/log` 尾部
- [x] 验收：场景 3、4；流式输出期间没有额外 `/overview` 重拉（代码路径 + 真实运行确认）

## 6 清理旧页面

- [x] 删除 `Overview.svelte`、`ProjectsList.svelte`、`Inbox.svelte`、`ProjectBoard.svelte`
- [x] 清理 `lib/tasks.ts` 里不再使用的 `board` / `columns` / `tone`，`lib/workbench.ts` 里不再使用的 `rows` / `load` / 导出
- [x] `Shell.svelte` 里没有残留的旧页面 import 和 `view === ...` 分支
- [x] `npm run check` 没有未使用变量和类型错误

## 7 手机和细节

- [x] 手机一列瀑布流、三个标签、sheet 全屏
- [x] Esc / 返回键关闭最上层 sheet，焦点回到触发元素
- [x] 深浅色、空状态（没有机器、没有项目、没有任务）
- [x] 中文输入法：工作台里没有输入框；聊天保持原行为
- [x] 验收：场景 7、8

## 8 刷新后的最近输出

- [x] 采用方案 1：客户端读 `/runs/{id}/log` 尾部，截取最后 4 KB，用 `parseLogTail` 塞进 `wb.live`
- [x] 思考内容不承诺（不落盘）
- [x] 不需要改 `docs/api/core-hub-http.md`

## 9 收尾

- [x] 更新 `docs/architecture/workbench.md` 的“界面”一节（单页、项目卡片、四态）
- [x] 更新 `apps/web/AGENTS.md` 的工作台段落（路由、sheet、live store、瀑布流）
- [x] 更新 `work/specs/README.md` 的状态
- [x] `wok test web`、`wok run web check`、`wok build web` 通过
- [x] 在浏览器里跑验收场景 1～10，桌面、手机宽度、浅色、深色
