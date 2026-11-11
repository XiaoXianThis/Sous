# 设计

逐步的执行说明见 [execution.md](execution.md)。本文只写定下来的设计。

## 1. 模型：事件日志居中的星形

Hub 内的模块之间不直接通信，都经过事件日志（唯一写入者），所以图如实画成：

- **写入**：模块 → 事件日志。写入方由 `eventlog.Write` 在运行时按调用栈的 Go 包名判断（第一个不在 `eventlog`、`store` 里的 `internal/<包>` 帧），不需要改各个模块。
- **读取**：事件日志 → 监听它的模块（主管、任务引擎、定时、副作用队列），按事件类型决定点亮哪条连线。
- **直接调用**：接口 ↔ 客户端（HTTP、websocket）、模块 → 模型路由 → 供应商、主管 → 工具所在模块（按工具名）、机器连接 ↔ 机器（按帧）、推送 → Web Push。

## 2. 模块（固定的）

| id | 名字 | 层 | 代码 |
|---|---|---|---|
| `ui` | 界面 | client | `apps/web` |
| `cli` | 命令行 | client | `apps/core/cmd/sous` |
| `httpapi` | 接口 | hub | `internal/httpapi` |
| `eventlog` | 事件日志 | hub | `internal/eventlog`、`internal/store` |
| `effects` | 副作用队列 | hub | `internal/effects` |
| `notify` | 推送 | hub | `internal/notify` |
| `supervisor` | 主管 | hub | `internal/supervisor` |
| `task` | 任务引擎 | hub | `internal/task` |
| `trigger` | 定时与跟进 | hub | `internal/trigger` |
| `inspector` | 巡检 | hub | `internal/inspector` |
| `models` | 模型路由 | hub | `internal/models` |
| `mcp` | MCP 客户端 | hub | `internal/mcp` |
| `web` | 联网 | hub | `internal/web` |
| `nodehub` | 机器连接 | hub | `internal/nodehub` |
| `search` | 搜索引擎 | external | — |
| `push` | Web Push | external | — |

动态实例：`provider:<名字>`（每个已配置的模型供应商，external）、`mcpserver:<slug>`（每个 MCP 服务，external）、`machine:<node id>`（每台已配对、没吊销的机器，node）。

## 3. 连线

见 execution.md 第 2 步的表格，那是唯一的权威版本。

## 4. 状态

每个模块实现 `sysmap.Prober`。状态值 `ok` / `busy` / `idle` / `degraded` / `down`，界面上分别显示“正常”“忙”“空闲”“降级”“停止”。机器节点的状态由 nodehub 提供（在线 = `ok` 或 `busy`，离线 = `down`）。

## 5. 推送

单独的 websocket `GET /api/v1/system/stream`（只给登录会话），不走 `/ws`：`/ws` 现在不读客户端发来的消息，而且大部分页面用不到这些数据。连上之后 Hub 每秒发一帧 `sys.tick`，没有连接时不跑定时器。

## 6. 界面（2026-11-11 修订）

- 路由 `#/system`，`Shell` 在 `main.work` 里渲染 `SystemMap`（`lazy()` 加载），不用图形库。
- 时序图：列是参与者（`coord.ts` 的 `LANES`，一列可以包含几个模块），行是交接（`steps()`）。写入和它叫醒的读取配成一条箭头，直接调用来自 core 的 `call <类型>` 记录（`sysmap.Callers`），重复的折叠成 ×N，按任务或对话归组。
- 列头和行点开 `.sheet` 弹窗（`SystemDetail.tsx`）。
- 颜色遵守 web 的约定：只有黑白灰和天蓝 `--accent`。忙用天蓝闪烁的点，刚到的行用天蓝，降级和停止靠字重、图标和文字区分。

## 7. 和文档的关系

`docs/architecture/system-map.md` 说明各层、模块和连线，并指出实时的图在“系统”页。模块的 `Desc` 和这篇文档用同样的说法。
