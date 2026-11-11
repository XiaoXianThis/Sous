# 执行说明：系统架构图

给执行的 agent：按顺序做，每一步做完跑这一步写的验证命令，全部通过再做下一步。本文已经定下所有命名、文案、坐标和行为；**不要自己发挥**。遇到本文没写到、而且代码里也找不到唯一答案的情况，停下来，把问题写清楚交回给指派你的人，不要猜。

## 0. 通用规则

- 先读：根目录 `AGENTS.md`、`apps/core/AGENTS.md`、`apps/web/AGENTS.md`、本目录的 `requirements.md` 和 `design.md`。
- Go 代码在 `apps/core/` 下，模块路径是 `github.com/XiaoXianThis/Sous/apps/core`。web 代码在 `apps/web/`。
- Go 验证：在 `apps/core/` 里运行 `gofmt -l .`（必须没有输出）、`go vet ./...`、`go test ./...`。
- web 验证：在 `apps/web/` 里运行 `bun run check`、`bun run test`。
- 注释和标识符用英文，界面文字用中文，文案照本文原样抄。
- 不提交，除非指派你的人要求。不要改本文没有提到的文件，第 13 步列出的文档除外。
- 新增依赖只有一个：web 的 `@xyflow/react`，版本固定为 `12.12.0`（不加 `^`）。Go 不加依赖。

## 1. 新包 `internal/sysmap`：类型和注册表

新建目录 `apps/core/internal/sysmap/`。这个包**不能 import 任何 `internal/` 下的其他包**（其他包都要 import 它）。

文件 `sysmap.go`：

```go
// Package sysmap describes the Hub's modules and the lines between them,
// counts what flows along each line, and collects each module's status for
// the system map page (work/specs/system-map).
package sysmap

type Layer string

const (
	LayerClient   Layer = "client"
	LayerHub      Layer = "hub"
	LayerExternal Layer = "external"
	LayerNode     Layer = "node"
)

type State string

const (
	OK       State = "ok"
	Busy     State = "busy"
	Idle     State = "idle"
	Degraded State = "degraded"
	Down     State = "down"
)

type Module struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Layer Layer  `json:"layer"`
	Desc  string `json:"desc"`
	Code  string `json:"code,omitempty"`
}

type Edge struct {
	ID    string `json:"id"` // "<from>><to>"
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
	// Carries says in words what goes along the line, for the details panel.
	Carries string `json:"carries"`
}

type Gauge struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit,omitempty"`
}

type Item struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	State  State  `json:"state"`
	// Link is a hash route of the PWA, such as "#/task=<id>".
	Link string `json:"link,omitempty"`
}

type Status struct {
	State   State   `json:"state"`
	Summary string  `json:"summary"`
	Gauges  []Gauge `json:"gauges,omitempty"`
	Items   []Item  `json:"items,omitempty"`
}

// Prober reports a module's status. Probe must return within a few
// milliseconds: read memory, or a store query cached for 2 seconds.
type Prober interface {
	Probe(ctx context.Context) Status
}

type ProbeFunc func(ctx context.Context) Status

func (f ProbeFunc) Probe(ctx context.Context) Status { return f(ctx) }

// Instance is one module that exists at run time: a model provider, an
// MCP server, a machine.
type Instance struct {
	Module Module
	Status Status
}
```

文件 `registry.go`：一个包级的注册表 `var reg = &registry{}`，带 `sync.Mutex`，字段：

- `probes map[string]Prober`（模块 id → 探针）
- `instances map[string]func(ctx context.Context) []Instance`（种类 → 列出实例的函数，种类是 `provider`、`mcpserver`、`machine`）

导出函数：

```go
func RegisterProbe(moduleID string, p Prober)
func RegisterInstances(kind string, fn func(ctx context.Context) []Instance)
// Reset clears everything; tests only.
func Reset()
```

`RegisterProbe` 对同一个 id 再次注册时覆盖旧的。

## 2. 模块表和连线表（`modules.go`）

在 `modules.go` 里写两个包级变量。`Modules` 照下表**原样**写（顺序也一样）：

```go
var Modules = []Module{
	{"ui", "界面", LayerClient, "网页和桌面端：工作台、对话、设置", "apps/web"},
	{"cli", "命令行", LayerClient, "机器上的 sous chat，用设备令牌访问", "apps/core/cmd/sous"},
	{"httpapi", "接口", LayerHub, "REST 接口和 /ws 实时事件，登录和 CSRF 在这里检查", "internal/httpapi"},
	{"eventlog", "事件日志", LayerHub, "唯一的写入者：追加事件、更新视图、入队副作用，一个事务", "internal/eventlog"},
	{"effects", "副作用队列", LayerHub, "事件之后要做的事：推进任务、审批、推送，失败会重试", "internal/effects"},
	{"notify", "推送", LayerHub, "把要提醒的事件变成 Web Push 通知", "internal/notify"},
	{"supervisor", "主管", LayerHub, "每条对话一个串行 worker：读历史、调模型、调工具、回复", "internal/supervisor"},
	{"task", "任务引擎", LayerHub, "任务的状态、派发、验收、git 操作、项目顾问", "internal/task"},
	{"trigger", "定时与跟进", LayerHub, "定时任务到点、受阻任务的跟进、数据保留", "internal/trigger"},
	{"inspector", "巡检", LayerHub, "检查卡住、重复、过长的 Run，必要时暂停", "internal/inspector"},
	{"models", "模型路由", LayerHub, "按角色找到供应商和模型，记录每次调用", "internal/models"},
	{"mcp", "MCP 客户端", LayerHub, "连接用户配置的 MCP 服务，把它们的工具交给主管", "internal/mcp"},
	{"web", "联网", LayerHub, "web_search 和 web_fetch，带内网防护", "internal/web"},
	{"nodehub", "机器连接", LayerHub, "和每台机器的 websocket：下发 Run、收进展和审批", "internal/nodehub"},
	{"search", "搜索引擎", LayerExternal, "hub.yaml 里配置的搜索服务", ""},
	{"push", "Web Push", LayerExternal, "浏览器厂商的推送服务", ""},
}
```

（Go 里用带字段名的写法 `{ID: "ui", Name: "界面", ...}`，上面为了简短省略了字段名。）

`StaticEdges` 照下表原样写。`Carries` 列就是 `Edge.Carries` 的值：

| ID | From | To | Label | Carries |
|---|---|---|---|---|
| `ui>httpapi` | ui | httpapi | 请求 | 页面调用的 REST 接口 |
| `cli>httpapi` | cli | httpapi | 设备请求 | /api/v1/device/ 下的接口 |
| `httpapi>ui` | httpapi | ui | 实时事件 | /ws 推给页面的事件 |
| `httpapi>cli` | httpapi | cli | 实时事件 | /device/ws 推给命令行的事件 |
| `httpapi>eventlog` | httpapi | eventlog | 写入 | 用户在页面上的操作产生的事件 |
| `supervisor>eventlog` | supervisor | eventlog | 写入 | 回复、话题、记忆、整理 |
| `task>eventlog` | task | eventlog | 写入 | 任务、项目、Run 的创建和状态 |
| `trigger>eventlog` | trigger | eventlog | 写入 | 定时任务和跟进的触发 |
| `inspector>eventlog` | inspector | eventlog | 写入 | 巡检发现 |
| `nodehub>eventlog` | nodehub | eventlog | 写入 | 机器上报的 Run 进展、审批请求、上下线 |
| `notify>eventlog` | notify | eventlog | 写入 | 推送记录 |
| `mcp>eventlog` | mcp | eventlog | 写入 | MCP 服务的配置变化 |
| `models>eventlog` | models | eventlog | 写入 | 模型配置的变化 |
| `eventlog>supervisor` | eventlog | supervisor | 唤醒 | 用户消息、触发、并入 |
| `eventlog>task` | eventlog | task | 通知 | Run 结束、任务状态、审批、机器上线 |
| `eventlog>trigger` | eventlog | trigger | 通知 | 定时任务的改动 |
| `eventlog>effects` | eventlog | effects | 入队 | 需要后续处理的事件 |
| `effects>task` | effects | task | 执行 | 推进任务、审批、项目准备 |
| `effects>notify` | effects | notify | 执行 | 推送 |
| `notify>push` | notify | push | 发送 | Web Push 请求 |
| `supervisor>models` | supervisor | models | 调用 | 主管和整理的模型调用 |
| `task>models` | task | models | 调用 | 验收和审批的模型调用 |
| `inspector>models` | inspector | models | 调用 | 巡检的模型调用 |
| `supervisor>task` | supervisor | task | 工具 | 任务、项目、git、顾问的工具调用 |
| `supervisor>trigger` | supervisor | trigger | 工具 | 定时任务的工具调用 |
| `supervisor>web` | supervisor | web | 工具 | web_search、web_fetch |
| `web>search` | web | search | 联网 | 搜索和抓取网页 |
| `supervisor>mcp` | supervisor | mcp | 工具 | MCP 工具和管理 |
| `supervisor>nodehub` | supervisor | nodehub | 工具 | machine_look |

动态连线（由第 7 步展开，不写进 `StaticEdges`）：

| ID 格式 | Label | Carries |
|---|---|---|
| `models>provider:<名字>` | 请求 | 发给这个供应商的模型请求 |
| `mcp>mcpserver:<slug>` | 调用 | 这个 MCP 服务的工具调用 |
| `nodehub>machine:<node id>` | 下发 | Run 的开始、追加指令、取消、审批结果等帧 |
| `machine:<node id>>nodehub` | 上报 | Run 的进展、输出、结束、审批请求等帧 |

另外写一个函数：

```go
// EdgeID joins two module ids.
func EdgeID(from, to string) string { return from + ">" + to }
```

## 3. 事件和帧的分类（`classify.go`）

```go
// EventGroups maps an event type prefix to the group the map shows.
// Every event type must start with one of these prefixes.
var EventGroups = []struct{ Prefix, Group string }{
	{"thread.", "对话"}, {"message.", "对话"}, {"topic.", "对话"},
	{"memory.", "记忆"}, {"usage.", "用量"},
	{"task.", "任务"}, {"project.", "项目"}, {"persona.", "预设"},
	{"run.", "Run"}, {"consult.", "Run"},
	{"node.", "机器"}, {"agent.job.", "机器"},
	{"approval.", "审批"},
	{"schedule.", "定时"}, {"trigger.", "定时"},
	{"inspector.", "巡检"}, {"notify.", "推送"},
	{"models.", "模型"}, {"mcp.", "MCP"},
	{"note.", "记事本"}, {"meeting.", "会议"}, {"minutes.", "会议"},
}

// GroupOf returns the group of an event type, "" when none matches.
func GroupOf(eventType string) string

// ReadEdges maps an event type to the read lines it lights.
var ReadEdges = map[string][]string{
	"message.user":      {"eventlog>supervisor"},
	"trigger.fired":     {"eventlog>supervisor"},
	"message.moved":     {"eventlog>supervisor"},
	"node.repos":        {"eventlog>task"},
	"node.online":       {"eventlog>task"},
	"node.inventory":    {"eventlog>task"},
	"run.ended":         {"eventlog>task"},
	"task.created":      {"eventlog>task"},
	"project.put":       {"eventlog>task"},
	"task.status":       {"eventlog>task"},
	"approval.requested": {"eventlog>task"},
	"approval.decided":  {"eventlog>task"},
	"run.paused":        {"eventlog>task"},
	"run.resumed":       {"eventlog>task"},
	"schedule.put":      {"eventlog>trigger"},
}

// FrameDown and FrameUp list the Hub ↔ Node frame types by direction.
var FrameDown = []string{"welcome", "ack", "run.start", "run.message", "run.cancel", "run.pause", "run.resume",
	"approval.decide", "workspace.remove", "node.update", "node.request"}
var FrameUp = []string{"hello", "inventory", "ack", "run.started", "run.update", "run.output", "run.diff", "run.ended",
	"run.turn", "run.delivered", "run.ask", "approval.request", "node.response", "node.stream", "node.job_ended",
	"node.repos", "node.update_result"}
```

`models.` 和 `mcp.` 这两个前缀，执行时以 `internal/event` 里实际出现的事件为准。如果某个常量的值不以表中任何前缀开头，测试会失败（第 4 步），这时在表里**按这个规则补一行**：前缀取事件类型第一个点之前的部分加点，分组用对应模块在第 2 步表里的名字。

## 4. 分类的测试（`classify_test.go`）

两个测试，都用 `go/parser` 解析源码，不 import 那些包（避免循环引用）：

- `TestEveryEventGrouped`：解析 `../event/*.go`（跳过 `_test.go`），找出所有“类型是 `Type`、值是字符串字面量”的常量（`ast.ValueSpec` 里 `Type` 是 `Type` 的 ident，或者在同一个 `const (...)` 块里沿用上一个 spec 的类型），对每个值调用 `GroupOf`，结果为空就 `t.Errorf("event %s (%s) has no group in EventGroups", 名字, 值)`。
- `TestEveryFrameDirected`：解析 `../proto/*.go`，找出所有名字以 `T` 开头、第二个字符是大写字母、值是字符串字面量的常量，每个值必须出现在 `FrameDown` 或 `FrameUp` 里。缺少时报 `frame %s (%s) is in neither FrameDown nor FrameUp`。

测试失败时，按第 3 步的规则补表，**不要改测试**。

验证：`cd apps/core && go test ./internal/sysmap/`

## 5. 计数、记录和调用来源（`flow.go`、`refs.go`）

`refs.go`：

```go
type Refs struct {
	ThreadID string `json:"thread_id,omitempty"`
	TaskID   string `json:"task_id,omitempty"`
	RunID    string `json:"run_id,omitempty"`
}

type refsKey struct{}

func WithRefs(ctx context.Context, r Refs) context.Context
func RefsFrom(ctx context.Context) Refs // zero value when unset
```

`flow.go`：

```go
const (
	Window     = 900  // seconds of per-second counts kept
	MaxRecords = 5000 // flow records kept for traces and the live list
	callStats  = 20   // recent calls kept per line for latency and errors
)

type Record struct {
	TS      int64  `json:"ts"` // unix milliseconds
	Edge    string `json:"edge"`
	Refs    Refs   `json:"refs"`
	Summary string `json:"summary"`
}

type CallStat struct {
	Calls  int     `json:"calls"`
	Errors int     `json:"errors"`
	AvgMS  float64 `json:"avg_ms"`
	LastError string `json:"last_error,omitempty"`
}

// Count records one thing going along a line.
func Count(edge string, refs Refs, summary string)
// Call records one call along a line, with how long it took and whether it failed.
func Call(edge string, refs Refs, summary string, d time.Duration, err error)
// Counts returns the per-second counts of every line from since (unix
// seconds, inclusive) to now, oldest first; lines with no counts are left out.
func Counts(since int64) map[string][]uint32
// Sum is how many things went along a line in the last n seconds.
func Sum(edge string, n int) int
// Records returns the records after ts (unix ms), oldest first, at most limit.
func Records(after int64, limit int) []Record
// Stats returns the recent call statistics of a line.
func Stats(edge string) CallStat
```

实现要求：

- 一个包级的 `sync.Mutex` 保护全部状态。
- 每条连线一个长度 900 的 `[]uint32` 环形数组，下标是 `unix 秒 % 900`；另外记下每个下标对应的秒数，写入时如果秒数不同就先清零。
- 记录用长度 5000 的环形数组。`summary` 先用 `redact.Mask`（`internal/redact`；sysmap 可以 import 它，这是唯一的例外，因为 redact 不依赖别的 internal 包）打码，再截到 80 个字符（按 rune）。
- `Call` 等于 `Count` 加上更新这条连线最近 20 次调用的耗时和成败。
- `Reset()`（第 1 步）也要清空这些状态。
- 用 `time.Now()`，测试里通过包级变量 `var now = time.Now` 替换。

调用来源（同一个文件）：

```go
// Writer is the module whose code is calling, found from the call stack:
// the first frame in an internal package other than eventlog, store and
// sysmap. "" when none is known.
func Writer() string
```

实现：`runtime.Callers(2, pcs[:32])`，用 `runtime.CallersFrames` 遍历，取函数全名里 `/internal/` 后面的第一段作为包名；跳过 `eventlog`、`store`、`sysmap`。包名到模块的映射：

| 包 | 模块 |
|---|---|
| `httpapi`、`auth` | `httpapi` |
| `supervisor`、`tool`、`prompts` | `supervisor` |
| `task`、`router` | `task` |
| `trigger` | `trigger` |
| `inspector` | `inspector` |
| `nodehub` | `nodehub` |
| `notify` | `notify` |
| `mcp` | `mcp` |
| `models` | `models` |

遇到不在表里的包就返回 `""`。

测试 `flow_test.go`：

- `TestCountAndSum`：替换 `now`，在同一秒 `Count` 3 次，`Sum` 得 3；把时间推后 901 秒再 `Count` 1 次，`Sum(edge, 900)` 得 1。
- `TestRecordsRing`：写 5001 条，`Records(0, 10000)` 长度是 5000，第一条是第 2 条写入的。
- `TestCallStats`：3 次成功（10ms、20ms、30ms）、1 次失败（40ms，错误文字 `boom`），`Stats` 必须是 `{Calls: 4, Errors: 1, AvgMS: 25, LastError: "boom"}`。平均耗时按全部调用算，失败的也算。
- `TestSummaryRedacted`：`Count` 一条带 `sk-` 开头长字符串的摘要，记录里不包含原串。
- `TestWriter`：在测试里直接调用 `Writer()`，期望 `""`（测试包是 `sysmap`，不在映射表里）。

## 6. 推送循环（`stream.go`）

```go
// Tick is what the system map page gets every second.
type Tick struct {
	T       int64             `json:"t"` // unix seconds
	Edges   map[string]uint32 `json:"edges"`             // counts in second T
	Modules map[string]Status `json:"modules,omitempty"` // only the ones that changed
	Flows   []Record          `json:"flows,omitempty"`   // since the previous tick, at most 50
}

// Subscribe starts the ticker on the first subscriber; the returned stop
// ends the subscription, and the ticker with the last one.
func Subscribe() (<-chan Tick, func())
```

- 每个订阅者一个带 4 个缓冲的 channel；发送时缓冲满就跳过这一帧（不阻塞）。
- 定时器每秒一次：`T` 是上一整秒，`Edges` 是这一秒每条连线的计数（不为 0 的才放），`Flows` 是上一帧之后的记录（最多 50 条，多了只留最新的 50 条），`Modules` 是调用 `Snapshot(ctx)`（第 7 步）后和上一帧比较、序列化结果不同的那些模块。第一帧带全部模块。
- 最后一个订阅者退出时停止定时器，清掉“上一帧”的缓存。
- 定时间隔写成包级变量 `var tickEvery = time.Second`，测试里改成 10ms。

测试 `stream_test.go`：订阅后能收到第一帧且带全部模块；`Count` 一次后下一帧的 `Edges` 里有它；两个订阅者退出一个后另一个还能收到；全部退出后包级的“定时器在跑”标志是 false（加一个只给测试用的 `running() bool`）。

## 7. 快照（`snapshot.go`）

```go
type Graph struct {
	Modules []Module          `json:"modules"`
	Edges   []Edge            `json:"edges"`
	Status  map[string]Status `json:"status"`
	Now     int64             `json:"now"` // unix seconds
}

// Snapshot is the current graph with every status.
func Snapshot(ctx context.Context) Graph
```

步骤：

1. 从 `Modules` 和 `StaticEdges` 开始（复制，不改包级变量）。
2. 按 `provider`、`mcpserver`、`machine` 的顺序调用注册的实例函数，把每个实例的 `Module` 加进列表、`Status` 放进 map，并加上第 2 步动态连线表里的连线：
   - `provider:<名字>`：加 `models>provider:<名字>`；
   - `mcpserver:<slug>`：加 `mcp>mcpserver:<slug>`；
   - `machine:<id>`：加 `nodehub>machine:<id>` 和 `machine:<id>>nodehub`。
3. 对每个注册了探针的模块调用 `Probe`，每个探针用 `context.WithTimeout(ctx, 200*time.Millisecond)`；超时或 panic（用 `recover`）时这个模块的状态是 `{State: Degraded, Summary: "状态读取超时"}`。
4. 没有探针的模块状态是 `{State: Idle, Summary: ""}`。

测试 `snapshot_test.go`：注册一个假的 `machine` 实例和一个会 sleep 300ms 的探针，检查连线被展开、超时的那个是“状态读取超时”。

## 8. 插桩和探针（改现有代码）

每一处都只加几行，不改原有逻辑。改完跑全部 Go 测试。

### 8.1 `internal/eventlog/eventlog.go`

- `Write`：在 `l.Store.Write` 成功后，先 `w := sysmap.Writer()`，然后对每个 `ev`：
  - `w != ""` 时 `sysmap.Count(sysmap.EdgeID(w, "eventlog"), refsOf(ev), string(ev.Type))`；
  - 对 `sysmap.ReadEdges[string(ev.Type)]` 里的每条连线 `sysmap.Count(连线, refsOf(ev), string(ev.Type))`。
  - 记下这次写入里最大的 `ev.Seq`，存到包级 `atomic.Int64 lastSeq`（只增不减）。
  - 如果 `effects` 为真，`sysmap.Count("eventlog>effects", sysmap.Refs{}, "入队")`。
- `Emit`：同样按 `Writer()` 计写入的那条线（不计 `ReadEdges`）。
- `refsOf(ev)` 是新的小函数，返回 `sysmap.Refs{ThreadID: ev.ThreadID, TaskID: ev.TaskID, RunID: ev.RunID}`。
- 探针：在 `New` 里 `sysmap.RegisterProbe("eventlog", ...)`。`Summary` 是 `fmt.Sprintf("最新 #%d · 每分钟 %d 条", lastSeq, n)`，`n` 是全部 `*>eventlog` 连线最近 60 秒的 `Sum` 之和。`Gauges` 是 `[{Label: "事件/分钟", Value: n}]`。`n == 0` 时状态是 `Idle`，否则是 `OK`。

### 8.2 `internal/httpapi`

- `ServeHTTP`：在 `s.mux.ServeHTTP` 之后，路径以 `/api/v1/device/` 开头的计 `cli>httpapi`；以 `/api/` 开头、但不以 `/api/v1/system/` 开头的计 `ui>httpapi`。摘要是 `方法 + " " + r.URL.Path`，refs 为空。
- `stream`：每成功 `send` 一个事件，`allow == nil` 时计 `httpapi>ui`，否则计 `httpapi>cli`；摘要是事件类型，refs 用事件的三个 id。另外加两个包级 `atomic.Int64`：`pageConns`、`deviceConns`，`stream` 开始时 +1，`defer` -1。
- 探针（在 `New` 里注册三个）：
  - `httpapi`：`{OK, fmt.Sprintf("%d 个页面、%d 台设备连着", 页面数, 设备数), [{"连接", 页面数+设备数}]}`。
  - `ui`：页面数大于 0 时 `{OK, "N 个页面打开着"}`，否则 `{Idle, "没有打开的页面"}`。
  - `cli`：设备数大于 0 时 `{OK, "N 台设备连着"}`，否则 `{Idle, "没有命令行连着"}`。

### 8.3 `internal/models/models.go`

在 `roleProvider.Stream` 里调用 `res.Provider.Stream` 前后计时：

- 来源模块：角色 `supervisor`、`compactor` 是 `supervisor`；`reviewer`、`approver` 是 `task`；`inspector` 是 `inspector`；其他角色不计来源连线。
- `sysmap.Call(sysmap.EdgeID(来源, "models"), sysmap.RefsFrom(ctx), p.role+" · "+res.Model, 耗时, err)`；
- `sysmap.Call("models>provider:"+res.Name, sysmap.RefsFrom(ctx), p.role+" · "+res.Model, 耗时, err)`。
- 实例函数：`sysmap.RegisterInstances("provider", ...)`，在 `New`（或构造 `Registry` 的函数）里注册。列出所有已配置的供应商（用 `Registry` 现有的列表方法，名字以代码为准），每个生成 `Module{ID: "provider:"+名字, Name: 名字, Layer: LayerExternal, Desc: "模型供应商（" + kind + "）"}`；状态由这条连线的 `Stats` 算出：
  - `Calls == 0`：`{Idle, "最近没有调用"}`；
  - 最近的调用里失败数 ≥ 3：`{Degraded, fmt.Sprintf("最近 %d 次里失败 %d 次：%s", Calls, Errors, LastError)}`；
  - 其他：`{OK, fmt.Sprintf("平均 %.1f 秒 · 最近 %d 次", AvgMS/1000, Calls)}`。
- 探针 `models`：把所有供应商的 `Calls`、`Errors` 加起来；有任何供应商是 `Degraded` 时状态是 `Degraded`，最近 60 秒有调用时是 `Busy`，否则是 `Idle`；`Summary` 是 `fmt.Sprintf("最近 %d 次调用，失败 %d 次", 总数, 失败)`；`Items` 是每个供应商一行（`Label` 名字，`Detail` 同上面的 Summary，`State` 同上）。

### 8.4 设置 refs

- `internal/supervisor/supervisor.go` 的 `answerTopic` 里，`turnCtx` 返回前再包一层 `sysmap.WithRefs(..., sysmap.Refs{ThreadID: threadID})`。
- `internal/task` 里调用验收模型（`advance.go` 第 551 行附近的 `m.Provider.Stream`）和审批模型（`approver_loop.go` 第 66 行附近）前，`ctx = sysmap.WithRefs(ctx, sysmap.Refs{TaskID: 任务 id, RunID: Run id})`，id 用那里已有的变量；只有其中一个时另一个留空。

### 8.5 `internal/tool/tool.go`

`Registry.Call` 里，`safeCall` 前后计时，然后 `sysmap.Call(连线, sysmap.RefsFrom(ctx), c.Name, 耗时, err)`。连线按工具名决定（写成 `toolEdges(name string) []string`，放在 `tool` 包里）：

| 工具名 | 连线 |
|---|---|
| `task_` 开头、`project_` 开头、`git_` 开头、`consult`、`run_cancel`、`run_message` | `supervisor>task` |
| `schedule_` 开头 | `supervisor>trigger` |
| `web_search`、`web_fetch` | `supervisor>web`、`web>search` |
| `mcp_list`、`mcp_propose`、`mcp_withdraw` | `supervisor>mcp` |
| `mcp__<slug>__` 开头 | `supervisor>mcp`、`mcp>mcpserver:<slug>` |
| `machine_look` | `supervisor>nodehub` |
| 其他 | 不计 |

`<slug>` 是 `mcp__` 和下一个 `__` 之间的部分。给 `toolEdges` 写表驱动测试 `tool/edges_test.go`，表里每一行都要测到。

### 8.6 `internal/effects/effects.go`

`runOne` 开头：`kind` 是 `store.EffectPush` 时计 `effects>notify`，否则计 `effects>task`；摘要是 `e.Kind`。

探针 `effects`（在 `NewRunner` 里注册）：加一个 store 方法 `func (s *Store) PendingEffects(ctx) (int, error)`（`SELECT COUNT(*) FROM effects`，写在 `store/effects.go`），再用已有的 `FailingEffects(ctx, 1)` 看有没有失败多次的。结果缓存 2 秒（`sync.Mutex` + 上次时间）。

- 有失败多次的：`{Degraded, fmt.Sprintf("积压 %d 个，有失败多次的：%s", n, 第一条的 Kind)}`
- `n > 0`：`{Busy, fmt.Sprintf("积压 %d 个", n)}`
- 否则：`{Idle, "队列是空的"}`
- `Gauges`: `[{"积压", n}]`

### 8.7 `internal/notify`

找到真正发出 Web Push 请求的地方（`notify.go` 里调用 webpush 发送的函数），每发一个请求计 `notify>push`，摘要是通知的标题。探针 `notify`：最近 900 秒 `Sum("notify>push", 900)` 为 n，`{n>0 ? OK : Idle, fmt.Sprintf("最近 15 分钟发了 %d 条", n)}`。`push` 模块的探针也写在这里：同样的数字，`Summary` 是“浏览器推送服务”。

### 8.8 `internal/nodehub`

- `nodeConn.send`：计 `nodehub>machine:<c 的节点 id>`，摘要是 `f.T`，refs 从 `f` 的 payload 里取 `run_id`（`json.Unmarshal` 到 `struct{ RunID string \`json:"run_id"\` }`，失败就留空）。
- `nodeConn.handle` 开头：计 `machine:<节点 id>>nodehub`，同样的摘要和 refs。
- 实例函数 `machine`（在 `nodehub.New` 里注册）：从 store 读机器列表（现有的读机器列表的方法），跳过已吊销的；每台机器：
  - `Module{ID: "machine:"+id, Name: 机器名, Layer: LayerNode, Desc: 角色是 server 时 "服务器" 否则 "开发机"}`；
  - 正在跑的 Run：从 store 读这台机器 `status` 是 `running` 的 Run（用现有的按机器和状态列 Run 的方法）；
  - 不在线：`{Down, "离线 · 最后在线 " + 相对时间}`，相对时间格式“3 分钟前 / 2 小时前 / 5 天前”（写一个小函数 `ago(t time.Time) string`）；
  - 在线有 Run：`{Busy, fmt.Sprintf("在跑 %d 个 · 工位 %d/%d", 在跑数, 占用, 并发上限)}`；占用按 nodehub 现有的 `Busy` 计算（git Run 和等用户审批的 Run 不算），并发上限是机器的 `Concurrency`；
  - 在线没有 Run：`{OK, fmt.Sprintf("空闲 · 工位 0/%d", 并发上限)}`；
  - `Items`：每个在跑的 Run 一行，`Label` 是 agent 名，`Detail` 是 Run 的标题（没有就用用途），`State` 是 `Busy`，`Link` 是 `#/task=<任务 id>`（没有任务就不填）。
  - 结果缓存 2 秒。
- 探针 `nodehub`：`{在线数>0 ? OK : Down, fmt.Sprintf("在线 %d/%d 台 · 工位 %d/%d", ...)}`，工位是所有在线机器之和。

### 8.9 `internal/supervisor`

- 在 `Supervisor` 里加 `active map[string]*activity`（由 `s.mu` 保护），`activity{Topic string; Step int; Tool string; Since time.Time; Compacting bool}`。
  - `answerTopic` 开始回合时写入、`defer` 删除；每一步循环开头更新 `Step`；执行工具前把 `Tool` 设成工具名（多个时用“、”连起来），执行完清空。
  - `compact` 开始时写入 `Compacting: true`，结束时删除（如果本来就有回合记录，只改这个字段）。
- 探针 `supervisor`（在 `New` 里注册）：
  - 有 activity：`{Busy, fmt.Sprintf("%d 个对话在处理", n)}`，否则 `{Idle, "空闲"}`；
  - `Gauges`：`[{"处理中", n}, {"等待中", len(s.batches)+len(s.gathers)}]`；
  - `Items`：每个 activity 一行，`ID` 是线程 id，`Label` 留空（页面按线程 id 显示对话名），`Detail` 是 `整理上下文` 或 `fmt.Sprintf("第 %d 步%s · %d 秒", Step+1, 工具部分, 秒数)`，工具部分是 `" · 调用 " + Tool`（没有工具时为空），`State` 是 `Busy`，`Link` 是 `#/thread=<线程 id>`。

### 8.10 `internal/task`

探针 `task`（在 `New` 或 `Start` 里注册，以引擎能拿到 store 的地方为准）：用 store 按状态统计任务数（`running`、`queued`、`waiting_approval`、`blocked`），缓存 2 秒。

- `Summary`：`fmt.Sprintf("执行中 %d · 排队 %d · 等审批 %d · 受阻 %d", ...)`
- 状态：执行中 > 0 是 `Busy`；受阻 > 0 是 `Degraded`（受阻优先于执行中）；全部为 0 是 `Idle`。
- `Gauges`：执行中、排队。

### 8.11 `internal/trigger`

探针 `trigger`：读 `Schedules`，只看 `Enabled` 的，用现有的 `NextRun` 找最早的一个。缓存 2 秒。

- 没有：`{Idle, "没有启用的定时任务"}`
- 有：`{OK, fmt.Sprintf("%d 个定时任务 · 下一个 %s「%s」", 数量, 时间, 标题)}`，时间格式 `01-02 15:04`（Hub 本地时区）。

### 8.12 `internal/inspector`

在 `Inspector` 里加 `lastRun time.Time` 和 `lastFound int`（`sync.Mutex` 保护），在每次巡检（`Inspect`）结束时写入。探针 `inspector`：

- 从没跑过：`{Idle, "还没有巡检过"}`
- `lastFound > 0`：`{Degraded, fmt.Sprintf("%s巡检，发现 %d 个问题", ago, n)}`
- 否则：`{OK, fmt.Sprintf("%s巡检，没有问题", ago)}`，`ago` 是“3 分钟前”这样的文字（`ago` 函数放在 sysmap 里导出成 `sysmap.Ago`，8.8 也用它）。

### 8.13 `internal/mcp`

- 实例函数 `mcpserver`：每个配置的服务一个 `Module{ID: "mcpserver:"+Slug(名字), Name: 名字, Layer: LayerExternal, Desc: "MCP 服务（" + transport + "）"}`；状态：`ready` → `OK`“已连接，N 个工具”；`connecting` → `Busy`“正在连接”；`error` → `Degraded` + 错误的第一行；`off` → `Idle`“未启用或等你批准”。
- 探针 `mcp`：`{有 error ? Degraded : (有 ready ? OK : Idle), fmt.Sprintf("%d 个已连接，%d 个出错", ...)}`。

### 8.14 `internal/hub/hub.go`

- 探针 `web`：`hub.yaml` 配了搜索引擎时 `{OK, "搜索：" + 引擎名}`，没配时 `{Idle, "没有配置搜索引擎，只能抓取网页"}`；`search` 模块的探针：最近 60 秒 `Sum("web>search", 60)` 大于 0 是 `Busy`，否则 `Idle`，`Summary` 是“最近 15 分钟 N 次”。
- 在 `Open` 开头调用 `sysmap.Reset()`（同一个进程里重新打开 Hub 的测试不会留下旧的注册）。

验证：`cd apps/core && gofmt -l . && go vet ./... && go test ./...`

## 9. 接口（`internal/sysmap/routes.go` + 注册）

```go
// Routes adds the system map endpoints.
func Routes(mux *http.ServeMux, protect func(string, http.HandlerFunc), origins []string)
```

- `GET /api/v1/system/graph` → `Snapshot(ctx)` 的 JSON。
- `GET /api/v1/system/history?since=<unix 秒>` → `{"counts": Counts(since), "records": Records(since*1000, 5000), "now": 现在}`；`since` 缺省或早于 900 秒前时取 900 秒前。
- `GET /api/v1/system/trace?task_id=|run_id=|thread_id=`（只能给一个，否则 400 `bad_request`）→ `{"steps": [...], "pending": "<模块 id 或空>"}`：
  - 从 `Records(0, 5000)` 里挑出 refs 匹配的记录（`task_id` 匹配记录的 `TaskID`，`run_id` 匹配 `RunID`，`thread_id` 匹配 `ThreadID`）；
  - 对 `task_id`，再加上 refs 里 `RunID` 属于这个任务的记录：由调用方传进来的函数 `runsOf func(ctx, taskID) []string` 提供（在 hub 里用 store 实现）；
  - `steps` 是 `[{ts, edge, summary}]`，按时间排序，最多 300 条（多了留最新的）；
  - `pending`：任务是 `running` / `queued` / `waiting_approval`，或者 Run 是 `running`，或者线程有 supervisor activity 时，取最后一步连线的 `To`；否则为空。状态由调用方传进来的函数 `pendingOf func(ctx, kind, id) bool` 提供。
  - 所以 `Routes` 的签名改成带一个 `TraceDeps{RunsOf, PendingOf}` 参数。
- `GET /api/v1/system/stream` → websocket：`websocket.Accept`，`OriginPatterns` 用调用方传进来的 `origins`（照 `httpapi/ws.go` 里的算法：`public_url` 的 host，开发模式加 `localhost:*`、`127.0.0.1:*`；在 hub 里算好传进来）。`conn.CloseRead`，`Subscribe()`，每个 `Tick` 序列化成 `{"type":"sys.tick","payload":<Tick>}` 发出；写超时 10 秒；每 30 秒 ping。
- 全部用 `protect`，设备令牌访问不了（`protect` 只认登录会话）。
- 在 `hub.go` 里组装 `httpapi.New` 时，把它包成一个 `httpapi.Routes` 加进 `extra`。

测试 `internal/sysmap/routes_test.go`（不依赖 hub）：用 `httptest`，`protect` 传一个直接放行的函数，测 graph 返回 16 个静态模块；trace 同时给两个参数返回 400；先 `Count` 几条带 `TaskID` 的记录再查 trace，结果按时间排好。

再在 `internal/httpapi/httpapi_test.go` 的 `TestUnauthenticatedRequestsAreRejected` 里加上 `/api/v1/system/graph`（要求 401）。如果那个测试的环境没有装上 sysmap 的路由，就照 `newEnv` 的写法把 `sysmap.Routes` 加进 `extra`。

验证：`cd apps/core && go test ./internal/sysmap/ ./internal/httpapi/ ./internal/hub/`

## 10. 契约文档

在 `docs/api/core-hub-http.md` 的接口表里加四行（放在 `/usage` 那一行后面），照第 9 步的说明写，并说明 `sys.tick` 的格式和“不进事件日志、设备令牌拿不到”。

## 11. web：数据层

1. 在 `apps/web` 里运行 `bun add @xyflow/react@12.12.0 --exact`。
2. `src/lib/types.ts` 末尾加类型，和 Go 的 JSON 一一对应：`SysLayer`、`SysState`、`SysModule`、`SysEdge`、`SysGauge`、`SysItem`、`SysStatus`、`SysGraph`、`SysRecord`、`SysTick`、`SysHistory`、`SysTrace`（`{steps: {ts: number; edge: string; summary: string}[]; pending: string}`）。
3. `src/lib/api.ts` 加：`systemGraph()`、`systemHistory(since?: number)`、`systemTrace(kind: 'task' | 'run' | 'thread', id: string)`，都用现有的 `request`。
4. 新文件 `src/lib/sysmap.ts`（纯函数，全部要有测试 `src/lib/sysmap.test.ts`）：

```ts
export const STATE_TEXT: Record<SysState, string> = { ok: '正常', busy: '忙', idle: '空闲', degraded: '降级', down: '停止' }

/** Per-line counts of the last 900 seconds, keyed by unix second. */
export interface Rates { counts: Record<string, Record<number, number>>; latest: number }

export function emptyRates(): Rates
export function applyHistory(r: Rates, h: SysHistory): Rates
export function applyTick(r: Rates, t: SysTick): Rates   // also drops seconds older than t.t - 900
/** Sum of a line over the n seconds ending at `at` (inclusive). */
export function sumAt(r: Rates, edge: string, at: number, n: number): number
/** 0..1 for the animation: 0 when no flow in the last 5 s, 1 at 10 or more per second. */
export function intensity(r: Rates, edge: string, at: number): number  // = min(1, sumAt(…, 5) / 50)
/** Fixed positions, see the layout table. */
export function layout(g: SysGraph): Record<string, { x: number; y: number }>
/** Lines and modules a trace touches. */
export function traceSets(t: SysTrace, g: SysGraph): { edges: Set<string>; modules: Set<string> }
```

`layout` 的固定坐标（单位 px，节点宽 208、高 96）：

| 模块 | x | y |
|---|---|---|
| ui | 0 | 40 |
| cli | 0 | 200 |
| httpapi | 300 | 120 |
| eventlog | 600 | 120 |
| effects | 600 | 300 |
| notify | 600 | 480 |
| supervisor | 900 | 0 |
| task | 900 | 160 |
| trigger | 900 | 320 |
| inspector | 900 | 480 |
| models | 1200 | 0 |
| mcp | 1200 | 160 |
| web | 1200 | 320 |
| nodehub | 1200 | 480 |
| push | 900 | 660 |

动态模块放在 x = 1500 这一列，从上往下依次是：全部 `provider:*`（按名字排序）、全部 `mcpserver:*`（按名字排序）、`search`，每个占 130px，从 y = 0 开始；然后是全部 `machine:*`（按名字排序），从 `max(480, 上一个的 y + 170)` 开始，每个占 170px（机器节点高 140）。

测试至少覆盖：`applyTick` 丢掉过旧的秒；`sumAt` 跨秒求和；`intensity` 的 0、中间值、封顶；`layout` 里静态模块坐标和上表一致、两个供应商 + 一台机器的位置正确；`traceSets` 从步骤里收集连线和两端的模块。

## 12. web：页面

### 12.1 路由和入口

- `src/lib/route.ts`：`View` 加 `'system'`；`parseRoute` 认 `#/system`，也认 `#/system?trace=<kind>:<id>`（返回 `{view: 'system', id: '<kind>:<id>'}`）；`routeHash` 对 `system` 返回 `#/system`，`id` 不空时返回 `#/system?trace=<id>`。在 `route.test.ts` 加对应测试。
- `src/components/Icon.tsx` 的映射表加 `system: HierarchySquare02Icon`（Hugeicons 免费包里有这个图标）。`filter`、`play`、`pause`、`x`、`alert` 已经在映射表里。
- `Shell.tsx`：图标栏里“打开对话”那个按钮下面加一个 `RailButton`，`tip="系统"`，`active` 为 `view === 'system'`，点击 `go({ view: 'system', id: '' })`，图标 `system`，`size={20}`。`main.work` 里：`view === 'system'` 时渲染 `<Suspense fallback={null}><SystemMap trace={uiState.route.id} /></Suspense>`，否则照旧渲染 `Workbench`。`SystemMap` 用 `lazy(() => import('./SystemMap'))`。工作台那个按钮的 `active` 改成 `view !== 'system'`。
- `vite.config.ts` 的 `manualChunks`：把 `@xyflow/react` 打进单独的 chunk `flow`（照 `motion` 的写法）。

### 12.2 `src/components/SystemMap.tsx`（桌面）

结构（从上到下）：

1. 标题行，高 54px，左边是 `系统`（`text-[17px] font-semibold`），右边依次是：
   - “实时 / 暂停”切换：`.icon-btn`，实时时图标 `pause`、`aria-label="暂停"`、`data-tip="暂停实时更新"`；暂停时图标 `play`、`aria-label="继续"`、`data-tip="回到实时"`；
   - “只看有流量的”：`.icon-btn`，图标 `filter`，`aria-pressed` 表示开关，`data-tip="只看最近 1 分钟有流量的连线"`；打开时，最近 60 秒 `sumAt` 为 0 的连线不画，两端都没有剩下连线的模块变成 `opacity: 0.25`；
   - 追踪中时多一个胶囊 `.pill`：`追踪：任务 / Run / 对话`，加一个 × 按钮（`aria-label="结束追踪"`），点击 `go({view: 'system', id: ''})`。
2. 图：占满剩下的高度。用 `<ReactFlow>`，`nodes` 来自 `graph.modules` + `layout`，`edges` 来自 `graph.edges`；`nodesDraggable={false}`、`nodesConnectable={false}`、`elementsSelectable` 为真、`fitView`、`minZoom={0.3}`、`maxZoom={1.5}`、`proOptions={{ hideAttribution: true }}`；加 `<Background variant="dots" gap={24} size={1} />` 和 `<Controls showInteractive={false} />`。在组件文件顶部 `import '@xyflow/react/dist/style.css'`。
3. 时间轴：暂停时显示在图的底部，高 44px。一个 `input type="range"`，`min` 是 900 秒前，`max` 是暂停时的那一秒，步长 1；左边文字是选中时刻（`HH:mm:ss`），右边是“回到实时”按钮（`.btn small`）。拖动时图上的流量和悬停数字都按选中的那一秒计算（`intensity(rates, edge, at)`）。
4. 右侧详情面板：选中模块或连线时出现，宽 360px，从右边滑入（CSS 类 `.sheet` 的桌面样式不合适，用一个普通的 `aside`，`bg-panel shadow-pop`，`transition: transform 160ms`）。关闭按钮 `.icon-btn`，`aria-label="关闭详情"`。

数据流程：

- 挂载时：`api.systemGraph()` → `graph`；`api.systemHistory()` → `applyHistory`；然后打开 websocket `/api/v1/system/stream`（协议照 `stream.ts`：`https` 页面用 `wss:`），每收到一帧 `applyTick`，并用 `tick.modules` 替换 `graph.status` 里对应的项。断线后 2 秒重连，重连成功后重新拉一次 `systemHistory`。
- 每 30 秒重新拉一次 `systemGraph()`（机器、供应商、MCP 服务可能有增减），保留 `rates`。
- 卸载时关闭 websocket。
- 暂停时继续接收 tick（数据照样累积），但图只按时间轴选中的那一秒显示。
- 追踪（`trace` 不空时）：解析出 `kind` 和 `id`，调用 `api.systemTrace`，每 3 秒重拉一次；用 `traceSets` 得到要高亮的连线和模块，其余的节点和连线设成 `opacity: 0.25`。

节点组件 `ModuleNode`（自定义 node type `module`）：

- 卡片：宽 208、高 96（机器 140），`card` 类，内边距 12px，圆角 `--r-md`。
- 第一行：状态点 + 名字（`text-[14px] font-semibold`，单行省略）+ 右侧状态字（`text-[12px]`）。状态点 8px 圆：`ok` 是 `bg-ink`；`busy` 是 `bg-accent` 加现有的 `live-dot` 类（闪烁）；`idle` 是 1.5px `border-faint` 的空心圆；`degraded` 是 `bg-ink` 加外圈 2px `ring` 同色、状态字加粗；`down` 是 `Icon name="x"` 12px `text-faint`，名字也变成 `text-faint`。
- 第二行：`summary`，`text-[12px] text-muted`，最多两行（`line-clamp-2`）。
- 第三行（有 `gauges` 时）：最多两个，`标签 数值`，`text-[12px] tabular-nums`。
- 追踪时 `pending` 那个节点加 `outline: 2px solid var(--accent)` 和 `live-dot` 动画。
- 选中时 `shadow-pop`。
- 节点的 Handle：左边一个 target、右边一个 source，都不可见（`style={{ opacity: 0 }}`）。

连线组件 `FlowEdge`（自定义 edge type `flow`）：

- 用 `getBezierPath` 画路径。底线：`stroke: var(--line)`，`strokeWidth: 1.5`。
- `intensity > 0` 时，上面再画一条同样路径的流动线：`stroke: var(--accent)`，`strokeWidth: 1.5 + 1.5 * intensity`，`strokeDasharray: "6 10"`，`opacity: 0.35 + 0.65 * intensity`，用 CSS 动画让 `stroke-dashoffset` 从 16 变到 0，周期 `max(0.4, 1.6 - 1.2 * intensity)` 秒，`linear infinite`。在 `app.css` 加关键帧 `@keyframes flow-dash { to { stroke-dashoffset: 0 } }`，起点由内联 `strokeDashoffset: 16` 给出。系统开了“减少动态效果”（`useReducedMotion()`）时不放动画，只按 `intensity` 改粗细和不透明度。
- 悬停（`EdgeLabelRenderer` 在路径中点放一个透明的 24px 热区）时显示小浮层（`.card text-[12px] p-2`）：`label`、`最近 1 分钟 N 次`、最近 3 条记录的摘要（从实时流量记录里按连线过滤）。浮层用 `PopoverContent`（照 web 的浮层约定），`side="top"`。
- 追踪时属于路径的连线始终画流动线（`intensity` 至少按 0.5 算）。

详情面板：

- 模块：名字、状态（点 + 文字）、`desc`、`code`（等宽字体，`text-faint`）；`summary`；`gauges`（每个一行：标签、数值、单位）；`items` 列表（每行：状态点、`label`（为空时：如果 `ID` 是某个对话的 id，就用 `chat.threads` 里的标题；否则显示 `ID` 的前 8 位）、`detail`；有 `link` 时整行是按钮，点击 `location.hash = link`）；“近 15 分钟”走势：所有以这个模块为端点的连线每 30 秒一个柱，SVG 画，宽 312、高 48，柱用 `fill: var(--muted)`，不画坐标轴，下面写“15 分钟前”和“现在”；“流量记录”：以这个模块为端点的最近 50 条记录，最新的在上，每行 `HH:mm:ss · 连线的 label · summary`，记录里有 `task_id` 时行尾加“任务”链接（`#/task=<id>`）。
- 连线：`From → To` 的名字、`label`、`carries`；近 15 分钟走势（只这一条）；最近 50 条记录。
- 追踪时面板默认打开，显示“追踪”页：每一步一行 `HH:mm:ss · From 名 → To 名 · summary`，最后一行如果有 `pending` 写“正在 <模块名> 处理”。

空和错误：

- `systemGraph` 失败：页面中间显示 `Icon name="alert"` 和“读不到系统状态：<错误信息>”，加“重试”按钮（`.btn`）。
- websocket 连不上：标题行左边名字后面显示 `.pill`“未连接，正在重试”。

### 12.3 入口：追踪

- `TaskView.tsx` 标题行右侧的图标按钮里加一个：`Icon name="system"`，`aria-label="在架构图里追踪"`，`data-tip="在架构图里追踪"`，点击 `go({ view: 'system', id: 'task:' + task.id })`。
- `RunSheet.tsx` 标题行同样加一个，`id` 是 `'run:' + run.id`。
- `ChatPanel.tsx` 的 `actions` 里同样加一个，`id` 是 `'thread:' + 当前线程 id`，没有线程时不显示。

### 12.4 手机

- `SettingsModal.tsx` 的分组表加一项 `{ key: 'system', text: '系统状态', icon: 'system', group: 1 }`（放在“用量”后面），页面内容是 `SystemList`。
- 新组件 `src/components/SystemList.tsx`：桌面和手机共用的数据逻辑抽成 hook `useSystemData(trace?: string)`，放在 `src/lib/sysmap-data.ts`（SystemMap 也用它）。`SystemList` 按层分组：客户端、Hub、外部服务、机器，组标题用 `text-[13px] text-muted`；每个模块一行（最小高 64px）：状态点、名字、`summary`（单行省略）、右侧状态字；点击推入详情（复用 12.2 的详情面板内容，抽成组件 `SystemDetail`）。手机上 `#/system` 路由打开设置的 `system` 页。
- 桌面上设置里也会有这一项，内容同样是 `SystemList`，并在顶部放一个链接按钮“打开架构图”（`go({view: 'system', id: ''})` 并关闭设置）。

验证：`cd apps/web && bun run check && bun run test && bun run build`

## 13. 文档

- `docs/architecture/system-map.md`（新建）：一节“为什么是星形”（第 1 节的原因），一张 mermaid 图（静态模块和连线，照第 2 步的表），一张连线表（ID、含义），一节“状态从哪来”（每个模块一行，照第 8 步）。最后写“实时的图在页面左侧的“系统”里”。
- `docs/architecture/README.md` 的列表里加这一篇。
- `apps/core/AGENTS.md` 的 Conventions 末尾加一条：sysmap 的用法（新事件类型要在 `EventGroups` 里有分组、新帧要在 `FrameDown` / `FrameUp` 里，否则测试失败；新模块要在 `Modules` 里加一行并注册探针；探针不能慢）。
- `apps/web/AGENTS.md` 的 Stack 加 `@xyflow/react`（只在系统页用，`lazy()` 加载，`flow` chunk），Conventions 加一条：系统页的文件和 `sysmap.ts` 的纯函数，以及颜色约定（状态不用彩色）。
- 本目录 `tasks.md`：做完一项勾一项。

## 14. 端到端验收

照用户的习惯，用一份独立的 Hub 做浏览器验收，做完清理：

1. 在 `work/tmp/system-map/` 里：复制 `apps/core/.data/hub.db`（不存在就让 Hub 新建），写一份 `hub.yaml`：`listen: 127.0.0.1:8797`、`public_url: http://localhost:8797`、`data_dir` 指向这个目录、`web_dir` 指向 `build/web`、`dev: true`，`roles` 全部用 `mock/echo`（验收和审批用 `mock/verdict`）。
2. `wok build web`，然后在 `apps/core` 里 `go run ./cmd/sous-hub -config ../../work/tmp/system-map/hub.yaml`（用后台方式运行）。
3. 用 `apps/core/AGENTS.md` 里说的假 agent 和 `--home work/tmp/system-map/node` 配对并启动一个 daemon。
4. 在内置浏览器打开 `http://localhost:8797`，用 dev-login 登录，检查：
   - 图标栏有“系统”，点开后 16 个静态模块、供应商 `mock`、一台机器都在第 11 步的位置；
   - 在对话里发一条消息：`ui>httpapi`、`httpapi>eventlog`、`eventlog>supervisor`、`supervisor>models`、`models>provider:mock`、`supervisor>eventlog`、`httpapi>ui` 依次出现流动；
   - 建一个任务并派发：`nodehub>machine:*` 和 `machine:*>nodehub` 有流动，机器节点显示“在跑 1 个”；
   - 打开任务详情点“在架构图里追踪”，路径高亮，详情面板列出步骤；
   - 暂停后拖动时间轴，流量随时间变化；“回到实时”恢复；
   - 深色模式下颜色正常，没有出现红色或琥珀色；
   - 手机尺寸（375×812）下设置里的“系统状态”列表正常；
   - 离开系统页再回来，Hub 日志里没有错误，回来后能正常收到 tick（离开期间 Hub 不推送，由第 6 步的单测保证：最后一个订阅者退出后定时器停止，给 `stream_test.go` 加这个测试）。
5. 截图保存在 `work/tmp/system-map/` 里交给指派你的人看，然后停止 Hub 和 daemon，删除整个 `work/tmp/system-map/`。

最后在仓库根目录运行 `wok check`，修掉自己造成的问题。
