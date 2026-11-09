package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	projectkey "github.com/XiaoXianThis/Sous/apps/core/internal/project"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/router"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

// Tools returns the supervisor's task tools.
func (e *Engine) Tools() []tool.Tool {
	return []tool.Tool{createTool{e}, updateTool{e}, cancelTool{e}, dispatchTool{e}, listTool{e}, runMessageTool{e}, runCancelTool{e},
		projectPutTool{e}, projectCreateTool{e}, projectCloneTool{e}, projectArchiveTool{e},
		reviewTool{e}, verdictTool{e},
		gitStatusTool{e}, gitTool{e, proto.GitCommit}, gitTool{e, proto.GitMerge}, gitTool{e, proto.GitPush}, gitTool{e, proto.GitPR},
		gitTool{e, proto.GitUndo}, gitTool{e, proto.GitDiscard}, gitTool{e, proto.GitRestore},
		consultTool{e}, projectReadTool{e}, newLookTool(e), resultTool{e}}
}

type createTool struct{ e *Engine }

func (createTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "task_create",
		Description: "创建一个任务，交给一台电脑（开发机或服务器）上的编程 agent 去做（写代码、改文件、跑命令、部署，或者需要通读代码、查资料的调研）。只在用户明确要你动手、范围也清楚时用；拿不准就先讨论。" +
			"在哪儿做（place）、在 worktree 里做时从哪个分支开始（base_branch + base_from）按这次的场景定。" +
			"不知道用户指的是哪个分支时，先用 git_status 看项目现在的状态，还拿不准就问用户。" +
			"place：worktree 在任务自己的分支和 worktree 里做（可以并行，改动在分支上等用户决定怎么处理）；in_place 直接在项目目录里改（开始前拍快照，可以撤销；同一目录同时只跑一个）；scratch 在临时目录里做，不涉及项目。" +
			"谁来做：preset 选一个预设（agent + 模型，各有定位，见环境说明），或者直接给 agent、model、effort；都不填按项目和机器的默认。" +
			"review 不填就是不验收，agent 的总结就是汇报；只有改动大、风险高，或者用户要求时才验收：model 由验收模型看 diff，agent 由验收 agent 在目录里只读地跑测试，user 交给用户。调研、提问、小改动不要验收。" +
			"任务做完不会自动提交、合并、推送或开 PR，这些等用户说了再用 git_* 工具做。detail 要写清楚目标、范围和完成标准，agent 看不到这段对话。" +
			"讨论是和项目顾问（consult）进行的、现在要动手时，from_consult=true 让读过代码的顾问会话接着做。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"title":{"type":"string","description":"一句话标题"},
			"detail":{"type":"string","description":"完整的任务说明"},
			"project":{"type":"string","description":"项目名或 id；不涉及项目时不填（scratch）"},
			"place":{"type":"string","enum":["worktree","in_place","scratch"],"description":"在哪儿做"},
			"review":{"type":"string","enum":["none","model","agent","user"],"description":"怎么验收；不填就是不验收"},
			"on_fail":{"type":"string","enum":["rework","ask"],"description":"验收不通过时：rework 照意见返工（不填就是它）；ask 停下来由你或用户决定"},
			"base_branch":{"type":"string","description":"place=worktree：任务分支从哪个分支开始（看 git_status 和对话判断，常常是用户正在用的当前分支）"},
			"base_from":{"type":"string","enum":["local","remote"],"description":"place=worktree：local 用本地分支（含没推送的提交），remote 先 fetch 再用 origin 上的"},
			"kind":{"type":"string","enum":["code","general","ops"],"description":"标签：代码、一般、运维（部署和维护服务器）；不填按 place"},
			"preset":{"type":"string","description":"预设名或 id（agent + 模型，见环境说明里的预设和它们能在哪些电脑上用）；不填用项目的默认预设"},
			"model":{"type":"string","description":"这次用的模型，覆盖预设的模型；只能填环境说明里这个 agent 在那台电脑上列出的模型"},
			"effort":{"type":"string","description":"这次的思考强度，覆盖预设的设置（agent 自己的取值，比如 low / medium / high）"},
			"from_consult":{"type":"boolean","description":"把这个项目和顾问的讨论会话交给这个任务"},
			"dispatch":{"type":"boolean","description":"是否立刻派发，默认 true"},
			"agent":{"type":"string","description":"指定 agent（环境说明里电脑上启用的 agent）；不填按预设、项目、电脑和全局的默认"},
			"machine":{"type":"string","description":"指定机器名或 id；不填按项目的机器优先级（运维任务优先服务器，其余优先开发机，没有就用别的并在结果里说明）"},
			"permission_mode":{"type":"string","enum":["default","auto","open"],"description":"只在用户明确要求时设置；不填用项目或全局默认"},
			"priority":{"type":"integer","description":"越大越优先，默认 0"},
			"parent":{"type":"string","description":"父任务 id，用于拆分子任务"},
			"depends_on":{"type":"array","items":{"type":"string"},"description":"要等这些任务完成后才开始"}
		},"required":["title","detail","place"]}`),
	}
}

func (t createTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Title, Detail, Project, Agent, Machine, Parent string
		Kind, Persona, Preset, Place, Review           string
		Model, Effort                                  string
		OnFail                                         string `json:"on_fail"`
		BaseBranch                                     string `json:"base_branch"`
		BaseFrom                                       string `json:"base_from"`
		FromConsult                                    bool   `json:"from_consult"`
		PermissionMode                                 string `json:"permission_mode"`
		Dispatch                                       *bool
		Priority                                       int
		DependsOn                                      []string `json:"depends_on"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	in := Input{ThreadID: tool.TurnFrom(ctx).ThreadID, Title: a.Title, Detail: a.Detail, Agent: a.Agent, Mode: a.PermissionMode,
		Priority: a.Priority, ParentID: a.Parent, DependsOn: a.DependsOn, Dispatch: a.Dispatch == nil || *a.Dispatch, Actor: event.ActorSupervisor,
		Kind: a.Kind, Place: a.Place, Review: a.Review, OnFail: a.OnFail, BaseBranch: a.BaseBranch, BaseFrom: a.BaseFrom, FromConsult: a.FromConsult,
		Options: map[string]string{"model": a.Model, "effort": a.Effort}}
	if preset := firstNonEmpty(a.Preset, a.Persona); preset != "" {
		p, err := t.e.findPersona(ctx, preset)
		if err != nil {
			return "", err
		}
		in.Persona = p.ID
	}
	if a.Project != "" {
		p, err := t.e.findProject(ctx, a.Project)
		if err != nil {
			return "", err
		}
		in.ProjectID = p.ID
	} else if pid := scopeOf(ctx); pid != "" {
		in.ProjectID = pid // a project chat's tasks belong to its project
	}
	if a.Parent != "" {
		if err := t.e.checkScope(ctx, a.Parent); err != nil {
			return "", err
		}
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
	return "已创建：" + describe(task) + t.e.whereNote(ctx, task.ID), nil
}

type updateTool struct{ e *Engine }

func (updateTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "task_update",
		Description: "修改任务：标题、说明、优先级、指定的 agent 或机器、依赖、在哪儿做和怎么验收；或者把状态改成 inbox / planned / blocked / done。只填要改的字段。" +
			"任务正在执行时，改说明会把新说明转给 agent；只是补充一两句要求时，用 run_message 更直接。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"id":{"type":"string"},
			"title":{"type":"string"},"detail":{"type":"string"},"priority":{"type":"integer"},
			"agent":{"type":"string"},"machine":{"type":"string"},
			"place":{"type":"string","enum":["worktree","in_place","scratch"]},
			"review":{"type":"string","enum":["none","model","agent","user"]},
			"on_fail":{"type":"string","enum":["rework","ask"]},
			"base_branch":{"type":"string"},"base_from":{"type":"string","enum":["local","remote"]},
			"preset":{"type":"string","description":"预设名或 id；空字符串清除"},
			"model":{"type":"string","description":"这次用的模型；空字符串改回预设的"},
			"effort":{"type":"string","description":"思考强度；空字符串改回预设的"},
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
		Persona, Preset, Place, Review, Model, Effort     *string
		OnFail                                            *string `json:"on_fail"`
		BaseBranch                                        *string `json:"base_branch"`
		BaseFrom                                          *string `json:"base_from"`
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
	if err := t.e.checkScope(ctx, *a.ID); err != nil {
		return "", err
	}
	f := event.TaskFields{Title: a.Title, Detail: a.Detail, Priority: a.Priority, AgentPref: a.Agent, PermissionMode: a.PermissionMode,
		Place: a.Place, Review: a.Review, OnFail: a.OnFail, BaseBranch: a.BaseBranch, BaseFrom: a.BaseFrom}
	if a.Preset != nil {
		a.Persona = a.Preset
	}
	if a.Model != nil || a.Effort != nil {
		cur, err := t.e.store().Task(ctx, *a.ID)
		if err != nil {
			return "", err
		}
		m := map[string]string{}
		for k, v := range cur.Options {
			m[k] = v
		}
		if a.Model != nil {
			m["model"] = *a.Model
		}
		if a.Effort != nil {
			m["effort"] = *a.Effort
		}
		f.Options = &m
	}
	if a.Persona != nil {
		id := ""
		if *a.Persona != "" {
			p, err := t.e.findPersona(ctx, *a.Persona)
			if err != nil {
				return "", err
			}
			id = p.ID
		}
		f.PersonaID = &id
	}
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
	if err := t.e.checkScope(ctx, a.ID); err != nil {
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
		Description: "派发一个还没开始、失败了或受阻的任务（也用于重试、返工）。可以换 agent 或换机器；不填就按任务原来的设置。" +
			"任务要先定下在哪儿做和怎么验收（用 task_update）。没有可用机器时任务会排队，机器上线后自动开始。",
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
	if err := t.e.checkScope(ctx, a.ID); err != nil {
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
	return describe(task) + t.e.whereNote(ctx, task.ID), nil
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
	tasks, err := t.e.store().Tasks(ctx, store.TaskFilter{Statuses: a.Status, ProjectID: scopeOf(ctx), Limit: 50})
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
		Description: "把补充说明转给正在执行的任务（agent 在当前这一步结束后会收到）。" +
			"用户在任务执行期间补充要求、纠正方向时用它；和正在执行的任务无关的新需求，应该另建任务。" +
			"agent 用 ask_supervisor 提问、正停着等回答时（【agent 的问题】），用它回答，会立刻送到。",
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
	if err := t.e.checkScope(ctx, a.Task); err != nil {
		return "", err
	}
	r, err := t.e.activeRun(ctx, a.Task)
	if err != nil {
		return "", err
	}
	if err := t.e.Nodes.MessageAs(ctx, r.ID, a.Text, event.ActorSupervisor); err != nil {
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
	if err := t.e.checkScope(ctx, a.Task); err != nil {
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

// whereNote says which machine and agent took a task's run, for the
// supervisor's tool results.
func (e *Engine) whereNote(ctx context.Context, taskID string) string {
	r, err := e.activeRun(ctx, taskID)
	if err != nil || r.NodeID == "" {
		return ""
	}
	name := r.NodeID
	if n, err := e.store().Node(ctx, r.NodeID); err == nil && n.Name != "" {
		name = n.Name
	}
	if r.Status == event.RunStatusQueued {
		return fmt.Sprintf("；Run %s 已排队，目标 %s / %s，尚未收到客户端启动确认", r.ID, name, r.Agent)
	}
	return fmt.Sprintf("；Run %s 已收到客户端启动确认，在 %s 上由 %s 执行", r.ID, name, r.Agent)
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

// scopeOf is the project a project chat's turn is limited to, or "".
func scopeOf(ctx context.Context) string { return tool.TurnFrom(ctx).ProjectID }

// errOutOfScope refuses work on another project from a project chat.
var errOutOfScope = fmt.Errorf("%w: 这是项目对话，只能处理这个项目；别的项目请用户去主对话或那个项目的对话里说", ErrInvalid)

// checkScope refuses a task outside the project chat's project.
func (e *Engine) checkScope(ctx context.Context, taskID string) error {
	pid := scopeOf(ctx)
	if pid == "" {
		return nil
	}
	t, err := e.store().Task(ctx, taskID)
	if err != nil {
		return fmt.Errorf("%w: 没有任务 %s", ErrInvalid, taskID)
	}
	if t.ProjectID != pid {
		return errOutOfScope
	}
	return nil
}

func (e *Engine) findProject(ctx context.Context, ref string) (store.Project, error) {
	ps, err := e.store().Projects(ctx)
	if err != nil {
		return store.Project{}, err
	}
	for _, p := range ps {
		if p.ID == ref || strings.EqualFold(p.Name, strings.TrimSpace(ref)) {
			if pid := scopeOf(ctx); pid != "" && p.ID != pid {
				return store.Project{}, errOutOfScope
			}
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
	var attrs []string
	if t.Place != "" {
		attrs = append(attrs, placeShort[t.Place])
	}
	if t.Place == event.PlaceWorktree && t.Branch != "" {
		attrs = append(attrs, "分支 "+t.Branch)
	} else if t.Place == event.PlaceWorktree && t.BaseBranch != "" {
		attrs = append(attrs, "从 "+t.BaseFrom+" 的 "+t.BaseBranch+" 开始")
	}
	if t.Review != "" && t.Review != event.ReviewNone {
		attrs = append(attrs, "验收："+reviewShort[t.Review])
	}
	if m := t.Options["model"]; m != "" {
		attrs = append(attrs, "模型 "+m)
	}
	if v := t.Options["effort"]; v != "" {
		attrs = append(attrs, "思考强度 "+v)
	}
	if t.GitState != "" {
		attrs = append(attrs, "分支"+gitStateName[t.GitState])
	}
	if len(attrs) > 0 {
		s += "；" + strings.Join(attrs, "，")
	}
	if t.PRURL != "" {
		s += "，PR " + t.PRURL
	}
	return s
}

var placeShort = map[string]string{event.PlaceWorktree: "在 worktree 里", event.PlaceInPlace: "就地", event.PlaceScratch: "临时目录"}
var reviewShort = map[string]string{event.ReviewNone: "不验收", event.ReviewModel: "模型看 diff", event.ReviewAgent: "验收 agent 跑测试", event.ReviewUser: "用户"}

// Environment describes personas, projects and the open tasks of a thread
// for the supervisor's prompt, one line each.
func (e *Engine) Environment(ctx context.Context, threadID string) []string {
	var out []string
	scope := ""
	if th, err := e.store().Thread(ctx, threadID); err == nil {
		scope = th.ProjectID
	}
	personas, _ := e.store().Personas(ctx)
	nodes, _ := e.store().Nodes(ctx)
	name := map[string]string{}
	for _, n := range nodes {
		name[n.ID] = n.Name
	}
	pname := map[string]string{}
	for _, p := range personas {
		pname[p.ID] = p.Name
		if p.ID == reviewerID {
			continue // it reviews; tasks are not given to it
		}
		out = append(out, e.presetLine(ctx, p, nodes))
	}
	ps, _ := e.store().Projects(ctx)
	var archived []string
	active := 0
	for _, p := range ps {
		if scope != "" && p.ID != scope {
			continue // a project chat sees only its project
		}
		if p.ID == projectkey.MiscID && scope == "" {
			continue
		}
		if p.Archived() && scope == "" {
			archived = append(archived, fmt.Sprintf("%s（id %s）", p.Name, p.ID))
			continue
		}
		active++
		var where []string
		for _, n := range p.Nodes {
			where = append(where, fmt.Sprintf("%s:%s%s", name[n.NodeID], n.Path, GitNote(n.Git)))
		}
		loc := strings.Join(where, " > ")
		if loc == "" {
			loc = "还不在任何机器上（可以用 project_clone 放到一台电脑上）"
		}
		line := fmt.Sprintf("项目 %s（id %s）：%s", p.Name, p.ID, loc)
		if scope != "" && p.Notes != "" {
			line += "，说明：" + p.Notes
		}
		if p.Layout == projectkey.LayoutWokspace {
			line += "，wokspace 结构"
		}
		if c := e.ConsultState(ctx, p.ID); c != "" {
			line += "，" + c
		}
		if p.DefaultPersona != "" {
			line += "，默认预设 " + pname[p.DefaultPersona]
		}
		if p.DefaultAgent != "" {
			line += "，默认 agent " + p.DefaultAgent
		}
		if d := p.Deploy; d != nil {
			line += fmt.Sprintf("，部署到 %s:%s（%s）", name[d.NodeID], d.Path, d.Command)
			if d.AutoOnMerge && d.Branch != "" {
				line += "，" + d.Branch + " 有新提交时自动部署"
			}
		}
		out = append(out, line)
	}
	if scope != "" {
		return append(out, e.scopedTasks(ctx, scope, name)...)
	}
	if len(archived) > 0 {
		out = append(out, fmt.Sprintf("已归档的项目（用户没有明确提到时不要管它们，也不要往里派任务；在已归档的项目里建任务会让它重新激活）：%s", strings.Join(archived, "、")))
	}
	if active == 0 && len(archived) == 0 {
		out = append(out, "还没有登记任何项目；可以用 project_put 登记已有的仓库，或者用 project_create 新建一个。")
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

// scopedTasks lists a project's open tasks for a project chat.
func (e *Engine) scopedTasks(ctx context.Context, projectID string, nodeName map[string]string) []string {
	tasks, _ := e.store().Tasks(ctx, store.TaskFilter{ProjectID: projectID, Statuses: openStatuses, Limit: 30})
	if len(tasks) == 0 {
		return []string{"这个项目现在没有未关闭的任务"}
	}
	var out []string
	for _, t := range tasks {
		line := "这个项目里的" + describe(t)
		if r, err := e.activeRun(ctx, t.ID); err == nil {
			line += fmt.Sprintf("，%s 正在 %s 上执行（用户对它的补充用 run_message 转给 agent）", r.Agent, nodeName[r.NodeID])
		}
		out = append(out, line)
	}
	return out
}

// presetLine describes a preset for the supervisor: what it runs, what it
// is for, and on which machines its agent can run now (decision 0024).
func (e *Engine) presetLine(ctx context.Context, p store.Persona, nodes []store.Node) string {
	line := fmt.Sprintf("预设 %s（id %s）", p.Name, p.ID)
	what := firstNonEmpty(p.Agent, "电脑的默认 agent")
	if v := presetModel(p.Options); v != "" {
		what += " + " + v
	}
	line += "：" + what
	if b := strings.TrimSpace(p.Brief); b != "" {
		line += "，定位：" + oneLine(b, 120)
	}
	if p.Agent == "" {
		return line
	}
	var on, off []string
	for _, n := range nodes {
		if !router.Usable(parseAgents(n.Agents), p.Agent) {
			continue
		}
		if e.Nodes.Online(n.ID) {
			on = append(on, n.Name)
		} else {
			off = append(off, n.Name)
		}
	}
	switch {
	case len(on) > 0:
		line += "；能在 " + strings.Join(on, "、") + " 上用"
	case len(off) > 0:
		line += "；装了它的 " + strings.Join(off, "、") + " 现在离线"
	default:
		line += "；现在没有电脑能用它（没装、没启用或没登录），换别的预设或直接指定 agent"
	}
	return line
}

// presetModel is the model a preset's options name, if any: the "model"
// key, or an option id that says model.
func presetModel(opts map[string]string) string {
	if v := opts["model"]; v != "" {
		return v
	}
	for k, v := range opts {
		if strings.Contains(strings.ToLower(k), "model") && v != "" {
			return v
		}
	}
	return ""
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func kindNames(kinds []string) []string {
	out := []string{}
	for _, k := range kinds {
		out = append(out, map[string]string{event.TaskKindCode: "代码任务", event.TaskKindGeneral: "一般任务", event.TaskKindOps: "运维任务"}[k])
	}
	if len(out) == 0 {
		out = append(out, "各种任务")
	}
	return out
}

type projectPutTool struct{ e *Engine }

func (projectPutTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "project_put",
		Description: "登记或修改一个项目。登记已有的仓库给 repo_url（同一个仓库在各台机器的工作空间里会被自动找到）；" +
			"修改时给 id，只填要改的字段。部署目标（deploy）要是一台服务器上的目录加部署命令。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"id":{"type":"string","description":"要修改的项目名或 id；新建时不填"},
			"name":{"type":"string"},
			"repo_url":{"type":"string","description":"git 远端地址"},
			"notes":{"type":"string","description":"项目说明，会写进 agent 的提示里"},
			"default_persona":{"type":"string","description":"默认预设名或 id"},
			"deploy":{"type":"object","properties":{
				"machine":{"type":"string","description":"服务器名或 id"},
				"path":{"type":"string"},"command":{"type":"string"},"health":{"type":"string"},
				"branch":{"type":"string","description":"部署分支：origin 上这个分支有新提交时就是要部署的东西"},
				"auto_on_merge":{"type":"boolean","description":"部署分支有新提交时自动部署（要有 branch）"}}}
		}}`),
	}
}

func (t projectPutTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		ID             *string `json:"id"`
		Name           *string `json:"name"`
		RepoURL        *string `json:"repo_url"`
		Notes          *string `json:"notes"`
		DefaultPersona *string `json:"default_persona"`
		Deploy         *struct {
			Machine, Path, Command, Health, Branch string
			AutoOnMerge                            bool `json:"auto_on_merge"`
		} `json:"deploy"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	in := ProjectInput{Name: a.Name, RepoURL: a.RepoURL, Notes: a.Notes}
	if a.ID != nil && *a.ID != "" {
		p, err := t.e.findProject(ctx, *a.ID)
		if err != nil {
			return "", err
		}
		in.ID = p.ID
	} else if scopeOf(ctx) != "" {
		return "", errOutOfScope
	} else if a.Name == nil {
		return "", fmt.Errorf("%w: 新项目需要 name", ErrInvalid)
	}
	if a.DefaultPersona != nil {
		id := ""
		if *a.DefaultPersona != "" {
			p, err := t.e.findPersona(ctx, *a.DefaultPersona)
			if err != nil {
				return "", err
			}
			id = p.ID
		}
		in.DefaultPersona = &id
	}
	if a.Deploy != nil {
		var d *event.Deploy
		if a.Deploy.Machine != "" {
			id, err := t.e.findNode(ctx, a.Deploy.Machine)
			if err != nil {
				return "", err
			}
			if a.Deploy.AutoOnMerge && strings.TrimSpace(a.Deploy.Branch) == "" {
				return "", fmt.Errorf("%w: 自动部署要说明部署分支（deploy.branch）", ErrInvalid)
			}
			d = &event.Deploy{NodeID: id, Path: a.Deploy.Path, Command: a.Deploy.Command, Health: a.Deploy.Health,
				Branch: strings.TrimSpace(a.Deploy.Branch), AutoOnMerge: a.Deploy.AutoOnMerge}
		}
		in.Deploy = &d
	}
	p, err := t.e.PutProject(ctx, in, event.ActorSupervisor)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("已保存项目 %s（id %s），在 %d 台机器上", p.Name, p.ID, len(p.Nodes)), nil
}

type projectArchiveTool struct{ e *Engine }

func (projectArchiveTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "project_archive",
		Description: "归档一个项目，或者把已归档的项目重新激活（archived=false）。归档的项目不出现在工作台上，平时不用管；" +
			"只在用户明确要求时使用。还有正在执行的任务的项目不能归档。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"project":{"type":"string","description":"项目名或 id"},
			"archived":{"type":"boolean","description":"true 归档，false 激活"}
		},"required":["project","archived"]}`),
	}
}

func (t projectArchiveTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Project  string `json:"project"`
		Archived bool   `json:"archived"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	p, err := t.e.findProject(ctx, a.Project)
	if err != nil {
		return "", err
	}
	if p, err = t.e.ArchiveProject(ctx, p.ID, a.Archived, event.ActorSupervisor); err != nil {
		return "", err
	}
	if a.Archived {
		return fmt.Sprintf("已归档项目 %s（id %s）", p.Name, p.ID), nil
	}
	return fmt.Sprintf("已激活项目 %s（id %s），它回到了工作台上", p.Name, p.ID), nil
}

type projectCreateTool struct{ e *Engine }

func (projectCreateTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "project_create",
		Description: "新建一个项目：在一台开发机的工作空间里创建仓库。结构（layout）按用户的说法或记下的习惯定：wokspace 用 wok init 建 wokspace 结构，plain 只 git init；不知道就先问用户。" +
			"会请用户批准要执行的命令。owner 不为空时同时创建 GitHub 仓库并 push（也要用户批准）。完成后会在对话里汇报。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"name":{"type":"string","description":"项目名：小写字母、数字和连字符，比如 my-app"},
			"machine":{"type":"string","description":"电脑名或 id；不填选第一台能用的开发机，放到服务器上要写明"},
			"layout":{"type":"string","enum":["wokspace","plain"],"description":"wokspace：wok init；plain：只 git init"},
			"owner":{"type":"string","description":"GitHub 用户或组织；不填不建远端仓库"},
			"private":{"type":"boolean","description":"GitHub 仓库是否私有，默认 true"}
		},"required":["name","layout"]}`),
	}
}

func (t projectCreateTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Name, Machine, Layout, Owner string
		Private                      *bool
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if scopeOf(ctx) != "" {
		return "", errOutOfScope
	}
	in := SetupInput{Name: a.Name, Layout: a.Layout, Owner: a.Owner, Private: a.Private == nil || *a.Private,
		ThreadID: tool.TurnFrom(ctx).ThreadID, Actor: event.ActorSupervisor}
	if a.Machine != "" {
		id, err := t.e.findNode(ctx, a.Machine)
		if err != nil {
			return "", err
		}
		in.NodeID = id
	}
	p, r, err := t.e.CreateProject(ctx, in)
	if err != nil {
		return "", err
	}
	msg := "，完成后会自动汇报"
	if in.Owner != "" {
		msg = "；创建 GitHub 仓库那一步要用户批准，完成后会自动汇报"
	}
	return fmt.Sprintf("已登记项目 %s（id %s），正在机器上创建（Run %s）%s。", p.Name, p.ID, r.ID, msg), nil
}

type projectCloneTool struct{ e *Engine }

func (projectCloneTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "project_clone",
		Description: "把一个已登记、有仓库地址的项目 clone 到一台电脑的工作空间里，让它也能在那台电脑上干活：开发机，或者设了工作空间的服务器（比如要在服务器上直接部署）。会请用户批准。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"project":{"type":"string","description":"项目名或 id"},
			"machine":{"type":"string","description":"电脑名或 id；不填选第一台能用的开发机，放到服务器上要写明"}
		},"required":["project"]}`),
	}
}

func (t projectCloneTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct{ Project, Machine string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	p, err := t.e.findProject(ctx, a.Project)
	if err != nil {
		return "", err
	}
	in := SetupInput{ProjectID: p.ID, ThreadID: tool.TurnFrom(ctx).ThreadID, Actor: event.ActorSupervisor}
	if a.Machine != "" {
		if in.NodeID, err = t.e.findNode(ctx, a.Machine); err != nil {
			return "", err
		}
	}
	r, err := t.e.CloneProject(ctx, in)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("正在把项目 %s clone 到机器上（Run %s）；git clone 要用户批准，完成后会自动汇报。", p.Name, r.ID), nil
}

// GitNote describes where a checkout stands, for the supervisor:
// "（main，落后 3、2 个文件未提交）"; empty when nothing is notable.
func GitNote(g *proto.GitState) string {
	if g == nil {
		return ""
	}
	var parts []string
	if g.Behind > 0 {
		parts = append(parts, fmt.Sprintf("落后远端 %d 个提交", g.Behind))
	}
	if g.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("%d 个提交没推送", g.Ahead))
	}
	if n := g.Changes + g.Untracked; n > 0 {
		parts = append(parts, fmt.Sprintf("%d 个文件未提交", n))
	}
	if len(parts) == 0 {
		return ""
	}
	return "（" + strings.Join(parts, "、") + "）"
}
