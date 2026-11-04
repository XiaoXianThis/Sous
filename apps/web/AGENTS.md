# web

- Purpose: sous 的 PWA 前端：聊天、任务、审批、机器与设置界面。手机、平板、浏览器直接用；构建产物同时被 desktop 内嵌。
- Stack: React 19 + TypeScript + Vite 6 + Tailwind CSS 4（设计变量在 `src/app.css`，映射成 `bg-page`、`text-ink` 等类），Vitest；`react-markdown` + `remark-gfm` + `highlight.js`（聊天的 Markdown）；PWA（手写 Service Worker 和 manifest，见 `public/`）；`@xterm/xterm`（机器上安装、登录 agent 时的终端）。
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
- 共享状态在 `src/lib/app.ts`：`chat`、`ui`、`wb` 是可变的 store。改完字段后必须调用对应的 `emit()`，组件用 `useChat()` / `useUi()` / `useWb()` 订阅；不调用 `emit()` 的修改不会重绘。
- 界面只做中文。中文输入法组合输入时（`isComposing`）按 Enter 不能发送消息。
- 配色参照 Cursor 主题（开源复刻 BioHazard786/cursor-theme-vscode）：灰色是框架（`bg-rail`、`bg-chrome`：左栏、聊天栏、设置导航），近白是内容区（`bg-page`），白色是卡片（`bg-panel`），层级靠深浅和 1px 细线（`--line`）区分；唯一的强调色是天蓝 `--accent`（运行中也用它），琥珀色 `--warm` 只表示“要你处理”。深色模式由 `src/lib/theme.ts` 写在 `<html data-theme>` 上（跟随系统 / 浅色 / 深色，存在 localStorage `sous.theme`；`index.html` 里的内联脚本在加载前先套上），深色变量写在 `:root[data-theme='dark']`，不要用 `prefers-color-scheme` 媒体查询。
- 颜色只用 `src/app.css` 里的 CSS 变量（Tailwind 主题映射成 `bg-page`、`bg-panel`、`bg-sunken`、`bg-hover`、`text-ink`、`text-muted`、`text-faint`、`border-line`、`bg-accent`、`shadow-card`、`shadow-soft`、`shadow-pop` 等），浅色和深色都要能看。
- 视觉语言是“客户端”而不是网页：卡片用阴影（`.card`、`shadow-*`）而不是边框，信息优先用图标 + 数字表达（`.pill tone-<语气>`），操作优先用图标按钮（`.icon-btn` 加 `aria-label` 和 `data-tip` 悬停提示，`data-tip-pos="bottom"` 放在下方），只有主要操作用带字的 `.btn`。
- agent 的图标在 `AgentIcon.tsx`（claude、codex、cursor、opencode、kiro、sous，未知的显示首字母）；“谁在干活”用 `Avatar.tsx` 的 `Who`：岗位头像带 agent 角标（`stack` 时并排），没有岗位时只显示 agent 图标。项目图标是 `ProjectTile.tsx`（按项目 id 取色）。新增 agent 时在 `AgentIcon.tsx` 加一个标记。
- 工作台（`docs/architecture/workbench.md`，`work/specs/workbench-single-page`）：`Shell.tsx` 是左侧图标栏（工作台、对话开关、机器状态、深浅色切换、设置）+ 工作台 + 聊天栏；手机三个标签。工作台只有一页，只放项目卡片：顶部标题行右侧是几个图标加数字的概况（正在执行、要你处理、待办、机器在线），再右边是“+ 项目”；“要你处理”（待审批 + 收件箱里的任务，审批只在这里显示，不进项目卡片）默认收起，点它的图标才弹出 popover；`#/inbox` 只是打开这个 popover。工作台上的任务行是两行高：标题和任务状态，下面是最近一次 Run 的状态（`CardTask.run` / `runLook`）和接手的 agent（图标 + 名称）；不画阶段进度条（进度条只在弹窗的任务详情里）。路由在 `src/lib/route.ts`（`#/`、`#/projects` 兼容旧链接、`#/project=<id>`、`#/task=<id>`、`#/inbox`），`Shell` 始终渲染 `Workbench`，`task` / `project` 决定顶层 sheet，`inbox` 只是打开“要你处理”popover。工作台数据是 `GET /overview` + `GET /inbox`（`wb.data`、`wb.inbox`），按 `tasksRev`、`nodesRev`、`inboxRev`、`runsRev` 去抖重拉；`run.output` 不重拉。项目卡片由 `src/lib/workbench.ts` 的 `projectCards` 计算（包含已归档的项目，`card.archived`；工作台只画激活的，“打开”按钮弹出 `ArchiveModal.tsx`，用 `archivedCards` 按 `touchedAt` 倒序和搜索过滤，卡片复用 `Workbench` 的 `renderCard`；归档 / 激活走 `src/lib/projects.ts` 的 `setArchived`），卡片右下角是 `src/lib/time.ts` 的 `ago(touchedAt)`（相对时间，工作台每分钟重绘一次），卡片排成等高的网格；卡片里有“任务 / 详情”两个标签（`ProjectTabs`，标签状态在 `Workbench` 里，卡片和弹窗共用），详情是懒加载的文件树 `FileTree.tsx`；点卡片上的放大按钮或树里的文件会打开 `ProjectModal.tsx`（从卡片位置长出来、关闭时缩回去的弹窗，`data-project-card` 用来找到卡片），弹窗里的 `FileEditor.tsx` 可以看和改文件（`GET/PUT /projects/{id}/file`，保存带 `base_sha256`，Cmd/Ctrl+S 保存）。圆角用 9 / 12 / 14 / 16 / 18 这一档（`--r-sm` `--r-md` `--r-lg`），`Icon` 会把尺寸吸附到 12/14/16/18/20/24 并统一描边粗细，列表项最小高度 64px，按钮 38px（small 32px）（`auto-rows-fr`，不是瀑布流）；任务只有 `todo/active/attention/closed` 四态（`status.ts`），详细状态只做 badge 文案。运行中 Run 的活动由 `src/lib/live.ts` 累积在 `wb.live`（100ms 合批），只有 `AgentCard.tsx` 读它；刷新时用 `/runs/{id}/log` 的尾部补最近输出。任务不再有单独的 sheet：`#/task=<id>` 会打开该任务所在项目的 `ProjectModal`，“任务”标签里左侧竖排列出项目的全部任务（`TaskRow`，`ProjectCard.listTasks`），右侧是 `TaskView embedded`（手机上列表和详情分页）；弹窗的显示由 `Workbench` 根据路由和 `open` 状态决定，项目 sheet 复用 `ProjectEditor`，Run sheet 用 `api.run` + live。图标在 `Icon.tsx`（内联 SVG），岗位头像是 `Avatar.tsx`，Run 的计划和步骤列表在 `RunSteps.tsx`，颜色类 `c-<颜色>`、语气类 `tone-<语气>` 和组件嵌入规则 `.embed` 在 `app.css`。
- 聊天里 Run 只显示成 `RunChip`，步骤在项目弹窗的任务详情里（`TaskView` 用 `RunCard full`）。设置是居中弹窗 `SettingsModal`（手机上全屏），点击外部不关闭，只能用关闭按钮或 Esc；含“外观”页切换主题。原来各自是弹窗的组件放进 `.embed` 容器后按页面内容显示（`app.css` 里 `.embed > .backdrop:first-child + .sheet` 的规则），所以这些组件的根节点顺序必须是“遮罩、面板”。
- 推送通知的链接是哈希路由（`#approvals`、`#task=<id>`、`#thread=<id>`），由 `src/lib/route.ts` 的 `parseRoute` 解析、`followHash` 处理（变成工作台路由）；Service Worker 在已打开的窗口里用 `postMessage({type: 'open', url})` 通知页面。
- 列表类视图（任务、首页“需要你处理”、待审批数）按 `chat.tasksRev` / `chat.inboxRev` 重新拉取；有新的事件类型会影响它们时，记得在 `reduce.ts` 里加上对应的计数。Run 结束或暂停会让它的待审批过期但不产生审批事件，所以 `run.ended`、`run.paused` 也要刷新首页。定时任务列表按 `chat.schedulesRev` 重新拉取。
- 聊天界面参照 ZCode（zai-org/ZCode 的 `DESIGN.md` 和 `ai-elements/message.tsx`）：助手消息不画头像和气泡，整宽显示 Markdown；用户消息是右侧的中性灰圆角气泡（`--user-bubble`），纯文本；悬停显示复制和时间；输入框是 16px 圆角、细边框、不发光，发送按钮是 `bg-ink` 的圆形。Markdown 由 `Markdown.tsx` 渲染（`react-markdown` + `remark-gfm`，不渲染原始 HTML；标题 `mt-6 mb-3`，列表 / 引用 / 表格 / 任务列表的样式写在组件里；代码块有语言、自动换行和复制按钮，用 `highlight.js/lib/core` 只注册常用语言，流式输出时不高亮）。链接色是 `--link`，行内代码底色是 `--code-inline`，高亮配色是 `app.css` 里的 `.hljs-*`。
- 对话的主体是 `ChatView.tsx`（标题行和对话选择、时间线、输入框），`ChatPanel.tsx`（右侧聊天栏，只列没有 `project_id` 的线程）和 `ProjectChat.tsx`（项目对话）都只是给它传线程列表和发送方式。`chat.threads` 里包含项目对话，按 `project_id` 过滤；`ui.projectThread[项目 id]` 是项目弹窗里显示的对话，第一次发消息时才建线程（`sendToProject`）；`openThread` 遇到项目对话会设置 `ui.openProject`，由 `Workbench` 打开那个项目的弹窗。
- 项目对话停靠在 `ProjectModal` 的右侧（弹窗加宽到 1560px），标题行的对话按钮开关它（`localStorage` 的 `sous.projectChat`；手机上默认关，打开时盖住整个弹窗）。项目里选中的任务会作为 `context.task_id` 随消息发出。后端把项目对话的作用域限定在这个项目（见 `docs/api/core-hub-http.md`）。
- 宽度可拖动：`ResizeHandle.tsx`（8px 透明热区，悬停 / 拖动时显示 2px 强调色细线，方向键微调，双击还原）+ `src/lib/resize.ts` 的 `useStoredWidth`。右侧聊天栏写到 `.shell` 的 `--chat-w`（`localStorage` 的 `sous.chatWidth`，CSS 用 `clamp` 保证工作台至少 420px），项目对话是 `sous.projectChatWidth`。
- `kind: trigger` 的消息（定时任务、自动跟进）虽然 `role` 是 `user`，但不是用户输入的，显示成一行可展开的系统提示，不显示成用户气泡。
- 机器上的安装、登录作业：`agent.job.*` 事件由 `src/lib/jobs.ts` 累积成 `chat.jobs`（按字节 `offset` 去重，和 `GET /nodes/{id}/jobs/{job}` 的尾部合并），`JobLog.tsx` 把它写进 xterm，并从输出里找出链接和验证码。管理界面是 `AgentPanel.tsx`（从“机器”打开）和 `ConfigEditor.tsx`。
- 项目终端：`ProjectModal` 的第三个标签 `shell`（`ProjectTabs` 的 `shell` 属性，卡片上不显示，卡片把它当 `tasks`）。`ShellPanel.tsx` 管标签和选机器（`GET/POST /projects/{id}/shells`），每个终端一个 `ShellTerm.tsx`（xterm，输出来自 `chat.jobs`，按键 15ms 合批发 `job input`）；切走时保持挂载、隐藏，回来再 fit。打开遇到 401 `reauth` 时显示“验证并打开”，按钮里调用 `passkey.ts` 的 `reauthenticate()`（开发模式走 dev-login）——WebAuthn 要在用户点击里调用，不要在 fetch 之后自动弹。
- 模型：设置的“模型”页是 `Models.tsx`（角色的模型 / 思考强度、供应商增删改、读取模型列表、测试）；聊天标题旁的 `ModelSwitch.tsx` 直接切主管的模型和思考强度。选项值是 `provider_id + \u0000 + model`（`choiceValue` / `parseChoice`）。改供应商可能 401 `reauth`，按钮里调 `reauthenticate()` 再重试。显示成“跟主管一样”的角色（`inherited`）不要把主管的值当成自己的。
- 项目位置：`Locations.tsx` 的机器标签显示 `gitMark`（↓落后 ↑未推送 ●未提交），点开是状态和“同步”“存成 WIP 分支”（`api.repoSync` / `repoWip`）。`node.repos` 事件让 `nodesRev` 增加，总览会重拉。
- Hub 的 CSP 禁止内联脚本和外部资源：不要在 `index.html` 里写 `<script>` 内容，也不要引用其他域名的脚本、样式、字体。
- 在内置浏览器面板里验收时，只改哈希的导航不会重新加载页面；要看新构建，用 `location.reload()`。
