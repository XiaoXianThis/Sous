package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

// Tools returns the supervisor's task tools.
func (e *Engine) Tools() []tool.Tool {
	return []tool.Tool{createTool{e}, updateTool{e}, cancelTool{e}, dispatchTool{e}, listTool{e}, runMessageTool{e}, runCancelTool{e}}
}

type createTool struct{ e *Engine }

func (createTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "task_create",
		Description: "创建一个任务，交给用户电脑上的编程 agent 去做。代码任务（改某个项目的代码）要给 project；" +
			"一般任务（不涉及仓库，比如查磁盘占用、整理文件）不给 project。detail 要写清楚目标、范围和完成标准，agent 看不到这段对话。" +
			"dispatch 为 true 时立刻派发：有空闲机器就开始，没有就排队，等机器上线再开始。完成后会先验收，代码任务再 push 并创建 PR，结果会自动汇报到这个对话。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"title":{"type":"string","description":"一句话标题"},
			"detail":{"type":"string","description":"完整的任务说明"},
			"project":{"type":"string","description":"项目名或 id；一般任务不填"},
			"dispatch":{"type":"boolean","description":"是否立刻派发，默认 true"},
			"agent":{"type":"string","description":"指定 agent：claude / codex / cursor / opencode / kiro；不填按项目和全局默认"},
			"machine":{"type":"string","description":"指定机器名或 id；不填按项目的机器优先级"},
			"permission_mode":{"type":"string","enum":["default","auto","open"],"description":"只在用户明确要求时设置；不填用项目或全局默认"},
			"priority":{"type":"integer","description":"越大越优先，默认 0"},
			"parent":{"type":"string","description":"父任务 id，用于拆分子任务"},
			"depends_on":{"type":"array","items":{"type":"string"},"description":"要等这些任务完成后才开始"}
		},"required":["title","detail"]}`),
	}
}

func (t createTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Title, Detail, Project, Agent, Machine, Parent string
		PermissionMode                                 string `json:"permission_mode"`
		Dispatch                                       *bool
		Priority                                       int
		DependsOn                                      []string `json:"depends_on"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	in := Input{ThreadID: tool.TurnFrom(ctx).ThreadID, Title: a.Title, Detail: a.Detail, Agent: a.Agent, Mode: a.PermissionMode,
		Priority: a.Priority, ParentID: a.Parent, DependsOn: a.DependsOn, Dispatch: a.Dispatch == nil || *a.Dispatch, Actor: event.ActorSupervisor}
	if a.Project != "" {
		p, err := t.e.findProject(ctx, a.Project)
		if err != nil {
			return "", err
		}
		in.ProjectID = p.ID
	}
	if a.Machine != "" {
		id, err := t.e.findNode(ctx, a.Machine)
		if err != nil {
			return "", err
		}
		in.Node = id
	}
	task, err := t.e.Create(ctx, in)
	if err != nil {
		return "", err
	}
	return "已创建：" + describe(task), nil
}

type updateTool struct{ e *Engine }

func (updateTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "task_update",
		Description: "修改任务：标题、说明、优先级、指定的 agent 或机器、依赖；或者把状态改成 inbox / planned / blocked / done。只填要改的字段。" +
			"任务正在执行时，改说明会把新说明转给 agent；只是补充一两句要求时，用 run_message 更直接。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"id":{"type":"string"},
			"title":{"type":"string"},"detail":{"type":"string"},"priority":{"type":"integer"},
			"agent":{"type":"string"},"machine":{"type":"string"},
			"permission_mode":{"type":"string","enum":["default","auto","open"]},
			"depends_on":{"type":"array","items":{"type":"string"}},
			"status":{"type":"string","enum":["inbox","planned","blocked","done"]},
			"reason":{"type":"string","description":"改状态的原因"}
		},"required":["id"]}`),
	}
}

func (t updateTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		ID, Title, Detail, Agent, Machine, Status, Reason *string
		PermissionMode                                    *string `json:"permission_mode"`
		Priority                                          *int
		DependsOn                                         *[]string `json:"depends_on"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if a.ID == nil {
		return "", fmt.Errorf("id is required")
	}
	f := event.TaskFields{Title: a.Title, Detail: a.Detail, Priority: a.Priority, AgentPref: a.Agent, PermissionMode: a.PermissionMode}
	if a.Machine != nil {
		id := ""
		if *a.Machine != "" {
			var err error
			if id, err = t.e.findNode(ctx, *a.Machine); err != nil {
				return "", err
			}
		}
		f.NodePref = &id
	}
	if a.DependsOn != nil {
		f.DependsOn, f.SetDeps = *a.DependsOn, true
	}
	task, err := t.e.Update(ctx, *a.ID, f, event.ActorSupervisor)
	if err != nil {
		return "", err
	}
	if a.Status != nil {
		reason := ""
		if a.Reason != nil {
			reason = *a.Reason
		}
		if task, err = t.e.SetStatus(ctx, *a.ID, *a.Status, reason, event.ActorSupervisor); err != nil {
			return "", err
		}
	}
	return "已更新：" + describe(task), nil
}

type cancelTool struct{ e *Engine }

func (cancelTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "task_cancel",
		Description: "取消任务：停止它正在执行的 Run，并取消它未完成的子任务。",
		Schema:      json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"reason":{"type":"string"}},"required":["id"]}`),
	}
}

func (t cancelTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct{ ID, Reason string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if err := t.e.Cancel(ctx, a.ID, a.Reason, event.ActorSupervisor); err != nil {
		return "", err
	}
	return "已取消", nil
}

type dispatchTool struct{ e *Engine }

func (dispatchTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "task_dispatch",
		Description: "派发一个还没开始、失败了或受阻的任务（也用于重试）。可以换 agent 或换机器；不填就按任务原来的设置。" +
			"没有可用机器时任务会排队，机器上线后自动开始。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"id":{"type":"string"},"agent":{"type":"string"},"machine":{"type":"string"}
		},"required":["id"]}`),
	}
}

func (t dispatchTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct{ ID, Agent, Machine string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	node := ""
	if a.Machine != "" {
		var err error
		if node, err = t.e.findNode(ctx, a.Machine); err != nil {
			return "", err
		}
	}
	task, err := t.e.Dispatch(ctx, a.ID, node, a.Agent, event.ActorSupervisor)
	if err != nil {
		return "", err
	}
	return describe(task), nil
}

type listTool struct{ e *Engine }

func (listTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "task_list",
		Description: "列出任务。默认列出所有未关闭的任务；status 可以过滤，比如 [\"done\"]。",
		Schema:      json.RawMessage(`{"type":"object","properties":{"status":{"type":"array","items":{"type":"string"}}}}`),
	}
}

func (t listTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct{ Status []string }
	json.Unmarshal(raw, &a)
	if len(a.Status) == 0 {
		a.Status = openStatuses
	}
	tasks, err := t.e.store().Tasks(ctx, store.TaskFilter{Statuses: a.Status, Limit: 50})
	if err != nil {
		return "", err
	}
	if len(tasks) == 0 {
		return "没有符合条件的任务", nil
	}
	var b strings.Builder
	for _, task := range tasks {
		b.WriteString("- " + describe(task) + "\n")
	}
	return b.String(), nil
}

var openStatuses = []string{event.TaskInbox, event.TaskPlanned, event.TaskQueued, event.TaskRunning,
	event.TaskWaitingApproval, event.TaskBlocked, event.TaskReview, event.TaskFailed}

type runMessageTool struct{ e *Engine }

func (runMessageTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "run_message",
		Description: "把用户的补充说明转给正在执行的任务（agent 在当前这一步结束后会收到）。" +
			"用户在任务执行期间补充要求、纠正方向时用它；和正在执行的任务无关的新需求，应该另建任务。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"task":{"type":"string","description":"任务 id"},"text":{"type":"string","description":"要转达给 agent 的话，写清楚"}
		},"required":["task","text"]}`),
	}
}

func (t runMessageTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct{ Task, Text string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	r, err := t.e.activeRun(ctx, a.Task)
	if err != nil {
		return "", err
	}
	if err := t.e.Nodes.Message(ctx, r.ID, a.Text); err != nil {
		return "", err
	}
	return "已转达给 " + r.Agent, nil
}

type runCancelTool struct{ e *Engine }

func (runCancelTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "run_cancel",
		Description: "停止任务当前正在执行的 Run，任务回到“已计划”，之后可以重新派发。要彻底放弃任务用 task_cancel。",
		Schema:      json.RawMessage(`{"type":"object","properties":{"task":{"type":"string"}},"required":["task"]}`),
	}
}

func (t runCancelTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct{ Task string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	r, err := t.e.activeRun(ctx, a.Task)
	if err != nil {
		return "", err
	}
	if err := t.e.Nodes.Cancel(ctx, r.ID, "主管取消"); err != nil {
		return "", err
	}
	return "已请求停止", nil
}

func (e *Engine) activeRun(ctx context.Context, taskID string) (store.Run, error) {
	runs, err := e.store().Runs(ctx, store.RunFilter{TaskID: taskID, Statuses: []string{event.RunStatusRunning, event.RunStatusQueued}})
	if err != nil {
		return store.Run{}, err
	}
	if len(runs) == 0 {
		return store.Run{}, fmt.Errorf("任务 %s 现在没有正在执行的 Run", taskID)
	}
	return runs[len(runs)-1], nil
}

func (e *Engine) findProject(ctx context.Context, ref string) (store.Project, error) {
	ps, err := e.store().Projects(ctx)
	if err != nil {
		return store.Project{}, err
	}
	for _, p := range ps {
		if p.ID == ref || strings.EqualFold(p.Name, strings.TrimSpace(ref)) {
			return p, nil
		}
	}
	return store.Project{}, fmt.Errorf("%w: 没有叫 %q 的项目；可用的项目见环境说明，或者请用户在“项目”页面登记", ErrInvalid, ref)
}

func (e *Engine) findNode(ctx context.Context, ref string) (string, error) {
	nodes, err := e.store().Nodes(ctx)
	if err != nil {
		return "", err
	}
	for _, n := range nodes {
		if n.ID == ref || strings.EqualFold(n.Name, strings.TrimSpace(ref)) {
			return n.ID, nil
		}
	}
	return "", fmt.Errorf("%w: 没有叫 %q 的机器", ErrInvalid, ref)
}

func describe(t store.Task) string {
	s := fmt.Sprintf("任务 %s「%s」，%s", t.ID, t.Title, statusName[t.Status])
	if t.StatusReason != "" {
		s += "（" + t.StatusReason + "）"
	}
	if t.PRURL != "" {
		s += "，PR " + t.PRURL
	}
	return s
}

// Environment describes projects and the open tasks of a thread for the
// supervisor's prompt, one line each.
func (e *Engine) Environment(ctx context.Context, threadID string) []string {
	var out []string
	ps, _ := e.store().Projects(ctx)
	nodes, _ := e.store().Nodes(ctx)
	name := map[string]string{}
	for _, n := range nodes {
		name[n.ID] = n.Name
	}
	for _, p := range ps {
		var where []string
		for _, n := range p.Nodes {
			where = append(where, fmt.Sprintf("%s:%s", name[n.NodeID], n.Path))
		}
		line := fmt.Sprintf("项目 %s（id %s）：%s", p.Name, p.ID, strings.Join(where, " > "))
		if p.DefaultAgent != "" {
			line += "，默认 agent " + p.DefaultAgent
		}
		out = append(out, line)
	}
	if len(ps) == 0 {
		out = append(out, "还没有登记任何项目；代码任务需要先请用户在“项目”页面登记项目和它在哪台电脑的哪个路径。")
	}
	tasks, _ := e.store().Tasks(ctx, store.TaskFilter{ThreadID: threadID, Statuses: openStatuses, Limit: 20})
	for _, t := range tasks {
		line := "这个对话里的" + describe(t)
		if r, err := e.activeRun(ctx, t.ID); err == nil {
			line += fmt.Sprintf("，%s 正在 %s 上执行（用户对它的补充用 run_message 转给 agent）", r.Agent, name[r.NodeID])
		}
		out = append(out, line)
	}
	return out
}
