# web

- Purpose: sous 的 PWA 前端：聊天、任务、审批、机器与设置界面。手机、平板、浏览器直接用；构建产物同时被 desktop 内嵌。
- Stack: Svelte 5（runes）+ TypeScript + Vite 6，Vitest；PWA（手写 Service Worker 和 manifest，见 `public/`）；`@xterm/xterm`（机器上安装、登录 agent 时的终端）。
- Depends on: core 的 `docs/api/core-hub-http.md` 契约。构建产物 `build/web/` 由 core 的 Hub 托管（运行时读取），也被 desktop 消费。

## Tasks

Commands run in `apps/web/`. Leave a command empty to use what `wok` detects; `wok ls web` shows the result.

| task | command | windows |
|---|---|---|
| install | npm ci | |
| dev | npm run dev | |
| test | npm test | |
| check | npm run check | |
| build | npm run build -- --outDir "$WOK_OUT" | |

## Conventions

- 使用 npm（不要用 pnpm 或 yarn），锁文件是 `package-lock.json`。
- `npm run dev` 在 5173 端口启动，把 `/api`、`/ws` 代理到 `SOUS_HUB`（默认 `http://127.0.0.1:8787`，先 `wok dev core`）。也可以 `wok build web` 之后直接打开 Hub 的 8787 端口。
- 状态变化写成纯函数放在 `src/lib/reduce.ts`，并配上 `*.test.ts` 单元测试；组件里不写事件处理逻辑。
- 界面只做中文。中文输入法组合输入时（`isComposing`）按 Enter 不能发送消息。
- 颜色只用 `src/app.css` 里的 CSS 变量，浅色和深色都要能看。
- 工作台（`docs/architecture/workbench.md`，`work/specs/workbench-single-page`）：`Shell.svelte` 只有工作台 + 聊天 + 设置；手机三个标签。路由在 `src/lib/route.ts`（`#/`、`#/projects` 兼容旧链接、`#/project=<id>`、`#/task=<id>`、`#/inbox`），`Shell` 始终渲染 `Workbench`，`task` / `project` 决定顶层 sheet，`inbox` 只是“要你处理”筛选。工作台数据是 `GET /overview` + `GET /inbox`（`wb.data`、`wb.inbox`），按 `tasksRev`、`nodesRev`、`inboxRev`、`runsRev` 去抖重拉；`run.output` 不重拉。项目卡片由 `src/lib/workbench.ts` 的 `projectCards` 计算，瀑布流的列由 `src/lib/masonry.ts` 分配；任务只有 `todo/active/attention/closed` 四态（`status.ts`），详细状态只做 badge 文案。运行中 Run 的活动由 `src/lib/live.ts` 累积在 `wb.live`（100ms 合批），只有 `AgentCard` 读它；刷新时用 `/runs/{id}/log` 的尾部补最近输出。任务 sheet 复用 `TaskView` 的 `embedded`，项目 sheet 复用 `ProjectEditor`，Run sheet 用 `api.run` + live。图标在 `Icon.svelte`（内联 SVG），岗位头像是 `Avatar.svelte`，颜色类 `c-<颜色>` 和 `tone-<语气>` 在 `app.css`。
- 聊天里 Run 只显示成 `RunChip`，步骤在任务 sheet（`TaskView` 用 `RunCard full`）。设置是全屏的 `SettingsModal`：原来各自是弹窗的组件放进 `.embed` 容器后按页面内容显示（`app.css` 里 `.embed > .backdrop:first-child + .sheet` 的规则），所以这些组件的根节点顺序必须是“遮罩、面板”。
- 推送通知的链接是哈希路由（`#approvals`、`#task=<id>`、`#thread=<id>`），由 `src/lib/route.ts` 的 `parseRoute` 解析、`followHash` 处理（变成工作台路由）；Service Worker 在已打开的窗口里用 `postMessage({type: 'open', url})` 通知页面。
- 列表类视图（任务、首页“需要你处理”、待审批数）按 `chat.tasksRev` / `chat.inboxRev` 重新拉取；有新的事件类型会影响它们时，记得在 `reduce.ts` 里加上对应的计数。Run 结束或暂停会让它的待审批过期但不产生审批事件，所以 `run.ended`、`run.paused` 也要刷新首页。定时任务列表按 `chat.schedulesRev` 重新拉取。
- `kind: trigger` 的消息（定时任务、自动跟进）虽然 `role` 是 `user`，但不是用户输入的，显示成一行可展开的系统提示，不显示成用户气泡。
- 机器上的安装、登录作业：`agent.job.*` 事件由 `src/lib/jobs.ts` 累积成 `chat.jobs`（按字节 `offset` 去重，和 `GET /nodes/{id}/jobs/{job}` 的尾部合并），`JobLog.svelte` 把它写进 xterm，并从输出里找出链接和验证码。管理界面是 `AgentPanel.svelte`（从“机器”打开）和 `ConfigEditor.svelte`。
- 在内置浏览器面板里验收时，只改哈希的导航不会重新加载页面；要看新构建，用 `location.reload()`。
