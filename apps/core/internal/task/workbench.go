package task

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	projectkey "github.com/XiaoXianThis/Sous/apps/core/internal/project"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// Milestone M5 (work/specs/m5-workbench): personas, projects as first-class
// entities that sous creates and clones, operations tasks and deploying
// after a pull request is merged.

// settingSeeded records that the built-in personas were created once, so
// deleting one is not undone at the next start.
const settingSeeded = "personas_seeded"

// BuiltinPersonas are created on the first start.
var BuiltinPersonas = []event.PersonaPayload{
	{PersonaID: "backend", Name: "后端", Glyph: "后", Color: "purple", Kinds: []string{event.TaskKindCode},
		Brief: "你负责后端和基础设施代码：接口、数据、性能和测试。改动要有测试，跑通现有测试再结束。"},
	{PersonaID: "frontend", Name: "前端", Glyph: "前", Color: "teal", Kinds: []string{event.TaskKindCode},
		Brief: "你负责前端界面：组件、样式、交互和可访问性。注意窄屏和深色模式，改完跑一遍构建和测试。"},
	{PersonaID: "reviewer", Name: "审查", Glyph: "审", Color: "coral", Kinds: []string{event.TaskKindCode, event.TaskKindGeneral},
		Brief: "你负责审查和排查问题：先读懂现状再动手，给出有依据的结论；只做必要的最小改动。"},
	{PersonaID: "ops", Name: "运维", Glyph: "运", Color: "amber", Kinds: []string{event.TaskKindOps, event.TaskKindGeneral},
		Brief: "你负责部署和维护服务器：按项目的部署脚本执行，每一步都确认结果；不确定时停下来说明，不要冒险。"},
	{PersonaID: "assistant", Name: "助理", Glyph: "助", Color: "gray", Kinds: []string{event.TaskKindGeneral},
		Brief: "你负责一般事务：查资料、整理文件、处理数据。说清楚做了什么、结果在哪里。"},
}

// Seed makes sure the built-in "misc" project exists, and creates the
// built-in personas the first time the Hub starts. Both go through the
// event log like any other change.
func (e *Engine) Seed(ctx context.Context) error {
	if _, err := e.store().Project(ctx, projectkey.MiscID); errors.Is(err, store.ErrNotFound) {
		if _, err := e.Log.Append(ctx, event.New(event.ProjectPut, event.ActorSystem, "", event.ProjectPayload{
			ProjectID: projectkey.MiscID, Name: "杂项", Notes: "不属于任何仓库的事：查资料、整理文件、看机器状态。",
		})); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if v, _ := e.store().Setting(ctx, settingSeeded); v != "" {
		return nil
	}
	have, err := e.store().Personas(ctx)
	if err != nil {
		return err
	}
	if len(have) == 0 {
		var evs []event.Event
		for _, p := range BuiltinPersonas {
			p.Builtin = true
			evs = append(evs, event.New(event.PersonaPut, event.ActorSystem, "", p))
		}
		if _, err := e.Log.Append(ctx, evs...); err != nil {
			return err
		}
	}
	return e.store().SetSetting(ctx, settingSeeded, "1")
}

// PersonaInput creates or changes a persona.
type PersonaInput struct {
	ID          string
	Name        string
	Glyph       string
	Color       string
	Agent       string
	Options     map[string]string
	Brief       string
	DefaultMode string
	Kinds       []string
}

var personaColors = map[string]bool{"purple": true, "teal": true, "coral": true, "pink": true, "blue": true, "green": true, "amber": true, "gray": true}

// PutPersona validates and stores a persona.
func (e *Engine) PutPersona(ctx context.Context, in PersonaInput, actor string) (store.Persona, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return store.Persona{}, fmt.Errorf("%w: 岗位需要名字", ErrInvalid)
	}
	if in.Glyph = strings.TrimSpace(in.Glyph); in.Glyph == "" {
		in.Glyph = string([]rune(in.Name)[:1])
	}
	if r := []rune(in.Glyph); len(r) > 2 {
		in.Glyph = string(r[:2])
	}
	if in.Color == "" {
		in.Color = "gray"
	}
	if !personaColors[in.Color] {
		return store.Persona{}, fmt.Errorf("%w: 不认识的颜色 %s", ErrInvalid, in.Color)
	}
	if err := validMode(in.DefaultMode); err != nil {
		return store.Persona{}, err
	}
	for _, k := range in.Kinds {
		if k != event.TaskKindCode && k != event.TaskKindGeneral && k != event.TaskKindOps {
			return store.Persona{}, fmt.Errorf("%w: 任务类型只能是 code、general 或 ops", ErrInvalid)
		}
	}
	builtin := false
	ps, _ := e.store().Personas(ctx)
	for _, p := range ps {
		if p.ID != in.ID && strings.EqualFold(p.Name, in.Name) {
			return store.Persona{}, fmt.Errorf("%w: 已经有叫「%s」的岗位", ErrConflict, in.Name)
		}
		if p.ID == in.ID {
			builtin = p.Builtin
		}
	}
	if in.ID == "" {
		in.ID = ids.New()
	}
	opts := map[string]string{}
	for k, v := range in.Options {
		if k = strings.TrimSpace(k); k != "" && strings.TrimSpace(v) != "" {
			opts[k] = strings.TrimSpace(v)
		}
	}
	if _, err := e.Log.Append(ctx, event.New(event.PersonaPut, actor, "", event.PersonaPayload{
		PersonaID: in.ID, Name: in.Name, Glyph: in.Glyph, Color: in.Color, Agent: strings.TrimSpace(in.Agent), Options: opts,
		Brief: strings.TrimSpace(in.Brief), DefaultMode: in.DefaultMode, Kinds: in.Kinds, Builtin: builtin,
	})); err != nil {
		return store.Persona{}, err
	}
	return e.store().Persona(ctx, in.ID)
}

// DeletePersona removes a persona that no open task uses.
func (e *Engine) DeletePersona(ctx context.Context, id, actor string) error {
	if _, err := e.store().Persona(ctx, id); err != nil {
		return err
	}
	open, _ := e.store().Tasks(ctx, store.TaskFilter{Statuses: openStatuses})
	for _, t := range open {
		if t.PersonaID == id {
			return fmt.Errorf("%w: 任务「%s」还在用这个岗位", ErrConflict, t.Title)
		}
	}
	_, err := e.Log.Append(ctx, event.New(event.PersonaDeleted, actor, "", event.PersonaPayload{PersonaID: id}))
	return err
}

// findPersona finds a persona by id or name.
func (e *Engine) findPersona(ctx context.Context, ref string) (store.Persona, error) {
	ps, err := e.store().Personas(ctx)
	if err != nil {
		return store.Persona{}, err
	}
	for _, p := range ps {
		if p.ID == ref || strings.EqualFold(p.Name, strings.TrimSpace(ref)) {
			return p, nil
		}
	}
	return store.Persona{}, fmt.Errorf("%w: 没有叫 %q 的岗位；可用的岗位见环境说明", ErrInvalid, ref)
}

// ProjectInput creates or changes a project. Nil fields keep their value
// when changing a project.
type ProjectInput struct {
	ID             string
	Name           *string
	RepoURL        *string
	DefaultMode    *string
	DefaultAgent   *string
	DefaultPersona *string
	Notes          *string
	Layout         *string
	Nodes          *[]event.ProjectNode
	Deploy         **event.Deploy
}

// PutProject validates and stores a project.
func (e *Engine) PutProject(ctx context.Context, in ProjectInput, actor string) (store.Project, error) {
	var p store.Project
	if in.ID != "" {
		var err error
		if p, err = e.store().Project(ctx, in.ID); err != nil {
			return store.Project{}, err
		}
	} else {
		in.ID = ids.New()
	}
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = strings.TrimSpace(*src)
		}
	}
	set(&p.Name, in.Name)
	set(&p.RepoURL, in.RepoURL)
	set(&p.DefaultMode, in.DefaultMode)
	set(&p.DefaultAgent, in.DefaultAgent)
	set(&p.DefaultPersona, in.DefaultPersona)
	set(&p.Notes, in.Notes)
	set(&p.Layout, in.Layout)
	if p.Name == "" {
		return store.Project{}, fmt.Errorf("%w: 项目需要名字", ErrInvalid)
	}
	if err := validMode(p.DefaultMode); err != nil {
		return store.Project{}, err
	}
	if p.Layout != "" && p.Layout != projectkey.LayoutWokspace {
		return store.Project{}, fmt.Errorf("%w: 项目结构只能是 wokspace 或留空", ErrInvalid)
	}
	if p.DefaultPersona != "" {
		if _, err := e.store().Persona(ctx, p.DefaultPersona); err != nil {
			return store.Project{}, fmt.Errorf("%w: 岗位 %s 不存在", ErrInvalid, p.DefaultPersona)
		}
	}
	ps, _ := e.store().Projects(ctx)
	key := projectkey.Key(p.RepoURL)
	for _, o := range ps {
		if o.ID != in.ID && strings.EqualFold(o.Name, p.Name) {
			return store.Project{}, fmt.Errorf("%w: 已经有同名的项目", ErrConflict)
		}
		if key != "" && o.ID != in.ID && o.RemoteKey == key {
			return store.Project{}, fmt.Errorf("%w: 仓库 %s 已经登记为项目「%s」", ErrConflict, key, o.Name)
		}
	}
	if in.Nodes != nil {
		seen := map[string]bool{}
		var nodes []event.ProjectNode
		for i, n := range *in.Nodes {
			n.Path = strings.TrimSpace(n.Path)
			if n.NodeID == "" || n.Path == "" || seen[n.NodeID] {
				return store.Project{}, fmt.Errorf("%w: 每台机器只能登记一次，并且要填仓库路径", ErrInvalid)
			}
			if _, err := e.store().Node(ctx, n.NodeID); err != nil {
				return store.Project{}, fmt.Errorf("%w: 机器不存在", ErrInvalid)
			}
			seen[n.NodeID] = true
			nodes = append(nodes, event.ProjectNode{NodeID: n.NodeID, Path: n.Path, Priority: i, Source: n.Source})
		}
		p.Nodes = nodes
	}
	if in.Deploy != nil {
		p.Deploy = *in.Deploy
	}
	if d := p.Deploy; d != nil {
		d.Path, d.Command, d.Health, d.Branch = strings.TrimSpace(d.Path), strings.TrimSpace(d.Command), strings.TrimSpace(d.Health), strings.TrimSpace(d.Branch)
		if d.AutoOnMerge && d.Branch == "" {
			return store.Project{}, fmt.Errorf("%w: 自动部署要说明部署分支", ErrInvalid)
		}
		if d.NodeID == "" {
			p.Deploy = nil
		} else {
			n, err := e.store().Node(ctx, d.NodeID)
			if err != nil {
				return store.Project{}, fmt.Errorf("%w: 部署目标的机器不存在", ErrInvalid)
			}
			if n.Role != proto.RoleServer {
				return store.Project{}, fmt.Errorf("%w: 部署目标要是一台服务器（%s 是开发机）", ErrInvalid, n.Name)
			}
			if d.Path == "" || d.Command == "" {
				return store.Project{}, fmt.Errorf("%w: 部署目标要填目录和部署命令", ErrInvalid)
			}
		}
	}
	if _, err := e.Log.Append(ctx, event.New(event.ProjectPut, actor, "", event.ProjectPayload{
		ProjectID: in.ID, Name: p.Name, RepoURL: p.RepoURL, DefaultMode: p.DefaultMode, DefaultAgent: p.DefaultAgent,
		Nodes: p.Nodes, DefaultPersona: p.DefaultPersona, Notes: p.Notes, Layout: p.Layout, Deploy: p.Deploy,
	})); err != nil {
		return store.Project{}, err
	}
	return e.store().Project(ctx, in.ID)
}

// inflightStatuses are the task statuses that keep a project from being
// archived: an agent is (or is about to be) working on it.
var inflightStatuses = []string{event.TaskQueued, event.TaskRunning, event.TaskWaitingApproval}

// ArchiveProject archives a project (archived) or makes it active again.
// Archived projects leave the workbench and the supervisor's environment;
// the built-in misc project and projects with work in flight stay active.
func (e *Engine) ArchiveProject(ctx context.Context, id string, archived bool, actor string) (store.Project, error) {
	p, err := e.store().Project(ctx, id)
	if err != nil {
		return store.Project{}, err
	}
	if p.Archived() == archived {
		return p, nil
	}
	if archived {
		if p.ID == projectkey.MiscID {
			return store.Project{}, fmt.Errorf("%w: 内置的“杂项”项目不能归档", ErrConflict)
		}
		busy, err := e.store().Tasks(ctx, store.TaskFilter{ProjectID: p.ID, Statuses: inflightStatuses, Limit: 1})
		if err != nil {
			return store.Project{}, err
		}
		if len(busy) > 0 {
			return store.Project{}, fmt.Errorf("%w: 项目「%s」还有正在执行的任务（%s），等它结束或取消后再归档", ErrConflict, p.Name, busy[0].Title)
		}
	}
	if _, err := e.Log.Append(ctx, event.New(event.ProjectArchived, actor, "",
		event.ProjectArchivedPayload{ProjectID: p.ID, Archived: archived})); err != nil {
		return store.Project{}, err
	}
	return e.store().Project(ctx, p.ID)
}

// SetupInput asks for a project to be created or cloned on a machine.
type SetupInput struct {
	ProjectID string // clone: the project; create: empty (a new one is made)
	Name      string // create: the new project's name (kebab-case)
	NodeID    string // empty: the first online dev machine that can do it
	Layout    string // create: "wokspace" or "plain", no default
	Owner     string // create: also create the GitHub repository owner/name
	Private   bool
	ThreadID  string
	Actor     string
}

// CreateProject registers a new project and asks a machine to create it in
// its first workspace (`wok init` by default, see requirements R2.5).
func (e *Engine) CreateProject(ctx context.Context, in SetupInput) (store.Project, store.Run, error) {
	in.Name = strings.TrimSpace(in.Name)
	if !projectkey.ValidName(in.Name) {
		return store.Project{}, store.Run{}, fmt.Errorf("%w: 项目名只能用小写字母、数字和连字符，比如 my-app", ErrInvalid)
	}
	// No default structure (decision 0022): the caller says which.
	layout := ""
	switch in.Layout {
	case projectkey.LayoutWokspace:
		layout = projectkey.LayoutWokspace
	case "plain":
	case "":
		return store.Project{}, store.Run{}, fmt.Errorf("%w: 要说明新项目用什么结构：wokspace 或 plain", ErrInvalid)
	default:
		return store.Project{}, store.Run{}, fmt.Errorf("%w: layout 只能是 wokspace 或 plain", ErrInvalid)
	}
	node, err := e.setupNode(ctx, in.NodeID, layout == projectkey.LayoutWokspace)
	if err != nil {
		return store.Project{}, store.Run{}, err
	}
	repo := ""
	if in.Owner != "" {
		repo = "https://github.com/" + strings.TrimSpace(in.Owner) + "/" + in.Name
	}
	name, ly := in.Name, layout
	p, err := e.PutProject(ctx, ProjectInput{Name: &name, RepoURL: &repo, Layout: &ly}, in.Actor)
	if err != nil {
		return store.Project{}, store.Run{}, err
	}
	run, err := e.startSetup(ctx, in, node, proto.PurposeCreate, &proto.Setup{Name: in.Name, Layout: layout, Owner: strings.TrimSpace(in.Owner), Private: in.Private}, p)
	return p, run, err
}

// CloneProject asks a machine to clone a project into its first workspace.
func (e *Engine) CloneProject(ctx context.Context, in SetupInput) (store.Run, error) {
	p, err := e.store().Project(ctx, in.ProjectID)
	if err != nil {
		return store.Run{}, err
	}
	if p.RepoURL == "" {
		return store.Run{}, fmt.Errorf("%w: 项目「%s」没有登记仓库地址，没法 clone", ErrInvalid, p.Name)
	}
	node, err := e.setupNode(ctx, in.NodeID, false)
	if err != nil {
		return store.Run{}, err
	}
	for _, n := range p.Nodes {
		if n.NodeID == node.ID {
			return store.Run{}, fmt.Errorf("%w: 项目已经在 %s 的 %s", ErrConflict, node.Name, n.Path)
		}
	}
	name := strings.ToLower(projectkey.NameFromKey(p.RemoteKey))
	if name == "" {
		name = strings.ToLower(strings.TrimSuffix(filepath.Base(strings.TrimRight(p.RepoURL, "/")), ".git"))
	}
	if !projectkey.ValidName(name) {
		name = strings.ToLower(strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
				return r
			}
			if r >= 'A' && r <= 'Z' {
				return r + 32
			}
			return '-'
		}, name))
		name = strings.Trim(name, "-")
	}
	if !projectkey.ValidName(name) {
		name = "project-" + strings.ToLower(ids.New()[20:])
	}
	return e.startSetup(ctx, in, node, proto.PurposeClone, &proto.Setup{Name: name, Remote: p.RepoURL}, p)
}

// setupNode picks the machine a project is created or cloned on.
func (e *Engine) setupNode(ctx context.Context, want string, needWok bool) (store.Node, error) {
	nodes, err := e.store().Nodes(ctx)
	if err != nil {
		return store.Node{}, err
	}
	var reasons []string
	for _, n := range nodes {
		if want != "" && n.ID != want {
			continue
		}
		switch {
		case !e.Nodes.Online(n.ID):
			reasons = append(reasons, n.Name+" 离线")
		case want == "" && n.Role == proto.RoleServer:
			continue
		case len(n.Workspaces) == 0:
			reasons = append(reasons, n.Name+" 没有设置工作空间")
		case needWok && n.Wok == "":
			reasons = append(reasons, n.Name+" 没有安装 wok")
		default:
			return n, nil
		}
	}
	if want != "" && len(reasons) == 0 {
		return store.Node{}, fmt.Errorf("%w: 机器不存在", ErrInvalid)
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "没有在线的开发机")
	}
	return store.Node{}, fmt.Errorf("%w: 现在没有机器能做：%s", ErrConflict, strings.Join(reasons, "；"))
}

func (e *Engine) startSetup(ctx context.Context, in SetupInput, n store.Node, purpose string, st *proto.Setup, p store.Project) (store.Run, error) {
	thread := in.ThreadID
	if thread == "" {
		thread, _ = e.store().MainThreadID(ctx)
	}
	mode := firstNonEmpty(p.DefaultMode, e.setting(ctx, SettingDefaultMode), proto.ModeDefault)
	title := "clone " + p.Name
	if purpose == proto.PurposeCreate {
		title = "新建项目 " + p.Name
	}
	return e.Nodes.Create(ctx, nodehub.CreateRun{
		ThreadID: thread, NodeID: n.ID, Purpose: purpose, Setup: st, PermissionMode: mode, Title: title,
		Prompt: p.ID, // the project the setup is for
	})
}

// Setup is the effect handler for a clone or create run that ended: it
// records the new place of the project and reports in the thread.
func (e *Engine) Setup(ctx context.Context, eff store.Effect) error {
	r, err := e.store().Run(ctx, string(eff.Payload))
	if errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	p, err := e.store().Project(ctx, r.Prompt)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	node, _ := e.store().Node(ctx, r.NodeID)
	what := "clone"
	if r.Purpose == event.PurposeCreate {
		what = "创建"
	}
	if r.Status != event.RunStatusSucceeded {
		e.say(ctx, r.ThreadID, fmt.Sprintf("在 %s 上%s项目「%s」没有成功：%s", node.Name, what, p.Name, firstNonEmpty(r.Error, r.Summary, r.Status)), true)
		return nil
	}
	nodes := append([]event.ProjectNode(nil), p.Nodes...)
	known := false
	for _, n := range nodes {
		known = known || n.NodeID == r.NodeID
	}
	if !known && r.Workdir != "" {
		nodes = append(nodes, event.ProjectNode{NodeID: r.NodeID, Path: r.Workdir, Priority: len(nodes)})
		if _, err := e.PutProject(ctx, ProjectInput{ID: p.ID, Nodes: &nodes}, event.ActorSystem); err != nil {
			return err
		}
	}
	e.say(ctx, r.ThreadID, fmt.Sprintf("项目「%s」已经%s到 %s 的 %s。", p.Name, map[bool]string{true: "创建", false: " clone "}[r.Purpose == event.PurposeCreate], node.Name, r.Workdir), true)
	e.Sweep()
	return nil
}

// say posts a report in a thread (the main thread when empty).
func (e *Engine) say(ctx context.Context, thread, text string, notify bool) {
	if thread == "" {
		thread, _ = e.store().MainThreadID(ctx)
	}
	ev := event.New(event.MessageAssistant, event.ActorSupervisor, thread,
		event.MessagePayload{MessageID: ids.New(), Content: text, Kind: event.KindReport, Notify: notify})
	if _, err := e.Log.Append(ctx, ev); err != nil {
		slog.Error("task: post", "err", err)
	}
}

// CheckDeploys deploys projects that deploy automatically when their
// deploy branch has a new commit (decision 0022): the machines report the
// commits of origin's branches with every scan (after a fetch or a push), and
// a commit that is not deployed yet gets an ops task, once. The first commit
// seen after automatic deploys were switched on is only remembered.
func (e *Engine) CheckDeploys(ctx context.Context) {
	e.deployMu.Lock()
	defer e.deployMu.Unlock()
	ps, err := e.store().Projects(ctx)
	if err != nil {
		return
	}
	var scans []store.RepoScan
	for _, p := range ps {
		d := p.Deploy
		if d == nil || !d.AutoOnMerge || d.Branch == "" || p.Archived() {
			continue
		}
		if scans == nil {
			if scans, err = e.store().RepoScans(ctx); err != nil {
				return
			}
		}
		head := deployHead(p, scans)
		if head == "" || head == p.DeployedCommit {
			continue
		}
		if p.DeployedCommit == "" {
			ev := event.New(event.ProjectDeployed, event.ActorSystem, "", event.ProjectDeployedPayload{ProjectID: p.ID, Commit: head, Baseline: true})
			if _, err := e.Log.Append(ctx, ev); err != nil {
				slog.Error("task: record the deploy baseline", "project", p.ID, "err", err)
			}
			continue
		}
		if e.deployQueued(ctx, p.ID, head) {
			continue
		}
		thread, _ := e.store().MainThreadID(ctx)
		short := head
		if len(short) > 8 {
			short = short[:8]
		}
		if _, err := e.Create(ctx, Input{
			ThreadID: thread, Kind: event.TaskKindOps, Place: event.PlaceInPlace, Review: event.ReviewNone,
			Title:     fmt.Sprintf("部署「%s」：%s 的 %s", p.Name, d.Branch, short),
			Detail:    fmt.Sprintf("%s 有了新的提交 %s。把项目「%s」部署到服务器上：先拉取这个提交，再执行部署命令，最后做健康检查。", d.Branch, head, p.Name),
			ProjectID: p.ID, Persona: e.opsPersona(ctx), Dispatch: true, Actor: event.ActorSupervisor, DeployCommit: head,
		}); err != nil {
			slog.Warn("task: create a deploy task", "project", p.ID, "err", err)
			continue
		}
		e.postThread(ctx, thread, "", fmt.Sprintf("「%s」的部署分支 %s 有新提交（%s），按项目设置开始部署到服务器。", p.Name, d.Branch, short))
	}
}

// deployHead is the newest commit of the project's deploy branch that any
// of its machines knows of (the most recent scan wins).
func deployHead(p store.Project, scans []store.RepoScan) string {
	places := map[string]bool{}
	for _, n := range p.Nodes {
		places[n.NodeID+"\x00"+n.Path] = true
	}
	if d := p.Deploy; d != nil {
		places[d.NodeID+"\x00"+d.Path] = true
	}
	head, seen := "", int64(0)
	for _, s := range scans {
		if !places[s.NodeID+"\x00"+s.Path] && (s.ProjectID != p.ID) {
			continue
		}
		if sha := s.Heads[p.Deploy.Branch]; sha != "" && s.SeenAt >= seen {
			head, seen = sha, s.SeenAt
		}
	}
	return head
}

// deployQueued reports whether a deploy of commit already exists and was
// not cancelled.
func (e *Engine) deployQueued(ctx context.Context, projectID, commit string) bool {
	tasks, _ := e.store().Tasks(ctx, store.TaskFilter{ProjectID: projectID, Limit: 5000})
	for _, t := range tasks {
		if t.DeployCommit == commit && t.Status != event.TaskCancelled {
			return true
		}
	}
	return false
}

// DeployedSince reports whether an ops task of the project was created at
// or after ts and not cancelled: a deploy is done or under way.
func DeployedSince(tasks []store.Task, projectID string, ts int64) bool {
	for _, t := range tasks {
		if t.ProjectID == projectID && t.Kind == event.TaskKindOps && t.CreatedAt >= ts && t.Status != event.TaskCancelled {
			return true
		}
	}
	return false
}

// opsPersona is the first persona for ops tasks, or none.
func (e *Engine) opsPersona(ctx context.Context) string {
	ps, _ := e.store().Personas(ctx)
	for _, p := range ps {
		for _, k := range p.Kinds {
			if k == event.TaskKindOps {
				return p.ID
			}
		}
	}
	return ""
}

// DescribeContext names a project or task the user was looking at.
func (e *Engine) DescribeContext(ctx context.Context, c event.MessageContext) string {
	if c.TaskID != "" {
		if t, err := e.store().Task(ctx, c.TaskID); err == nil {
			return describe(t)
		}
	}
	if c.ProjectID != "" {
		if p, err := e.store().Project(ctx, c.ProjectID); err == nil {
			return fmt.Sprintf("项目 %s（id %s）", p.Name, p.ID)
		}
	}
	return ""
}
