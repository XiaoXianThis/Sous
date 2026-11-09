# site

- Purpose: sous 的官网与文档站（第一阶段：骨架、设计系统、首页、文档站框架与首批核心文档）。构建产物输出到 `build/site/`（`$WOK_OUT`）。
- Stack: Astro 5 + Tailwind CSS 3 + TypeScript（包管理器使用 `bun`，使用 Astro 内置 Vite，不引入 React/Vue/Svelte 等 UI 框架，不使用第三方文档模板主题）。
- Depends on: none

## Tasks

Commands run in `apps/site/`. Leave a command empty to use what `wok` detects; `wok ls site` shows the result.

| task | command | windows |
|---|---|---|
| install | bun install | |
| dev | bun run dev | |
| test | bun run test | |
| build | bun run build -- --outDir "$WOK_OUT" | |

## Conventions

- 构建产物统一输出到仓库根的 `build/site/`（在 `astro.config.mjs` 中默认指向 `../../build/site`，并通过 `wok build site` 的 `--outDir "$WOK_OUT"` 覆盖）。
- 设计系统 token 集中在 `tailwind.config.mjs` 与 `src/styles/global.css`：
  - 色彩：`canvas`、`page`、`panel`、`raised`、`sunken`、`ink`、`muted`、`faint`、`line`、`line-strong`、`accent`、`accent-ink`、`ok`、`warn`。
  - 主题：深色为主，同时支持浅色并默认跟随系统（`prefers-color-scheme`），顶栏提供「跟随系统 / 深色 / 浅色」切换（保存在 `localStorage` 的 `sous.site.theme`）。
  - 动效：仅保留克制的淡入与悬停过渡，在 `@media (prefers-reduced-motion: reduce)` 下关闭所有动画与过渡。
- 文档内容全部存放在 `src/content/docs/`（Astro Content Collections），目录分组与排序集中在 `src/lib/docs-nav.ts`。
- 严禁编造功能、命令或配置字段：所有安装命令、CLI 参数、架构说明与接口描述均以 `docs/` 和 `apps/core` 实际实现为准。
