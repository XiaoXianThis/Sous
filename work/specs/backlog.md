# backlog

TODOs and ideas that belong to no spec. When an item takes shape, move it into a new spec and remove it here.

## 项目面板（wokspace 项目的可视化）

M5 之后再做。前提：M5 已经记下项目是不是 wokspace（`projects.layout`）。

- 符合 wokspace 规范的项目，项目页多一个可视化面板：`apps/`、`build/`、`docs/`、`work/` 四个目录对应四个 tab，可以切换。
- `apps` tab 是项目导览：每个 app 一个节点（用途、技术栈、任务表），app 之间的依赖（`build/` 产物、`docs/api/` 契约）画成连线；点进 app 看它的模块结构，也用节点图表示。
- 可以切换到传统的目录树 + 文件查看。
- 待定：文件内容从哪台机器读（Hub 不碰仓库，见 0002，需要走 Node 的只读请求）；模块结构怎么得到（静态分析还是让 agent 生成并缓存）。
