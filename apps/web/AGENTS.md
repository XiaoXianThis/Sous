# web

- Purpose: sous 的 PWA 前端：聊天、任务、审批、机器与设置界面。手机、平板、浏览器直接用；构建产物同时被 desktop 内嵌。
- Stack: Svelte 5（runes）+ TypeScript + Vite 6，Vitest；PWA（手写 Service Worker 和 manifest，见 `public/`）。
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
- 推送通知的链接是哈希路由（`#approvals`、`#task=<id>`、`#thread=<id>`），由 `src/lib/tasks.ts` 的 `parseHash` 解析、`followHash` 处理；Service Worker 在已打开的窗口里用 `postMessage({type: 'open', url})` 通知页面。
- 列表类视图（任务、首页“需要你处理”、待审批数）按 `chat.tasksRev` / `chat.inboxRev` 重新拉取；有新的事件类型会影响它们时，记得在 `reduce.ts` 里加上对应的计数。Run 结束会让它的待审批过期但不产生审批事件，所以 `run.ended` 也要刷新首页。
- 在内置浏览器面板里验收时，只改哈希的导航不会重新加载页面；要看新构建，用 `location.reload()`。
