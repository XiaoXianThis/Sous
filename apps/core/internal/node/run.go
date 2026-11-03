package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/agents"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/workspace"
)

const (
	summaryRunes = 4000
	detailRunes  = 2000
	// cancelGrace is how long an agent gets to stop after session/cancel
	// before its process is killed.
	cancelGrace = 10 * time.Second
)

// Modes that make an agent ask before every change, and modes that let it
// do everything, by the ids agents use for them.
var (
	askModes  = []string{"default", "ask", "manual"}
	openModes = []string{"bypassPermissions", "yolo", "full-access", "auto"}
)

type run struct {
	d    *Daemon
	spec proto.RunStart

	ctx    context.Context
	cancel context.CancelFunc

	mu         sync.Mutex
	client     *acp.Client
	sessionID  string
	isStarted  bool
	stopReason string // set by stop()
	followUps  []string
	approvals  map[string]chan string
	turnText   strings.Builder
}

func newRun(d *Daemon, s proto.RunStart) *run {
	ctx, cancel := context.WithCancel(context.Background())
	return &run{d: d, spec: s, ctx: ctx, cancel: cancel, approvals: map[string]chan string{}}
}

func (r *run) started() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.isStarted
}

func (r *run) status(text string) {
	r.d.ob.reliable(proto.New(proto.TRunUpdate, proto.RunUpdate{RunID: r.spec.RunID, Kind: proto.UpdateStatus, Text: text}))
}

func (r *run) end(status, stopReason, summary, diffstat string, err error) {
	e := proto.RunEnded{RunID: r.spec.RunID, Status: status, StopReason: stopReason, Summary: summary, DiffStat: diffstat}
	if err != nil {
		e.Error = err.Error()
	}
	r.d.ob.reliable(proto.New(proto.TRunEnded, e))
	slog.Info("daemon: run ended", "run", r.spec.RunID, "status", status, "err", err)
}

// execute runs the agent until its turn ends, then reports the diff.
func (r *run) execute() {
	defer r.cancel()
	select {
	case r.d.sem <- struct{}{}:
		defer func() { <-r.d.sem }()
	case <-r.ctx.Done():
		r.end("cancelled", "", "", "", errors.New(r.stopReasonText()))
		return
	}
	spec, ok := agents.Find(r.d.specs, r.spec.Agent)
	if !ok {
		r.end("failed", "", "", "", fmt.Errorf("这台机器不认识 agent %q", r.spec.Agent))
		return
	}

	var ws workspace.Prepared
	var err error
	if r.spec.RepoPath != "" {
		r.status("正在创建独立的 git worktree")
		ws, err = workspace.PrepareCode(r.ctx, r.d.root, r.spec.RepoPath, r.spec.RunID)
	} else {
		ws, err = workspace.PrepareGeneral(r.d.root, r.spec.RunID)
	}
	if err != nil {
		r.end("failed", "", "", "", fmt.Errorf("准备工作目录失败：%w", err))
		return
	}
	r.mu.Lock()
	r.isStarted = true
	r.mu.Unlock()
	r.d.ob.reliable(proto.New(proto.TRunStarted, proto.RunStarted{RunID: r.spec.RunID, Workdir: ws.Dir, Branch: ws.Branch}))

	status, stop, err := r.drive(spec, ws)

	// Report what changed even when the turn failed part way.
	diff, stat, derr := workspace.Diff(context.Background(), ws)
	if derr != nil {
		slog.Warn("daemon: diff failed", "run", r.spec.RunID, "err", derr)
	} else if diff != "" {
		r.sendDiff(diff)
	}
	r.end(status, stop, r.summary(), stat, err)
}

// drive starts the agent, opens a session and sends the prompt, then any
// follow-up messages that arrived meanwhile.
func (r *run) drive(spec agents.Spec, ws workspace.Prepared) (status, stop string, err error) {
	cmd, err := spec.Command(ws.Dir)
	if err != nil {
		return "failed", "", fmt.Errorf("找不到 %s 的启动命令 %q：%w", spec.Title, spec.Launch[0], err)
	}
	r.status("正在启动 " + spec.Title)
	client, err := acp.Start(r.ctx, cmd, acp.Callbacks{OnUpdate: r.onUpdate, OnPermission: r.onPermission})
	if err != nil {
		return r.failure(fmt.Errorf("启动 %s 失败：%w", spec.Title, err))
	}
	defer client.Close()
	session, err := client.NewSession(r.ctx, ws.Dir)
	if err != nil {
		if errors.Is(err, acp.ErrNotLoggedIn) {
			return "failed", "", fmt.Errorf("%s 还没有登录，请先在这台电脑上登录：%w", spec.Title, err)
		}
		return r.failure(err)
	}
	r.mu.Lock()
	r.client, r.sessionID = client, session.ID
	r.mu.Unlock()
	if mode := pickMode(session, r.spec.PermissionMode); mode != "" && mode != session.CurrentMode {
		if err := client.SetMode(r.ctx, session.ID, mode); err != nil {
			slog.Warn("daemon: set mode failed", "run", r.spec.RunID, "mode", mode, "err", err)
		}
	}

	prompt := r.firstPrompt(ws)
	for {
		r.mu.Lock()
		r.turnText.Reset()
		r.mu.Unlock()
		stop, err = client.Prompt(r.ctx, session.ID, prompt)
		if err != nil {
			return r.failure(err)
		}
		if stop == "cancelled" {
			return "cancelled", stop, errors.New(r.stopReasonText())
		}
		r.mu.Lock()
		more := r.followUps
		r.followUps = nil
		r.mu.Unlock()
		if len(more) == 0 {
			break
		}
		prompt = strings.Join(more, "\n\n")
	}
	if stop == "end_turn" {
		return "succeeded", stop, nil
	}
	return "failed", stop, fmt.Errorf("agent 停止：%s", stop)
}

// failure classifies an error from the agent: a stop the user asked for is
// a cancellation, anything else a failure.
func (r *run) failure(err error) (string, string, error) {
	if r.ctx.Err() != nil {
		return "cancelled", "", errors.New(r.stopReasonText())
	}
	return "failed", "", err
}

func (r *run) firstPrompt(ws workspace.Prepared) string {
	var b strings.Builder
	if r.spec.RecoveryNote != "" {
		b.WriteString(r.spec.RecoveryNote + "\n\n")
	}
	b.WriteString(r.spec.Prompt)
	if ws.Branch != "" {
		fmt.Fprintf(&b, "\n\n（说明：你在 sous 为这个任务新建的 git worktree 里工作，分支是 %s。可以提交，但不要 push，也不要切换分支；完成后简要说明改了什么。）", ws.Branch)
	}
	return b.String()
}

func pickMode(s acp.Session, permission string) string {
	want := askModes
	if permission == proto.ModeOpen {
		want = openModes
	}
	for _, id := range want {
		for _, m := range s.Modes {
			if m.ID == id {
				return id
			}
		}
	}
	return ""
}

func (r *run) onUpdate(_ string, u acp.Update) {
	id := r.spec.RunID
	switch u.Kind {
	case "agent_message_chunk":
		r.mu.Lock()
		r.turnText.WriteString(u.Text)
		r.mu.Unlock()
		r.d.ob.ephemeral(proto.New(proto.TRunOutput, proto.RunUpdate{RunID: id, Kind: proto.UpdateText, Text: u.Text}))
	case "agent_thought_chunk":
		r.d.ob.ephemeral(proto.New(proto.TRunOutput, proto.RunUpdate{RunID: id, Kind: proto.UpdateThought, Text: u.Text}))
	case "tool_call", "tool_call_update":
		r.d.ob.reliable(proto.New(proto.TRunUpdate, proto.RunUpdate{
			RunID: id, Kind: proto.UpdateTool, ToolCallID: u.ToolCallID, Title: u.Title, Status: u.Status, ToolKind: u.ToolKind,
		}))
	case "plan":
		plan := make([]proto.PlanEntry, len(u.Plan))
		for i, e := range u.Plan {
			plan[i] = proto.PlanEntry{Content: e.Content, Priority: e.Priority, Status: e.Status}
		}
		r.d.ob.reliable(proto.New(proto.TRunUpdate, proto.RunUpdate{RunID: id, Kind: proto.UpdatePlan, Plan: plan}))
	}
}

// onPermission forwards the agent's question to the Hub and waits for the
// user's answer. In open mode it allows at once.
func (r *run) onPermission(ctx context.Context, req acp.PermissionRequest) acp.Decision {
	if r.spec.PermissionMode == proto.ModeOpen {
		for _, kind := range []string{"allow_always", "allow_once"} {
			for _, o := range req.Options {
				if o.Kind == kind {
					return acp.Decision{OptionID: o.OptionID}
				}
			}
		}
	}
	approvalID := ids.New()
	ch := make(chan string, 1)
	r.mu.Lock()
	r.approvals[approvalID] = ch
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.approvals, approvalID)
		r.mu.Unlock()
	}()

	opts := make([]proto.Option, len(req.Options))
	for i, o := range req.Options {
		opts[i] = proto.Option{OptionID: o.OptionID, Name: o.Name, Kind: o.Kind}
	}
	title := req.ToolCall.Title
	if title == "" {
		title = "agent 请求执行一个操作"
	}
	r.d.ob.reliable(proto.New(proto.TApprovalRequest, proto.ApprovalRequest{
		ApprovalID: approvalID, RunID: r.spec.RunID, Title: title, ToolKind: req.ToolCall.Kind,
		Detail: truncate(prettyJSON(req.ToolCall.RawInput), detailRunes), Options: opts,
	}))
	select {
	case opt := <-ch:
		return acp.Decision{OptionID: opt}
	case <-r.ctx.Done():
		return acp.Decision{}
	}
}

func (r *run) decide(approvalID, optionID string) {
	r.mu.Lock()
	ch := r.approvals[approvalID]
	r.mu.Unlock()
	if ch != nil {
		select {
		case ch <- optionID:
		default:
		}
	}
}

// followUp queues an instruction for after the agent's current turn.
func (r *run) followUp(text string) {
	r.mu.Lock()
	r.followUps = append(r.followUps, text)
	r.mu.Unlock()
	r.status("收到追加指令，会在当前这一步结束后交给 agent")
}

// stop asks the agent to stop, and kills it if it does not stop in time.
func (r *run) stop(reason string) {
	r.mu.Lock()
	if r.stopReason == "" {
		r.stopReason = reason
	}
	client, sid := r.client, r.sessionID
	r.mu.Unlock()
	if client != nil && sid != "" {
		client.Cancel(sid)
		go func() {
			select {
			case <-r.ctx.Done():
			case <-time.After(cancelGrace):
				r.cancel()
			}
		}()
		return
	}
	r.cancel()
}

// kill stops the run without waiting for the agent to wind down.
func (r *run) kill(reason string) {
	r.mu.Lock()
	if r.stopReason == "" {
		r.stopReason = reason
	}
	r.mu.Unlock()
	r.cancel()
}

func (r *run) stopReasonText() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopReason == "" {
		return "已取消"
	}
	return r.stopReason
}

func (r *run) summary() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return truncate(strings.TrimSpace(r.turnText.String()), summaryRunes)
}

func (r *run) sendDiff(diff string) {
	var off int64
	for {
		n := min(len(diff), proto.DiffChunkSize)
		// Never split a UTF-8 sequence across chunks.
		for n < len(diff) && n > 0 && !utf8.RuneStart(diff[n]) {
			n--
		}
		final := n == len(diff)
		r.d.ob.reliable(proto.New(proto.TRunDiff, proto.RunDiff{RunID: r.spec.RunID, Offset: off, Chunk: diff[:n], Final: final}))
		off += int64(n)
		diff = diff[n:]
		if final {
			return
		}
	}
}

func prettyJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	return string(out)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
