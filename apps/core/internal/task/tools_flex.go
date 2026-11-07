package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

// The supervisor's tools added by decision 0022: reviews on request, git
// operations, the project consultant and reading a project's files.

type reviewTool struct{ e *Engine }

func (reviewTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "task_review",
		Description: "让一个已经做完的任务（再）验收一次，比如用户说“跑一遍测试给我看”“让 agent 跑测试验一下”。" +
			"by：model 由验收模型看 diff；agent 由验收 agent 在目录里只读地跑测试检查；user 交给用户。结果会自动汇报。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"task":{"type":"string","description":"任务 id"},
			"by":{"type":"string","enum":["model","agent","user"]}
		},"required":["task","by"]}`),
	}
}

func (t reviewTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct{ Task, By string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if err := t.e.checkScope(ctx, a.Task); err != nil {
		return "", err
	}
	task, err := t.e.RequestReview(ctx, a.Task, a.By, event.ActorSupervisor)
	if err != nil {
		return "", err
	}
	return "已安排验收：" + describe(task), nil
}

type verdictTool struct{ e *Engine }

func (verdictTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "task_verdict",
		Description: "记下用户对一个等验收任务的结论：用户说“通过”“可以”时 pass=true；用户说要改什么时 pass=false，reason 写清楚要改的地方，任务会照着返工。" +
			"只记用户自己的结论，不要替用户判断。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"task":{"type":"string","description":"任务 id"},
			"pass":{"type":"boolean"},
			"reason":{"type":"string","description":"不通过时要改什么；通过时可以不填"}
		},"required":["task","pass"]}`),
	}
}

func (t verdictTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Task   string
		Pass   bool
		Reason string
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if err := t.e.checkScope(ctx, a.Task); err != nil {
		return "", err
	}
	task, err := t.e.Verdict(ctx, a.Task, a.Pass, a.Reason, event.ActorUser)
	if err != nil {
		return "", err
	}
	return "已记下：" + describe(task), nil
}

type gitStatusTool struct{ e *Engine }

func (gitStatusTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "git_status",
		Description: "看一个项目的 checkout 现在的状态：当前分支（或 detached）、和上游比领先 / 落后多少、未提交的文件、远端、最近活跃的分支和它们检出在哪里。" +
			"判断用户说的“当前分支”“提交推送”指哪个分支之前先看它；不读网络，落后的数量以上次 fetch 为准。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"project":{"type":"string","description":"项目名或 id"},
			"machine":{"type":"string","description":"机器名或 id；项目在几台机器上时必填"}
		},"required":["project"]}`),
	}
}

func (t gitStatusTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct{ Project, Machine string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	p, node, err := t.e.projectAndNode(ctx, a.Project, a.Machine)
	if err != nil {
		return "", err
	}
	st, nodeID, err := t.e.GitStatus(ctx, p.ID, node)
	if err != nil {
		return "", err
	}
	return formatGitStatus(t.e.nodeName(ctx, nodeID), st), nil
}

// projectAndNode resolves a project (the chat's own in a project chat) and
// an optional machine.
func (e *Engine) projectAndNode(ctx context.Context, project, machine string) (store.Project, string, error) {
	if project == "" {
		project = scopeOf(ctx)
	}
	if project == "" {
		return store.Project{}, "", fmt.Errorf("%w: 要指定项目", ErrInvalid)
	}
	p, err := e.findProject(ctx, project)
	if err != nil {
		return p, "", err
	}
	node := ""
	if machine != "" {
		if node, err = e.findNode(ctx, machine); err != nil {
			return p, "", err
		}
	}
	return p, node, nil
}

func formatGitStatus(machine string, st proto.GitStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s 上的 %s：", machine, st.Root)
	if st.Detached {
		b.WriteString("HEAD 是 detached（不在任何分支上）")
	} else {
		fmt.Fprintf(&b, "当前分支 %s", st.Branch)
	}
	if st.Upstream != "" {
		fmt.Fprintf(&b, "，上游 %s（领先 %d、落后 %d）", st.Upstream, st.Ahead, st.Behind)
	} else if !st.Detached {
		b.WriteString("，没有上游分支")
	}
	if st.Changes+st.Untracked > 0 {
		fmt.Fprintf(&b, "，%d 个文件有未提交的改动、%d 个未跟踪的文件", st.Changes, st.Untracked)
	} else {
		b.WriteString("，工作区干净")
	}
	if len(st.Remotes) > 0 {
		fmt.Fprintf(&b, "\n远端：%s", strings.Join(st.Remotes, "、"))
		if st.RemoteDefault != "" {
			fmt.Fprintf(&b, "（origin 的默认分支是 %s，只供参考）", st.RemoteDefault)
		}
	}
	if st.FetchedAt > 0 {
		fmt.Fprintf(&b, "\n上次 fetch：%s", time.UnixMilli(st.FetchedAt).Format("2006-01-02 15:04"))
	}
	if len(st.Branches) > 0 {
		b.WriteString("\n最近活跃的分支：")
		for _, br := range st.Branches {
			fmt.Fprintf(&b, "\n- %s（%s", br.Name, time.Unix(br.When, 0).Format("01-02 15:04"))
			if br.Upstream != "" {
				fmt.Fprintf(&b, "，上游 %s 领先 %d 落后 %d", br.Upstream, br.Ahead, br.Behind)
			}
			if br.Worktree != "" {
				fmt.Fprintf(&b, "，检出在 %s", br.Worktree)
			}
			fmt.Fprintf(&b, "）%s", br.Subject)
		}
	}
	return b.String()
}

// gitTool is one git operation.
type gitTool struct {
	e  *Engine
	op string
}

var gitSpecs = map[string]struct{ desc, schema string }{
	proto.GitCommit: {
		"提交一个任务的改动。worktree 任务：在任务的 worktree 里提交到任务分支，不动别的分支；squash=true 把任务的提交压成一个。" +
			"就地任务：在项目的 checkout 里提交到它当前检出的分支，只提交这个任务改过的文件（用户自己的其他改动不动）；之后要推送就用 git_push 推那个分支。",
		`{"type":"object","properties":{"task":{"type":"string"},"message":{"type":"string","description":"提交信息；不填用任务标题"},"squash":{"type":"boolean"}},"required":["task"]}`},
	proto.GitMerge: {
		"把任务分支合并到一个分支：先提交剩下的改动，rebase 到目标分支上，再只快进目标分支（目标分支在某个 checkout 里检出时，那里的文件会更新，且那里要没有未提交的改动）。" +
			"合并后任务的 worktree 和分支会删除；没推送之前可以用 git_undo 撤回。有冲突时什么都不改，会告诉你冲突的文件。into 必填，按用户的话和 git_status 判断，不要默认 main。",
		`{"type":"object","properties":{"task":{"type":"string"},"into":{"type":"string","description":"目标分支"},"squash":{"type":"boolean","description":"把任务的提交压成一个"},"message":{"type":"string","description":"squash 时的提交信息；不填用任务标题"}},"required":["task","into"]}`},
	proto.GitPush: {
		"把项目里的一个本地分支推送到远端（同名分支，不会 force）。branch 必填；remote 不填时用分支的上游远端，只有一个远端时用它，有多个又没有上游时会报错，要问用户。",
		`{"type":"object","properties":{"project":{"type":"string"},"machine":{"type":"string","description":"项目在几台机器上时必填"},"branch":{"type":"string"},"remote":{"type":"string"},"task":{"type":"string","description":"可选：推送和这个任务有关时给出，用它的项目"}},"required":["branch"]}`},
	proto.GitPR: {
		"push 任务分支并开一个 PR（GitHub，需要 gh）。base 是 PR 的目标分支，必填。",
		`{"type":"object","properties":{"task":{"type":"string"},"base":{"type":"string"},"title":{"type":"string"},"body":{"type":"string"}},"required":["task","base"]}`},
	proto.GitUndo: {
		"撤回 sous 最近一次在这个项目里做的、还没推送的 git 操作（提交或合并）：分支回到操作之前，被合并删掉的任务分支会恢复。已经推送的不能撤回。",
		`{"type":"object","properties":{"project":{"type":"string"},"machine":{"type":"string"},"branch":{"type":"string","description":"只撤回这个分支上的最近一次操作；不填撤回整个仓库里最近一次"}},"required":["project"]}`},
	proto.GitDiscard: {
		"丢弃一个任务的分支：删除它的 worktree 和分支，上面没合并的改动会丢失。要用户明确说不要了才用。",
		`{"type":"object","properties":{"task":{"type":"string"}},"required":["task"]}`},
	proto.GitRestore: {
		"撤销一个就地执行的任务的改动：用任务开始前的快照恢复项目目录（改过的文件恢复原样，任务新增的文件删除）。",
		`{"type":"object","properties":{"task":{"type":"string"}},"required":["task"]}`},
}

var gitToolName = map[string]string{proto.GitCommit: "git_commit", proto.GitMerge: "git_merge", proto.GitPush: "git_push", proto.GitPR: "git_pr",
	proto.GitUndo: "git_undo", proto.GitDiscard: "git_discard", proto.GitRestore: "task_restore"}

func (t gitTool) Spec() provider.ToolSpec {
	s := gitSpecs[t.op]
	return provider.ToolSpec{Name: gitToolName[t.op], Description: s.desc + "会按权限模式请求批准；做完用一句话告诉用户结果。", Schema: json.RawMessage(s.schema)}
}

func (t gitTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Task, Project, Machine, Into, Base, Branch, Remote, Message, Title, Body string
		Squash                                                                   bool
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	in := GitInput{Op: t.op, TaskID: a.Task, Into: a.Into, Base: a.Base, Branch: a.Branch, Remote: a.Remote, Squash: a.Squash,
		Message: a.Message, Title: a.Title, Body: a.Body, ThreadID: tool.TurnFrom(ctx).ThreadID, Actor: event.ActorSupervisor}
	if a.Task != "" {
		if err := t.e.checkScope(ctx, a.Task); err != nil {
			return "", err
		}
	}
	if a.Project != "" || a.Machine != "" || (!taskOps[t.op] && a.Task == "") {
		p, node, err := t.e.projectAndNode(ctx, a.Project, a.Machine)
		if err != nil {
			return "", err
		}
		in.ProjectID, in.NodeID = p.ID, node
	}
	r, done, err := t.e.Git(ctx, in, GitWait)
	if err != nil {
		return "", err
	}
	if !done {
		return fmt.Sprintf("%s还在进行（可能在等用户批准）；结果出来后会自动汇报在对话里。", gitOpName[t.op]), nil
	}
	return GitReport(r), nil
}

type consultTool struct{ e *Engine }

func (consultTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "consult",
		Description: "向项目的顾问提问：一个在项目所在机器上、只读的编程 agent，能读代码、搜索、看 git 记录，不能改任何东西。" +
			"和用户讨论某个项目的需求、方案、现状时用它；它会记得这个项目之前的讨论（同一个会话），可以连续追问。" +
			"它的回答会原样显示给用户，你不要复述，需要时只补一两句你的判断或下一步的问题。只想看一两个文件时用 project_read 更快。" +
			"讨论收敛、用户同意动手时，用 task_create 加 from_consult=true 把会话交给任务。new_topic=true 开始一个新话题（新会话）。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"project":{"type":"string","description":"项目名或 id；项目对话里可以不填"},
			"question":{"type":"string","description":"要问顾问的问题，写清楚背景和想知道什么（它看不到这段对话）"},
			"new_topic":{"type":"boolean"},
			"machine":{"type":"string","description":"指定在哪台机器上看；一般不填"},
			"agent":{"type":"string","description":"用哪个 agent 当顾问；只在开始新会话时生效，一般不填"},
			"model":{"type":"string","description":"顾问用的模型；只在开始新会话时生效，一般不填"},
			"effort":{"type":"string","description":"顾问的思考强度；只在开始新会话时生效"}
		},"required":["question"]}`),
	}
}

func (t consultTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Project, Question, Machine, Agent, Model, Effort string
		NewTopic                                         bool `json:"new_topic"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	p, node, err := t.e.projectAndNode(ctx, a.Project, a.Machine)
	if err != nil {
		return "", err
	}
	turn := tool.TurnFrom(ctx)
	ans, err := t.e.Consult(ctx, ConsultInput{ProjectID: p.ID, Question: a.Question, NewTopic: a.NewTopic, NodeID: node,
		ThreadID: turn.ThreadID, ReplyTo: turn.ReplyTo, Agent: a.Agent, Options: map[string]string{"model": a.Model, "effort": a.Effort}})
	if err != nil {
		return "", err
	}
	if ans.Pending {
		return "顾问还在看代码，回答出来后会直接显示在对话里。先告诉用户顾问在看，不要自己编答案。", nil
	}
	return fmt.Sprintf("【顾问（%s，在 %s 上）的回答已经原样显示给用户，不要复述；需要时只补一两句你的判断或下一个问题】\n\n%s",
		ans.Agent, t.e.nodeName(ctx, ans.NodeID), ans.Text), nil
}

type projectReadTool struct{ e *Engine }

func (projectReadTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "project_read",
		Description: "直接读项目里的文件：read 读一个文件，list 列一个目录，grep 用正则搜索文本文件（跳过依赖和构建目录）。只读，很快。" +
			"回答“X 是怎么写的”“有没有 Y”这类一两个文件的问题用它；要通读代码、连续讨论时用 consult。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"project":{"type":"string","description":"项目名或 id；项目对话里可以不填"},
			"op":{"type":"string","enum":["read","list","grep"]},
			"path":{"type":"string","description":"项目里的相对路径（read 的文件、list 的目录、grep 的范围）；不填是项目根目录"},
			"pattern":{"type":"string","description":"grep 的正则表达式（Go 语法）"},
			"machine":{"type":"string","description":"指定机器；一般不填"}
		},"required":["op"]}`),
	}
}

// maxReadRunes bounds what project_read returns to the model.
const maxReadRunes = 40000

func (t projectReadTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct{ Project, Op, Path, Pattern, Machine string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	p, node, err := t.e.projectAndNode(ctx, a.Project, a.Machine)
	if err != nil {
		return "", err
	}
	place, err := t.e.readablePlace(ctx, p, node)
	if err != nil {
		return "", err
	}
	args := proto.ProjectArgs{Root: place.Path, Rel: a.Path}
	switch a.Op {
	case "read":
		f, err := t.e.Nodes.ProjectFile(ctx, place.NodeID, args)
		if err != nil {
			return "", nodeError(err)
		}
		if f.Binary {
			return fmt.Sprintf("%s 是二进制文件（%d 字节），读不了内容", f.Rel, f.Size), nil
		}
		return fmt.Sprintf("%s（%d 字节）：\n%s", f.Rel, f.Size, truncate(f.Content, maxReadRunes)), nil
	case "list":
		d, err := t.e.Nodes.ProjectDir(ctx, place.NodeID, args)
		if err != nil {
			return "", nodeError(err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s/：", firstNonEmpty(d.Rel, "."))
		for _, en := range d.Entries {
			if en.Dir {
				fmt.Fprintf(&b, "\n%s/", en.Name)
			} else {
				fmt.Fprintf(&b, "\n%s（%d 字节）", en.Name, en.Size)
			}
		}
		return b.String(), nil
	case "grep":
		res, err := t.e.Nodes.Grep(ctx, place.NodeID, proto.GrepArgs{Root: place.Path, Rel: a.Path, Pattern: a.Pattern})
		if err != nil {
			return "", nodeError(err)
		}
		if len(res.Matches) == 0 {
			return fmt.Sprintf("在 %d 个文件里没有找到匹配 %q 的行", res.Files, a.Pattern), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d 处匹配（搜了 %d 个文件）：", len(res.Matches), res.Files)
		for _, m := range res.Matches {
			fmt.Fprintf(&b, "\n%s:%d: %s", m.Rel, m.Line, m.Text)
		}
		if res.Truncated {
			b.WriteString("\n…结果太多，只列出前面这些；缩小范围或换个更具体的表达式再搜")
		}
		return truncate(b.String(), maxReadRunes), nil
	}
	return "", fmt.Errorf("%w: op 只能是 read、list 或 grep", ErrInvalid)
}

// readablePlace is a machine with the project that is online: the named
// one, else the first online one by priority.
func (e *Engine) readablePlace(ctx context.Context, p store.Project, nodeID string) (event.ProjectNode, error) {
	if nodeID != "" {
		return e.placeOn(ctx, p, nodeID)
	}
	for _, n := range p.Nodes {
		if e.Nodes.CanRequest(n.NodeID) {
			return n, nil
		}
	}
	if len(p.Nodes) == 0 {
		return event.ProjectNode{}, fmt.Errorf("%w: 项目 %s 还不在任何机器上", ErrConflict, p.Name)
	}
	return event.ProjectNode{}, fmt.Errorf("%w: 项目 %s 所在的机器都不在线", ErrConflict, p.Name)
}

// nodeError turns a machine's refusal into an error the model can use.
func nodeError(err error) error {
	var ne *proto.NodeError
	if errors.As(err, &ne) {
		return fmt.Errorf("%w: %s", ErrInvalid, ne.Message)
	}
	if errors.Is(err, nodehub.ErrOffline) {
		return fmt.Errorf("%w: 机器不在线", ErrConflict)
	}
	return err
}
