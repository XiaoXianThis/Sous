# assets

Assets shared by several apps, and asset sources (design files, vector sources, raw audio and video). Final assets used by only one app live inside that app.

## Rules

- Organize by type, such as `icons/`, `images/`, `design/`, `fonts/`.
- Apps use exported assets, copied by the app or referenced at build time. Apps never modify the sources here.
- Before committing a file larger than 10 MB, ask the user. The user decides whether to use Git LFS.
- Record the source and license of every third-party asset in the list below.

## Assets

| asset | source | license |
|---|---|---|
| `design/ui-redesign/*.png` | 界面参考图：`overview/project/task/chat-*.png` 是早期稿；`workbench-*.png` 是 2026-11-04 用新梦 GPT Image 2.5 Flare（high）生成的工作台风格稿，`workbench-v2-*.png` 按真实三栏布局和状态语义收紧约束后重绘；`single-page-*.png` 是工作台单页实现后的浏览器验收截图（桌面、深色、手机、Run 详情） | 自有生成，仅作设计参考 |
