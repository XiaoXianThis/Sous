# 执行说明：记事本、会议、直接和子 agent 对话

给执行的 agent：按顺序做三批（A 记事本 → B 会议 → C 私聊），每一步做完跑这一步写的验证命令，全部通过再做下一步。本文已经定下所有命名、文案和行为；**不要自己发挥**。遇到本文没写到、而且代码里也找不到唯一答案的情况，停下来，把问题写清楚交回给指派你的人，不要猜。

## 0. 通用规则

- 先读：根目录 `AGENTS.md`、`apps/core/AGENTS.md`、`apps/web/AGENTS.md`、本目录的 `requirements.md` 和 `design.md`、`docs/architecture/supervisor-context.md`、`docs/decisions/0027-chat-topics.md`。
- Go 模块路径 `github.com/XiaoXianThis/Sous/apps/core`，代码在 `apps/core/internal/`；web 在 `apps/web/src/`。
- Go 验证：在 `apps/core/` 里 `gofmt -l .`（没有输出）、`go vet ./...`、`go test ./...`。web 验证：在 `apps/web/` 里 `bun run check`、`bun run test`。
- 只能通过 `eventlog.Log` 写数据（`apps/core/AGENTS.md` 的“唯一写入者”）。迁移只能在 `store/migrate.go` 的 `migrations` 末尾追加，不能改已有的。
- 新的事件类型不加进 `httpapi.deviceEvent`（设备令牌拿不到），除非本文明确要求。
- 如果系统架构图（`work/specs/system-map`）已经做完，新事件类型的前缀 `note.`、`meeting.`、`minutes.` 已经在 `sysmap.EventGroups` 里；没做完就不用管。
- 注释和标识符用英文，界面文字用中文，文案照本文原样抄。
- 不提交，除非指派你的人要求。每一批做完，勾掉 `tasks.md` 里对应的项。

---

# A. 记事本

## A1. 数据

`store/migrate.go` 末尾追加一个迁移：

```sql
CREATE TABLE notes (
  id         TEXT PRIMARY KEY,
  text       TEXT NOT NULL,
  project_id TEXT NOT NULL DEFAULT '',
  status     TEXT NOT NULL DEFAULT 'open',
  attachments TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX notes_status ON notes(status, updated_at);
CREATE TABLE note_sends (
  note_id    TEXT NOT NULL,
  thread_id  TEXT NOT NULL,
  topic_id   TEXT NOT NULL,
  message_id TEXT NOT NULL,
  at         INTEGER NOT NULL
);
CREATE INDEX note_sends_note ON note_sends(note_id);
```

`attachments` 存 `[]event.Attachment` 的 JSON（和 `messages.attachments` 一样）。

新文件 `event/note.go`：

```go
const (
	NotePut     Type = "note.put"
	NoteDeleted Type = "note.deleted"
	NoteSent    Type = "note.sent"
)

const (
	NoteOpen     = "open"
	NoteSentDone = "sent"
	NoteArchived = "archived"
)

type NotePayload struct {
	NoteID      string       `json:"note_id"`
	Text        *string      `json:"text,omitempty"`
	ProjectID   *string      `json:"project_id,omitempty"`
	Status      string       `json:"status,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

type NoteSentPayload struct {
	NoteIDs   []string `json:"note_ids"`
	ThreadID  string   `json:"thread_id"`
	TopicID   string   `json:"topic_id"`
	MessageID string   `json:"message_id"`
}
```

`event/workbench.go` 的 `MessageContext` 加字段 `NoteIDs []string \`json:"note_ids,omitempty"\``，`Empty()` 里也要检查它（`len(c.NoteIDs) == 0`）。

投影（新文件 `store/notes.go`，在 `store/project.go` 的 `switch` 里加 `case event.NotePut, event.NoteDeleted, event.NoteSent: return projectNote(w, ev)`）：

- `note.put`：行不存在时 `INSERT`（`text` 为空字符串时用 `""`，`status` 为空时用 `open`，`created_at = updated_at = ev.TS`）；存在时只更新 payload 里给出的字段（指针非空的 `text`、`project_id`，非空的 `status`），并更新 `updated_at`。`attachments` 只在新建时写。
- `note.deleted`：`DELETE FROM notes WHERE id = ?` 和 `DELETE FROM note_sends WHERE note_id = ?`。
- `note.sent`：对每个 id 插入一行 `note_sends`，并 `UPDATE notes SET status = 'sent', updated_at = ? WHERE id = ? AND status = 'open'`。

读：

```go
type Note struct {
	ID          string             `json:"id"`
	Text        string             `json:"text"`
	ProjectID   string             `json:"project_id,omitempty"`
	Status      string             `json:"status"`
	Attachments []event.Attachment `json:"attachments,omitempty"`
	Sends       []NoteSend         `json:"sends,omitempty"`
	CreatedAt   int64              `json:"created_at"`
	UpdatedAt   int64              `json:"updated_at"`
}
type NoteSend struct {
	ThreadID  string `json:"thread_id"`
	TopicID   string `json:"topic_id"`
	MessageID string `json:"message_id"`
	At        int64  `json:"at"`
}
type NoteFilter struct{ Status, ProjectID, Query string }

func (s *Store) Notes(ctx context.Context, f NoteFilter) ([]Note, error) // 按 updated_at 倒序，最多 500 条；Query 是对 text 的 LIKE 子串匹配（转义 % 和 _）
func (s *Store) Note(ctx context.Context, id string) (Note, error)       // 不存在返回 ErrNotFound
```

单测 `store/notes_test.go`：新建、改文字、改状态、发送两次（`Sends` 两条、状态 `sent`）、删除后 `Note` 返回 `ErrNotFound`、`Query` 能匹配中文子串。

## A2. 接口

新文件 `httpapi/notes.go`，在 `httpapi.New` 里 `s.memoryRoutes(protect)` 后面调用 `s.noteRoutes(protect)`：

- `GET /api/v1/notes?status=&project_id=&q=` → `{"notes": [...]}`
- `POST /api/v1/notes {text, project_id?, attachments?}` → 201 和这条笔记。`text` 去掉首尾空白后为空且没有附件时 400 `empty`（“笔记不能为空”）；超过 20000 字 400 `too_long`；附件用 `s.files.Resolve`，失败 400 `bad_attachment`。
- `PATCH /api/v1/notes/{id} {text?, project_id?, status?}` → 这条笔记。`status` 只能是 `open`、`sent`、`archived`，否则 400 `bad_status`。不存在 404。
- `DELETE /api/v1/notes/{id}` → 204。不存在 404。

发送（改 `postMessage`）：

- `body.Context.NoteIDs` 非空时：去重、最多 20 个；每个都要存在，否则 404 `not_found`（“笔记不存在”）。
- 把原来的 `s.log.Append(... MessageUser ...)` 改成 `s.log.Write(ctx, func(w *store.Writer) error { ... })`，在同一个事务里先追加 `message.user`，再追加 `note.sent`（`ThreadID` 是这个线程，`TopicID` 是最终的话题 id：`body.TopicID` 为空时就是 `msgID`，`MessageID` 是 `msgID`）。

主管：找到 `DescribeContext` 的实现（`grep -rn "DescribeContext" apps/core/internal`），确认它不读 `NoteIDs`；如果它在 context 只有 `NoteIDs` 时会写出东西，就改成忽略。加一个单测：只带 `note_ids` 的消息，给模型的文字里不出现 note id。

设备：`nodehub/device.go` 的 `DeviceRoutes` 加 `POST /api/v1/device/notes {text}` → 201 `{note_id}`，同样的校验，追加 `note.put`（`Actor: event.ActorUser`）。只能新建。

单测 `httpapi/notes_test.go`：增删改查往返、`q` 搜索、带 `note_ids` 发消息后笔记变成 `sent` 且 `sends` 指向这条消息、不存在的 note id 返回 404、未登录 401；`/ws` 推送 `note.put` 给登录会话；`deviceEvent("note.put")` 是 false。

## A3. 命令行

- `node/ipcserver.go` 加 IPC 方法 `note.add`，参数 `{text}`：`d.hub(ctx, "POST", "/notes", map[string]string{"text": p.Text}, &out)`（`d.hub` 的路径是相对于设备接口前缀的，照 `chat.send` 的写法），返回 `{note_id}`。参数结构里加 `Text string \`json:"text"\``。
- `cmd/sous/main.go` 加子命令 `note`：`sous note <文字...>`（把参数用空格拼起来；没有参数时从标准输入读到 EOF）。照 `chat` 子命令连接 daemon 的方式 `ipc.Dial`，调用 `note.add`，成功打印 `已记下（<note_id 前 8 位>）`，失败打印错误并以 1 退出。帮助文字：`note <文字>    记一条笔记到记事本`。
- 文档：`docs/api/core-daemon-ipc.md` 加 `note.add` 一行。

## A4. web

类型和 api：

- `src/lib/types.ts`：`Note`、`NoteSend`（和 Go 的 JSON 对应）；`MessageContext` 加 `note_ids?: string[]`。
- `src/lib/api.ts`：`notes(params?: {status?: string; project_id?: string; q?: string})`、`createNote(text, projectId?, attachments?)`、`updateNote(id, patch)`、`deleteNote(id)`。

状态：

- `src/lib/app.ts` 的 `chat` store 加 `notesRev: number`；`reduce.ts` 收到 `note.put` / `note.deleted` / `note.sent` 时 `notesRev++`（加测试）。
- `ui` store 加 `notesOpen: boolean`、`noteSend: string[]`（正在“发给…”的笔记 id，空数组表示没有）。

组件 `src/components/NotesDrawer.tsx`（`lazy()` 加载）：

- 用 `dialog.tsx` 的 `Dialog` + `DialogContent`，挂载即打开、卸载即关闭；桌面上贴右边，宽 `min(400px, 100vw)`、全高，`aria-label="记事本"`；手机上是标准的 `.sheet` 抽屉。
- 顶部标题行：`记事本`（`text-[17px] font-semibold`）、右边“只看项目”下拉（`dropdown-menu`：`全部项目` + 激活的项目列表）、关闭按钮（`.icon-btn`，`aria-label="关闭记事本"`）。
- 输入区：一个自动增高的 textarea（最少 3 行、最多 10 行，16px 字号），占位文字 `想到什么就记下来…（Enter 保存，Shift+Enter 换行）`；Enter（不在输入法组合中）保存，保存后清空并保持焦点；下面一行左边是回形针按钮（复用 `ChatView` 的上传逻辑：`api.upload`，`MAX_FILES` 限制）、右边是“挂到项目”下拉（默认是当前路由里的项目，没有就是“不挂”）。
- 分组标签：`未发送`、`已发送`、`已归档`（三个按钮做的分段控件，默认 `未发送`），后面带数量。
- 搜索框：`搜索笔记`，输入后 300ms 去抖调用 `api.notes({q})`。
- 列表：每条笔记一张卡片（`.card`，内边距 12px，间距 8px）：
  - 左上复选框（多选用）；正文（Markdown 渲染，用 `Markdown.tsx`，`Clamp` 限高 160px）；附件缩略（复用 `Message.tsx` 的 `Attachments`）；
  - 底部一行：项目名（`text-faint`）、`ago(updated_at)`；已发送的显示“发到 <线程标题>”，可点击跳到那个话题（`openTopic`）；
  - 悬停时右上出现图标按钮：编辑（原地变成 textarea，Enter 保存、Esc 取消）、归档（已归档的显示“放回”）、删除（`window.confirm('删除这条笔记？删除后不能恢复。')`）。
- 底部操作条：有选中时出现，`已选 N 条`、`发给…`（主要按钮 `.btn`）、`取消选择`。

“发给…”（组件 `NoteSendDialog.tsx`）：

1. 第一步选目标（单选列表）：`主线`；每个激活项目的 `项目对话：<项目名>`；`开一个会`。（发给 agent 不放在这里：私聊只在项目对话里，用户进项目对话后在那里发。）
2. 第二步编辑：一个 textarea，内容是选中笔记按 `created_at` 从旧到新拼接，用一个空行分隔；附件合并（最多 8 个，多了提示“附件最多 8 个，多出来的不会发出”）。下面是 `发送` 按钮。
3. 发送：
   - 主线：`api.sendMessage(主线 id, {content, attachments, context: {note_ids}})`（用 `app.ts` 里现有的发送函数，给它加一个可选的 `noteIds` 参数）；
   - 项目对话：用 `sendToProject`，同样加 `noteIds` 参数；
   - 开一个会：调用 B 批的 `api.createMeeting({question: content, note_ids, parent_thread_id: 主线 id})`（B 批做完前这个选项不显示）。
   - 成功后关闭对话框、清空选择、提示 `已发出`（用现有的 toast；没有 toast 就在抽屉顶部显示 2 秒的 `.pill`）。

入口：

- `Shell.tsx` 图标栏“打开对话”按钮下面加 `RailButton tip="记事本"`，图标 `note`（`Icon.tsx` 映射表加 `note: Note01Icon`），点击切换 `ui.state.notesOpen`。
- 快捷键：`main.tsx` 或 `Shell.tsx` 里监听 `keydown`：`(e.metaKey || e.ctrlKey) && e.shiftKey && e.key.toLowerCase() === 'n'` 时 `preventDefault()` 并打开抽屉、聚焦输入框。
- 手机：`ChatPanel` 的手机顶栏 `actions` 里加记事本按钮（同一个图标）。
- 用户消息气泡：`context.note_ids` 非空时，气泡上方显示一行 `text-[12px] text-faint`：`来自笔记`（加 `Icon name="note" size={12}`）。

测试：`src/lib/notes.ts` 放纯函数 `joinNotes(notes): {text, attachments, dropped}`（拼接和附件截断）、`noteGroups(notes)`，配 `notes.test.ts`；`reduce.test.ts` 加 `notesRev` 的用例。

验证：`cd apps/core && go test ./...`；`cd apps/web && bun run check && bun run test`。

## A5. 浏览器验收

照 B/C 批末尾的“独立 Hub”步骤（见 C6），验收：抽屉打开/关闭、快捷键、新建（含附件）、编辑、归档和放回、删除、搜索、多选发到项目对话（消息带“来自笔记”，笔记变成已发送且能跳回）、手机布局。`sous note "测试"` 后页面上实时出现。

---

# B. 会议

## B1. 数据

迁移（追加）：

```sql
ALTER TABLE threads ADD COLUMN parent_thread_id TEXT NOT NULL DEFAULT '';
ALTER TABLE threads ADD COLUMN parent_topic_id TEXT NOT NULL DEFAULT '';
ALTER TABLE threads ADD COLUMN meeting_topic TEXT NOT NULL DEFAULT '';
ALTER TABLE threads ADD COLUMN brief TEXT NOT NULL DEFAULT '';
ALTER TABLE threads ADD COLUMN host TEXT NOT NULL DEFAULT '';
ALTER TABLE threads ADD COLUMN meeting_status TEXT NOT NULL DEFAULT '';
CREATE INDEX threads_parent ON threads(parent_thread_id);
CREATE TABLE minutes (
  meeting_id TEXT NOT NULL,
  version    INTEGER NOT NULL,
  content    TEXT NOT NULL,
  status     TEXT NOT NULL,
  message_id TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  posted_at  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (meeting_id, version)
);
```

事件（新文件 `event/meeting.go`）：

```go
const (
	MeetingClosed   Type = "meeting.closed"
	MeetingReopened Type = "meeting.reopened"
	MinutesDrafted  Type = "minutes.drafted"
	MinutesPosted   Type = "minutes.posted"
)

const (
	KindMeeting         = "meeting"          // thread kind
	KindMinutes         = "minutes"          // message kind
	KindMeetingProposal = "meeting_proposal" // message kind
	TriggerMeeting      = "meeting"          // trigger kind
	MeetingOpen         = "open"
	MeetingClosedState  = "closed"
)

type MeetingHost struct {
	ProviderID string `json:"provider_id,omitempty"`
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
}

type MeetingPayload struct {
	MeetingID string `json:"meeting_id"`
}

type MinutesPayload struct {
	MeetingID string `json:"meeting_id"`
	Version   int    `json:"version"`
	Content   string `json:"content"`
	MessageID string `json:"message_id,omitempty"` // minutes.posted
}
```

`ThreadCreatedPayload` 加：`ParentThreadID`、`ParentTopicID`、`MeetingTopic`、`Brief string`，`Host *MeetingHost`（JSON 名 `parent_thread_id`、`parent_topic_id`、`meeting_topic`、`brief`、`host`，都 `omitempty`）。`MessageSource` 加 `MeetingID string \`json:"meeting_id,omitempty"\``、`Version int \`json:"version,omitempty"\``、`TaskID string \`json:"task_id,omitempty"\``（C 批用）。

投影：

- `thread.created`：插入时多写这几列；`kind == "meeting"` 时 `meeting_status = 'open'`，`host` 存 JSON。
- `meeting.closed` / `meeting.reopened`：`UPDATE threads SET meeting_status = ? WHERE id = ?`（`ev.ThreadID` 是会议 id）。
- `minutes.drafted`：`INSERT OR REPLACE INTO minutes (..., status) VALUES (..., 'draft')`。
- `minutes.posted`：`UPDATE minutes SET status = 'posted', content = ?, message_id = ?, posted_at = ? WHERE meeting_id = ? AND version = ?`。

`store.Thread` 加字段 `ParentThreadID`、`ParentTopicID`、`MeetingTopic`、`Brief`（JSON 不输出 brief：`json:"-"`）、`Host *event.MeetingHost`、`MeetingStatus string \`json:"meeting_status,omitempty"\``；读线程的所有 `SELECT` 都加上这些列。`store.Threads`（一般线程列表）**排除** `kind = 'meeting'`。新读法：

```go
func (s *Store) Meetings(ctx context.Context, parentThreadID string) ([]Thread, error) // parent 为空时列全部会议；按 updated_at 倒序，最多 200
func (s *Store) Minutes(ctx context.Context, meetingID string) ([]MinutesRow, error)  // 按 version 升序
func (s *Store) LinkedMeeting(ctx context.Context, threadID, meetingID string) (bool, error)
// LinkedMeeting: threadID 里有一条 kind = 'minutes' 且 source 里 meeting_id = meetingID 的消息
```

`MinutesRow{MeetingID, Version, Content, Status, MessageID, CreatedAt, PostedAt}`，JSON 用小写下划线。

`topicOfMessage`（`store/topics.go`）：线程是会议、`topicID` 为空时，用这个线程的 `meeting_topic`（在事务里查一次 `threads`）；开场消息本身的 `MessageID` 就等于 `meeting_topic`，按原来的逻辑开出话题。

单测：会议线程不出现在 `Threads` 里、`Meetings` 能按 parent 过滤、会议里不带话题的消息都归到 `meeting_topic`、`LinkedMeeting` 真假两种。

## B2. 开会和会议的接口

新文件 `supervisor/meeting.go` 和 `supervisor/meeting_routes.go`，在 `supervisor.Routes` 里注册：

- `POST /api/v1/meetings {parent_thread_id, parent_topic_id?, question, note_ids?, host?, project_id?}` → 201 `{meeting: Thread}`：
  - `question` 去空白后为空 400 `empty`；父线程不存在 404；`parent_topic_id` 给了但不属于父线程 404。
  - `project_id`：不填时用父线程的 `project_id`。
  - 标题：`question` 第一行，最多 30 字（用 `store.TopicTitle("", question)`）。
  - 简报 `brief`（纯文本，格式固定）：

    ```
    ## 来自
    <父线程标题>（<父线程是主线时写“主线”，是项目对话时写“<项目名> 的项目对话”>）
    ## 话题
    <父话题标题>：<父话题的当前结论，没有就写“还没有结论”>
    ## 相关任务
    - <任务 id>「<标题>」<状态词>
    （父话题没有任务时整节不写）
    ## 相关摘要
    <父线程当前段摘要里，带有父话题内任意消息 [#seq] 的那些行，原样；没有就整节不写>
    ## 笔记
    <note_ids 对应的笔记正文，按创建时间，空行分隔；没有就整节不写>
    ```

    状态词用 `task` 包里现有的中文状态说明（没有现成的就用 `status` 原值）。
  - 在一个 `Write` 事务里追加：`thread.created`（`Kind: meeting`、上面这些字段、`MeetingTopic: openID`，`ThreadID` 是新的会议 id）、`message.user`（`MessageID: openID`、`Content: question`、`TopicTitle: 标题`、`Context: {ProjectID, NoteIDs}`）、有 `note_ids` 时再追加 `note.sent`（指向会议）。
- `GET /api/v1/meetings?parent_thread_id=` → `{meetings: [Thread...]}`。
- `GET /api/v1/meetings/{id}` → `{meeting: Thread, minutes: [MinutesRow...]}`；不是会议 404。
- `POST /api/v1/meetings/{id}/close` → 202 `{queued: true}`；会议已结束 409 `meeting_closed`。立即追加 `meeting.closed`，然后 `s.enqueue(id, func() { s.writeMinutes(id) })`（B4）。
- `POST /api/v1/meetings/{id}/reopen` → 200；会议没结束 409 `meeting_open`。追加 `meeting.reopened`。
- `POST /api/v1/meetings/{id}/minutes {content?}` → 200 `{message_id}`：发出最新一版草稿（B5）；`content` 非空时用它替换草稿内容；没有草稿 409 `no_draft`。
- `PATCH /api/v1/meetings/{id} {host}`：改主持人的模型（只能在会议进行中）；追加 `thread.created` 不合适，所以新增事件 `meeting.host {meeting_id, host}`，投影更新 `threads.host`。（把 `MeetingHost` 事件常量 `MeetingHostSet Type = "meeting.host"` 加进 `event/meeting.go`。）

发消息：`httpapi.postMessage` 对会议线程：`meeting_status` 是 `closed` 时 409 `meeting_closed`（“会议已经结束，续会后才能发言”）；`topic_id` 为空时由投影归到 `meeting_topic`（B1），返回的 `topic_id` 也要是它（查线程的 `meeting_topic`）。

## B3. 主持人的回合

- `prompts/meeting.go`：

```go
type MeetingEnv struct {
	Title   string
	Project string // "" when not scoped
	Brief   string
}

// Meeting is the host's system prompt; nothing in it changes during a meeting.
func Meeting(env MeetingEnv) string
```

内容（原样，`%s` 处填值；`Project` 为空时去掉“作用域”那一段）：

```
你是 sous，现在在主持一个会议，和用户一起把一件还没想清楚的事讨论清楚。会议有自己独立的上下文，散会后会整理成纪要交回原来的对话，由那边决定怎么执行。

## 怎么主持
- 先弄清楚目标和约束，再谈方案。用户说得含糊时，一次问一到三个具体的问题，不要一次抛出一长串。
- 给方案时至少给两个可选项，写清楚各自的代价和适用的情况，说出你倾向哪个、为什么。
- 用户否掉一个方案时，记下理由；用户改了主意时，指出和之前哪句话不一样。
- 需要看代码时用 project_read 或 consult（项目顾问只读）；需要查资料时用 web_search、web_fetch；需要以前的对话时用 thread_search、thread_read。
- 会议里只讨论，不动手：不建任务、不派发、不做 git 操作，也不要答应“我这就去做”。要做的事说成行动项，散会后交回原来的对话处理。
- 讨论差不多了、或者在原地打转时，提醒用户可以散会。

## 会议
- 议题：%s

## 作用域
这个会议属于项目 %s。

## 简报（开会时从原来的对话带过来的背景）
%s
```

- `supervisor/meeting.go`：

```go
// meetingTools are the only tools a meeting's host may call.
var meetingTools = []string{"project_read", "consult", "web_search", "web_fetch", "memory_search", "thread_search", "thread_read", "current_time"}

func isMeeting(t store.Thread) bool { return t.Kind == event.KindMeeting }
```

- `context.go` 的 `build`：`isMeeting(thread)` 时：
  - `System` 用 `prompts.Meeting(MeetingEnv{Title: thread.Title, Project: project, Brief: thread.Brief})`；
  - `Tools` 用 `s.opt.Tools.Subset(meetingTools...).Specs()` 加 `silentSpec`，**不加** `topicSpecs`，也不加 `meeting_propose`；
  - 其余不变。
- `supervisor.go` 的工具执行处（第 500 行附近 `r = s.opt.Tools.Call(ctx, c)`）：会议里调用不在 `meetingTools` 里的工具（`stay_silent` 由回合自己处理，不经过这里）时，不执行，返回 `provider.ToolResult{CallID: c.ID, Content: "会议里不能用 " + c.Name + "：会议只讨论，不动手。把它写成行动项。", IsError: true}`。
- 模型：`answerTopic` 里取 provider 的地方（第 407 行的 `s.opt.Provider.Stream`）改成局部变量 `prov`：会议的 `Host` 有 `Model` 且 `s.opt.Fixed != nil` 时 `prov = s.opt.Fixed(Host.ProviderID, Host.Model, Host.Effort)`，否则 `s.opt.Provider`。`Options` 加 `Fixed func(providerID, model, effort string) provider.Provider`。
- `models/models.go` 加：

```go
// Fixed is a provider that always uses one model of one provider, recorded
// under the supervisor role (a meeting's host).
func (r *Registry) Fixed(providerID, model, effort string) provider.Provider
```

  实现：和 `roleProvider` 一样，但 `Resolve` 换成用 `r.instance(ctx, providerID)` 取 provider，`req.Model = model`，`req.Effort` 为空时用 `effort`；`Name()` 返回 provider 的名字。在 `hub.go` 组装 supervisor 时传 `Fixed: h.Models.Fixed`。
- 整理：`compact.go` 的 `compact` 里，调用 `s.flush`（写记忆的静默回合）前判断线程是会议就跳过。
- 主线以外的 `notice`、`MaxAutoTurns` 等逻辑不用改。

单测 `supervisor/meeting_test.go`（照 `topics_test.go` 的写法，用 mock provider）：会议的请求里系统提示含简报、工具列表只有 `meetingTools` 里的加 `stay_silent`；模型调用 `task_create` 时得到上面那句错误；会议整理时没有写记忆的调用；`Host` 设了模型时用的是 `Fixed` 返回的 provider。

## B4. 写纪要

`prompts/meeting.go` 再加：

```go
const MinutesSections = "## 议题\n## 结论和决定\n## 被否掉的方案和理由\n## 未决问题\n## 行动项"

// Minutes asks for a meeting's minutes from its transcript.
func Minutes(title string) string
```

`Minutes` 的内容（原样）：

```
把下面这个会议整理成纪要。会议的议题是：%s

只输出纪要本身，用下面五个二级标题，顺序不变，每个标题下面是“- ”开头的条目；某一节没有内容时写“- 无”：
## 议题
## 结论和决定
## 被否掉的方案和理由
## 未决问题
## 行动项

要求：
- 每一条末尾用 [#编号] 标出它来自哪条消息，编号是原文里每条消息前面的 #数字；一条来自多条消息时写 [#12][#15]。“结论和决定”和“行动项”的每一条都必须有出处。
- 只写会上真的说过、定过的东西，不要补充自己的意见。
- 行动项写清楚要做什么，能看出是谁提出的就写上。
- 被否掉的方案要写理由。
```

`supervisor/meeting.go` 的 `writeMinutes(meetingID string)`：

1. 读线程、当前段（`s.segment`）和段后的全部消息（`MessagesAfter(ctx, id, seg.CoversUpTo)`），去掉 `kind` 是 `silent`、`step` 的。
2. 输入文字 = （段有摘要时）`"【之前的讨论摘要】\n" + seg.Summary + "\n\n"` + `meetingTranscript(消息)`。`meetingTranscript` 是新函数，格式和 `compact.go` 的 `transcript` 一样（`[#seq] 时间 谁：内容`），区别是不调用 `clip`（纪要要看全文），并且 `kind == consult` 的写成“项目顾问”、`kind == dm` 的助手消息写成 agent 名。
3. `estimateText(输入) > 0.6 * s.window(ctx)` 时：先 `s.compact(ctx, id, event.CompactManual)`，然后回到第 1 步（最多一次）。
4. 调用 `s.opt.Compactor`（为空时用 `s.opt.Provider`）：`System: prompts.Minutes(thread.Title)`，一条用户消息是输入文字。用量用 `s.record(id, "compactor", ...)` 记。
5. 检查（函数 `checkMinutes(content string, seqs map[int64]bool) []string`，返回问题列表）：
   - 五个标题都在，顺序对；
   - “结论和决定”“行动项”下每条 `- ` 开头的行（“- 无”除外）末尾有至少一个 `[#n]`，且每个 `n` 都在 `seqs` 里（`seqs` 是这次输入里出现的所有消息的 seq，加上段摘要里出现过的 `[#n]`）。
   问题列表非空时，把问题逐条附在用户消息后面（`"\n\n上一次的纪要有这些问题，改正后重新输出完整的纪要：\n- ..."`）再调用一次。第二次仍有问题时，接受这一次的输出，并在末尾追加一行 `\n\n（有 N 条没有标出处）`，N 是缺出处的条数。
6. 版本号 = 已有的最大版本 + 1。追加 `minutes.drafted`（`ThreadID` 是会议 id）。
7. 设置 `meeting.minutes_auto_post` 是 `"true"` 时，直接执行 B5 的发出。
8. 模型调用失败：在会议里追加一条 `kind: notice` 的助手消息 `写纪要失败：<错误第一行>。可以再点一次“散会”重试。`，并追加 `meeting.reopened`（让用户能重试）。

单测：`checkMinutes` 表驱动（缺标题、顺序错、缺出处、出处不存在、“- 无”不算缺）；`writeMinutes` 用 mock provider 跑通“第一次缺出处、第二次正确”和“两次都缺”的两种情况。

## B5. 发出纪要

`supervisor/meeting.go` 的 `postMinutes(ctx, meetingID string, content string) (messageID string, err error)`：

1. 取最新一版 `status = 'draft'` 的纪要；没有返回 `errNoDraft`。`content` 为空时用草稿内容。
2. 父话题：`thread.ParentTopicID`；为空时纪要消息自己开话题（`TopicTitle: "会议纪要：" + 会议标题`）。
3. 一个 `Write` 事务里追加：
   - `message.assistant`，`ThreadID` 是父线程，`Kind: minutes`，`Content: "## 会议纪要：" + 标题 + "\n\n" + content`，`Source: {MeetingID, Version}`，`TopicID` 如上；
   - `minutes.posted`（`ThreadID` 是会议 id，带 `MessageID`、最终 `Content`）；
   - `trigger.fired`，`ThreadID` 是父线程，payload `{Kind: TriggerMeeting, Key: "meeting:" + id + ":" + version, MessageID: 新 id, Title: "会议结束", Content: 见下}`，同时作为 `kind: trigger` 的用户消息（照现有 trigger 的存法，看 `trigger` 包 `fire` 怎么做的，用同样的方式），话题同上。

   trigger 的 `Content`（原样）：

   ```
   【会议结束】会议「<标题>」的纪要（第 <版本> 版）已经发到这个话题。
   看一下行动项：要建任务、动代码或做 git 操作的，先和用户确认再做；不清楚的地方用 thread_read（thread="<会议 id>"，seq=纪要里的编号）查会议原文。
   （这是 sous 的自动通知，不是用户发的消息。）
   ```

4. `supervisor.Start` 的监听里，`TriggerMeeting` 不走 `TaskBatch` 合并，按普通 trigger 立即处理（检查现有判断只对 `TriggerTask` 合并，确认即可）。
5. 第二版及以后：内容第一行写成 `## 会议纪要：<标题>（第 N 版，取代第 N-1 版）`。

设置：`notify/routes.go` 的 `settingsView` 加 `MinutesAutoPost bool \`json:"minutes_auto_post"\``，读写 settings 键 `meeting.minutes_auto_post`（`"true"` / `""`），照同文件里其他字段的写法。

## B6. 主管提议开会

- `supervisor/meeting.go` 加回合内处理的工具（照 `topicCall` 的方式，放在 `topicSpecs` 后面、只在非会议线程里加入工具列表，顺序固定）：

```
名字：meeting_propose
说明：用户的需求还不确定、需要来回讨论很多轮时，提议开一个会：会议在独立的上下文里讨论，散会后纪要回到这个话题。只是提议，用户点了“开会”才开始。
参数：{"question": string, 必填, "会议要讨论清楚的问题，一两句话"}
```

- 执行：存一条助手消息 `Kind: meeting_proposal`、`Content: question`、当前话题；工具结果是 `已经提议开会，等用户决定`。同一个回合只能提议一次，第二次返回错误 `这一轮已经提议过了`。
- `prompts.Supervisor` 的工具说明里不需要额外写（工具自己的说明够了）。
- `TestRequestsOnlyGrowWithinASegment` 必须继续通过（工具列表顺序固定）。

## B7. 回查原文

`tool/thread.go`：

- `thread_search` 和 `thread_read` 的 schema 加可选参数 `"thread": {"type":"string","description":"会议的 id（会议纪要里写着）；不填就是当前对话"}`。
- 给了 `thread` 且不等于当前线程时：`store.LinkedMeeting(ctx, turn.ThreadID, thread)` 为真才允许，否则返回错误 `只能读这个对话里纪要提到的会议`。允许时就在那个线程里搜或读。
- 会议的主持人读自己的会议不受影响（等于当前线程）。

单测：链接过的会议能读、没链接的不能读、`scope: all` 的行为不变。

## B8. 契约和文档

- `docs/api/core-hub-http.md`：会议的 6 个接口、消息的 `minutes` / `meeting_proposal` 种类、线程的新字段、`/threads` 不列会议、设置里的 `minutes_auto_post`、事件 `meeting.*`、`minutes.*`。
- `docs/architecture/supervisor-context.md` 加一节“会议”：独立线程、简报在系统提示、只读工具、整理不写记忆、纪要和叫醒、回查的权限。

## B9. web

类型和 api：

- `types.ts`：`Thread` 加 `parent_thread_id?`、`parent_topic_id?`、`meeting_topic?`、`host?`、`meeting_status?`；`Message.kind` 加 `'minutes' | 'meeting_proposal' | 'dm'`；`MessageSource` 加 `meeting_id?`、`version?`、`task_id?`；新类型 `MinutesRow`、`MeetingDetail`。
- `api.ts`：`createMeeting(input)`、`meetings(parentThreadId?)`、`meeting(id)`、`closeMeeting(id)`、`reopenMeeting(id)`、`postMinutes(id, content?)`、`setMeetingHost(id, host)`。
- `reduce.ts`：`meeting.closed` / `meeting.reopened` / `meeting.host` 更新 `chat.threads` 里对应线程的字段；`minutes.drafted` / `minutes.posted` 让 `chat.meetingsRev++`；会议线程的 `thread.created` 加进 `chat.threads`（但 `ChatPanel` 的对话选择里过滤掉 `kind === 'meeting'`）。加测试。
- `ui` store 加 `meeting: string`（正在看的会议 id，空表示没有）。

组件：

- `MeetingView.tsx`：在聊天栏里替换 `ChatPanel` 的内容（`ChatPanel` 渲染时 `ui.state.meeting` 非空就渲染 `MeetingView`）。用 `ChatView`，不传 `onSendIn`（只有时间线视图），`onSend` 发到会议线程。标题行：
  - 左边返回按钮（`aria-label="回到对话"`，清空 `ui.state.meeting`）、标题“会议 · <议题>”、状态胶囊（`进行中` / `已散会`）；
  - 右边：主持人模型（复用 `ModelSwitch`，改的是这个会议的 `host`，调用 `setMeetingHost`；`ModelSwitch` 现在改的是主管的角色，要给它加一个 `value` / `onChange` 的受控用法，不传时保持原来的行为）、`散会`（`.btn small`，进行中时显示；点击 `window.confirm('散会并整理纪要？')` 后 `closeMeeting`）、`续会`（已散会时显示）。
  - 已散会时输入框禁用，占位文字 `会议已经结束，点“续会”接着讨论`。
  - 底部（时间线最后）：有草稿时显示“纪要草稿”卡片：Markdown 预览 + “编辑”切换成 textarea；按钮 `发出纪要`（主要）和 `重新整理`（再调用一次 `closeMeeting` 之前先 `reopenMeeting`）。整理中（已散会、还没有草稿）显示一行 `正在整理纪要…` 带 `live-dot`。
- 会议列表：`ChatPanel` 标题行加一个图标按钮 `Icon name="meeting"`（映射表加 `meeting: UserGroupIcon`），`aria-label="会议"`，弹出 `PopoverContent`：顶部 `开会` 按钮（打开下面的开会对话框，父线程是当前线程）；下面分“进行中”“已散会”两组，每行：议题、`ago(updated_at)`，点击 `ui.state.meeting = id`。
- 开会对话框 `NewMeetingDialog.tsx`（`Dialog`）：标题 `开个会`；textarea `这次要讨论清楚什么？`；只读的一行“来自：<父线程标题> · <父话题标题>”；`开会` 按钮 → `createMeeting` → 打开会议。入口：会议列表的 `开会`、话题卡片标题行的图标按钮（`TopicList.tsx`，`aria-label="就这个话题开会"`，父话题是这个话题）、记事本的“开一个会”、`meeting_proposal` 卡片。
- `Message.tsx`：
  - `kind === 'minutes'`：卡片（`.card`，左侧 3px `--accent` 竖条）：标题行“会议纪要 · 第 N 版”、`查看原始记录` 链接（打开会议：`ui.state.meeting = source.meeting_id`）；正文 Markdown，`[#12]` 渲染成链接（点击打开会议并滚动到 seq 12 的消息：给 `ChatView` 加 `focusSeq` 参数，滚到 `data-seq="12"` 的元素并闪一下背景）；如果同一会议有更高版本的纪要消息，这张折叠成一行 `会议纪要 · 第 N 版 · 已被第 M 版取代`，可展开。
  - `kind === 'meeting_proposal'`：一行卡片：`Icon name="meeting"`、`sous 建议开个会：<content>`、按钮 `开会`（打开 `NewMeetingDialog`，问题预填 `content`，父话题是这条消息的话题）。
- 设置“通知与默认”页加一个开关：`纪要直接发出`，说明文字 `散会后不再让你确认，纪要整理好就发回原来的对话`。

测试：`src/lib/meetings.ts` 放纯函数 `minuteRefs(content): number[]`（取出所有 `[#n]`）、`supersededBy(messages, message): number | null`、`renderRefs(markdown)`（把 `[#12]` 换成 `[#12](#seq-12)` 这样的链接，`Markdown` 里拦截 `#seq-` 开头的链接），配 `meetings.test.ts`；`reduce.test.ts` 加会议事件的用例。

验证：`cd apps/core && go test ./...`；`cd apps/web && bun run check && bun run test`。

---

# C. 直接和子 agent 对话

## C1. 协议和 Node

- `proto/proto.go` 加 `PurposeChat = "chat" // the user talks to a task's agent directly (decision 0028)`；`event/task.go` 加同名的 `PurposeChat`；`proto` 加 `OutcomeChanged = "changed"`。
- `node/run.go`：
  - `execute` 里设置 `workBaseline` 的条件加上 `r.spec.Purpose == proto.PurposeChat`。
  - 会话恢复：找到处理 `ResumeSession` 的地方（`grep -n "ResumeSession\|continued" internal/node/run.go`）。`Purpose == chat` 且会话没能加载时，不退回完整提示，直接 `r.end("failed", "", "", "", fmt.Errorf("接不上之前的会话：%w", 加载错误))`。
  - 结束：`Purpose == chat` 时不走 `workOutcome`；计算 `produced`（和工作 Run 一样的指纹比较），`produced` 时 outcome 是 `proto.OutcomeChanged`，否则为空；用 `r.endWith` 或 `r.end` 现有的方式带上 outcome（看 `workOutcome` 返回的结构怎么传，照它构造）。
  - `buildPrompt`：`chat` 和工作 Run 走同一个分支（目录说明照常加）。
- `node/daemon.go`：`chat` Run 的权限模式照 Hub 发来的，不改。
- 测试：在 `internal/node` 里加 `chat_e2e_test.go`（照 `flex_e2e_test.go` 的结构）：假 agent `FAKE_ACP_LOAD=1`，先跑一个工作 Run 得到会话，再发一个 `chat` Run（`resume_session` 是那个会话），检查 agent 收到的是 `continue_prompt`；`FAKE_ACP_MODE=edit` 时 outcome 是 `changed`；会话 id 无效时 Run 失败且错误以“接不上之前的会话”开头。

## C2. Hub：接口和投递

迁移（追加）：`ALTER TABLE runs ADD COLUMN chat_from TEXT NOT NULL DEFAULT '';`，`store.Run` 加 `ChatFrom string \`json:"chat_from,omitempty"\``，`run.queued` 的投影写入它（`RunQueuedPayload` 加 `ChatFrom`，`nodehub.CreateRun` 加 `ChatFrom` 并传进 payload）。

`store/topics.go`：`startsTopic` 和 `reopensTopic` 在 `kind == "dm"` 时返回 false（`event` 包加常量 `KindDM = "dm"`）。

新文件 `task/dm.go`：

```go
type DMTarget struct {
	RunID   string `json:"run_id,omitempty"`
	TaskID  string `json:"task_id,omitempty"`
	Consult bool   `json:"consult,omitempty"`
}

type DMInput struct {
	ThreadID string
	TopicID  string
	Content  string
	To       DMTarget
}

// DM delivers what the user says to an agent directly (decision 0028).
func (e *Engine) DM(ctx context.Context, in DMInput) (messageID string, err error)
```

校验（全部返回 `ErrInvalid` / `ErrConflict` 包装的中文错误，HTTP 层映射成 400 / 409）：

- 线程必须是项目对话（`kind == project`），否则 `私聊只能在项目对话里用`。
- `To` 里恰好有一项，否则 `要指定一个对象`。
- `Content` 去空白后不能为空，最多 20000 字。
- `RunID`：Run 存在、属于这个项目（`run.ProjectID` 或它的任务的项目）、状态是 `running` 或 `paused`，否则 `这个 Run 已经结束了`。
- `TaskID`：任务存在、属于这个项目；有 `running` / `paused` 的工作或 chat Run 时等同 `RunID`；否则要有一个记着 `Session` 的工作或 chat Run（取最近的），没有就 `这个任务的 agent 没有留下会话，不能接着聊`。任务状态是 `queued` 或 `waiting_approval` 时 `任务正在排队或等审批，先处理完再私聊`。
- `Consult`：项目要在某台机器上，否则用 `Consult` 自己的错误。

处理：

1. 存用户消息：`message.user`，`Kind: dm`，`Source: {RunID, TaskID, Agent, ProjectID}`（`RunID` 是要投递到的 Run，新开 chat Run 时是新 Run 的 id，预先 `ids.New()` 生成；顾问时 `RunID` 为空、`Agent` 是 `顾问`），`TopicID`：`in.TopicID` 非空就用它，否则用任务的 `TopicID`，再没有就空（dm 不开话题）。
2. 投递：
   - 运行中的 Run：`e.Nodes.MessageAs(ctx, runID, content, event.ActorUser)`。
   - 新的 chat Run：照 `startRun` 构造 `nodehub.CreateRun`：`ThreadID` 是这个项目对话，`NodeID` / `Agent` 取那个有会话的 Run 的，`RepoPath` 取它的，`TaskID`、`Purpose: chat`、`PermissionMode`（同 `startRun` 的计算）、`Branch` / `BaseBranch` / `BaseFrom` / `Kind`（同 `startRun` 按 `t.Place`）、`Title: "私聊：" + 任务标题`、`ResumeSession: 那个 Run 的 Session`、`ContinuePrompt: content`、`Prompt: content`、`RunID: 预生成的 id`、`ChatFrom: 任务当前状态`。然后 `e.transition(ctx, t, event.TaskRunning, "用户在私聊", "", event.ActorUser)`。
   - 顾问：`go e.Consult(context.WithoutCancel(ctx), ConsultInput{ProjectID, Question: content, ThreadID, TopicID})`，不等回答（回答照旧作为 `consult` 消息出现）。
3. 返回消息 id。

`task/advance.go` 的 `Advance`：在 `if r.Purpose == event.PurposeConsult || r.TaskID == ""` 之前加 `if r.Purpose == event.PurposeChat { return e.afterChat(ctx, r) }`。`afterChat`（写在 `dm.go`）：

1. 读任务；任务已经不是 `running`（比如用户取消了）就什么都不做。
2. 有 agent 的回复（`r.Summary` 非空）：追加 `message.assistant`，`Kind: dm`，`ThreadID: r.ThreadID`，`Content: r.Summary`，`Source: {RunID: r.ID, TaskID, Agent: r.Agent, ProjectID}`，`TopicID: 任务的话题`。Run 失败时内容是 `没能接着聊：<r.Error>`，同样 `kind: dm`。
3. `r.Outcome == proto.OutcomeChanged`：`e.finish(ctx, t, r, "私聊后有改动")`（它会转成 done、设 git_state、发汇报并叫醒主管）。
4. 否则：`e.transition(ctx, t, firstNonEmpty(r.ChatFrom, event.TaskDone), "", "", event.ActorSystem)`。

HTTP：`task/routes.go` 注册 `POST /api/v1/threads/{id}/dm {content, to, topic_id?}` → 202 `{message_id}`；错误码：`ErrInvalid` 400 `bad_request`，`ErrConflict` 409 `conflict`，不存在 404。

单测（`task/dm_test.go`，照 `task/topics_internal_test.go` 的环境搭法）：非项目对话被拒；对运行中的 Run 投递成为追加指令（`run.message` 事件，actor user）；对已结束任务开出 `purpose: chat` 的 Run，带 `resume_session` 和 `continue_prompt`，任务转 `running`；`afterChat` 有改动时任务 `done` + 汇报 + trigger，没改动时回到原状态、没有汇报；dm 消息不开话题。

## C3. 主管

- `supervisor/topics.go` 的 `answers`、`pendingTopics`、`unanswered`、`oldestPending`：`Kind == dm` 的消息跳过（既不是待回复，也不是回复）。`absorb` 只看 `kind == ""`，已经排除了，确认即可。
- `context.go` 的 `build`：传给 `toProviderWith` 的历史里去掉 `Kind == dm` 的消息。
- 私聊提示：`prompts.TurnEnv` 加 `DMs []string`，`Tail` 在末尾加一节（有内容才写）：

  ```
  ## 用户的私聊
  用户绕过你直接和这些 agent 说过话（原文不在上面的对话里，需要时用 thread_read 按编号读）：
  - <一行>
  ```

  每行格式：`<agent> · 任务 <任务 id>「<标题>」：<N> 条，最近一次 <HH:MM>（#<最近一条用户私聊的 seq>）`；顾问：`项目顾问：<N> 条，最近一次 <HH:MM>（#<seq>）`。统计范围是当前段内（`MessagesAfter(seg.CoversUpTo)`）这个线程的 `role == user && kind == dm` 的消息，按 `(task_id, agent)` 分组，最近的在前，最多 8 行。
- 单测：dm 消息不触发回合；有 dm 时 Tail 里有这一节且编号正确；`TestRequestsOnlyGrowWithinASegment` 仍然通过。

## C4. 契约和文档

- `docs/api/core-hub-http.md`：`POST /threads/{id}/dm`、消息的 `dm` 种类、Run 的 `chat` 用途和 `chat_from`、outcome `changed`。
- `docs/api/core-hub-node.md`：`purpose: chat` 的行为（准备目录同工作 Run、接不上会话就失败、`outcome: changed`）。
- `docs/architecture/workbench.md` 的“讨论、做事和交付”加一段“直接和子 agent 对话”。
- `docs/decisions/0028`、`0029` 的 Status 改成 `Accepted`，`docs/decisions/README.md` 同步；`docs/decisions/0022` 的 Status 改成 `Accepted（第 1 条“只和主管对话”被 0028 修改）`。

## C5. web

- `api.ts`：`dm(threadId, content, to, topicId?)`。
- `ChatView.tsx` 加可选参数 `targets?: DmTarget[]`（`{key, kind: 'sous' | 'run' | 'task' | 'consult', label, agent?, id?}`）和 `onDm?: (target, text) => Promise<unknown>`。有 `targets` 时：
  - 输入框左下（回形针左边）是对象切换按钮：`Avatar`/`AgentIcon` + 名字 + 下拉箭头，`aria-label="选择和谁说"`，用 `dropdown-menu`，列表第一项是 `sous`，后面是 `targets`，分组标题 `正在运行`（kind run）、`任务的 agent`（kind task）、`项目顾问`（kind consult）。
  - 选中的不是 sous 时：输入框外框 `border-accent`，占位文字 `只有 <名字> 会看到…`，附件按钮隐藏（私聊不带附件），@ 候选关闭；发送调用 `onDm`。发送后对象保持不变；对象旁边有 × 回到 sous。
- `ProjectChat.tsx`：计算 `targets`：本项目里 `running` / `paused` 的工作 Run（`label: <agent> · <任务标题>`，kind run）、本项目里没有在跑的 Run 但最近工作 Run 有 `session` 的任务（最多 10 个，最近更新的在前，kind task，`label: <agent> · <任务标题>`）、`项目顾问`（kind consult，始终有）。`onDm` 调用 `api.dm`，`to` 分别是 `{run_id}`、`{task_id}`、`{consult: true}`，`topic_id` 是当前展开的话题（没有就不传）。
- `ui` store 加 `dmTarget: Record<string /* project id */, string /* target key */>`，任务的 Run 行（`RunChip`、`TaskView` 的 Run 卡片标题行）加一个图标按钮 `Icon name="chat"`、`aria-label="私聊"`：打开项目对话并选中这个对象。
- `Message.tsx`：`kind === 'dm'`：
  - 用户的：右侧气泡照常，但气泡上方一行 `text-[12px] text-faint`：`私聊 → <agent>`；
  - agent 的：左侧带 2px `--line` 竖条（和顾问的样式一样，但标签写 `<agent> · 私聊 · <任务标题>`），Markdown，`Clamp`。
  - 话题视图里，`kind === 'dm'` 的消息归到它的话题里显示（不需要特别处理，只要不被当成主管的回复）。在 `topics.ts` 里计算“未回复”时跳过 `dm`（加测试）。
- 已送达：投递给运行中的 Run 时，用户的 dm 气泡下显示 `已送达`（收到 `run.message.delivered` 且 `message_ids` 包含它时；`reduce.ts` 已有这个事件的话复用，没有就加）。

测试：`src/lib/dm.ts` 放 `dmTargets(project, tasks, runs)` 纯函数，配 `dm.test.ts`（运行中优先、去重、最多 10 个任务、顾问总在最后）；`topics.test.ts` 加 dm 不算未回复。

验证：`cd apps/core && go test ./...`；`cd apps/web && bun run check && bun run test`。

## C6. 端到端浏览器验收（三批一起做一次）

1. 在 `work/tmp/notes-meetings/` 里复制 `apps/core/.data/hub.db`（没有就新建），写 `hub.yaml`：`listen: 127.0.0.1:8798`、`public_url: http://localhost:8798`、`data_dir` 是这个目录、`web_dir` 指向 `build/web`、`dev: true`、角色 `supervisor` / `compactor` 用 `mock/echo`，`reviewer` / `approver` / `inspector` 用 `mock/verdict`。
2. `wok build web`；在 `apps/core` 里后台运行 `go run ./cmd/sous-hub -config ../../work/tmp/notes-meetings/hub.yaml`。
3. 用假 agent 配对并启动 daemon（`--home work/tmp/notes-meetings/node`，`node.json` 照 `apps/core/AGENTS.md` 的说明覆盖 opencode，`FAKE_ACP_MODE=edit`、`FAKE_ACP_LOAD=1`）；登记一个测试仓库为项目。
4. 内置浏览器打开 `http://localhost:8798`，dev-login 后检查：
   - 记事本：A5 的全部项。
   - 会议：从一个话题开会 → 会议在聊天栏打开、主线的对话选择里看不到它 → 发两条消息 → 散会 → 出现纪要草稿 → 编辑后发出 → 父话题里出现纪要卡片、`[#n]` 能跳到会议原文、主管被叫醒（mock 会回复）→ 续会 → 再散会 → 新版本，旧版本折叠。打开“纪要直接发出”后再开一个会，散会后不出现草稿、直接发出。
   - 私聊：建一个任务派发并跑完 → 在项目对话里把对象切到这个任务的 agent → 发一句 → 任务变成执行中、出现 `chat` Run → 结束后 agent 的回复以私聊显示、任务回到已结束、因为有改动所以有汇报；私聊项目顾问能得到回答；主线里看不到私聊对象切换。
   - 手机尺寸（375×812）下记事本、会议、对象切换都能用。
   - 深色模式正常。
5. 截图存在 `work/tmp/notes-meetings/` 交给指派你的人，然后停止 Hub 和 daemon，删除整个 `work/tmp/notes-meetings/`。

最后：更新 `apps/core/AGENTS.md`（会议、私聊、记事本的代码位置和约束）和 `apps/web/AGENTS.md`（新组件和约定），勾完 `tasks.md`，在仓库根目录运行 `wok check`，修掉自己造成的问题。
