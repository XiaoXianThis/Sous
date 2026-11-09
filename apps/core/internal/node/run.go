package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/agents"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/policy"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/workspace"
)

const (
	summaryRunes = 4000
	detailRunes  = 2000
	// cancelGrace is how long an agent gets to stop after session/cancel
	// before its process is killed.
	cancelGrace = 10 * time.Second
	// startAttempts bounds how often a transient failure to start an agent
	// is retried (design §7.6).
	startAttempts = 3
)

// startBackoff is the wait before the second and third start attempt.
var startBackoff = []time.Duration{2 * time.Second, 5 * time.Second}

// Modes that make an agent ask before every change, and modes that let it
// do everything, by the ids agents use for them. "ask" is not among them:
// Cursor uses it for a read-only Q&A mode. Agents whose names differ set
// AskMode / OpenMode in their spec.
var (
	askModes  = []string{"default", "manual"}
	openModes = []string{"bypassPermissions", "yolo", "full-access", "auto"}
)

type run struct {
	streams        map[string]*redactedLine
	approvalScopes map[string]approvalScope
	d              *Daemon
	spec           proto.RunStart

	ctx    context.Context
	cancel context.CancelFunc

	mu          sync.Mutex
	client      *acp.Client
	sessionID   string
	canLoad     bool // the agent can resume the session after a restart
	isStarted   bool
	inTurn      bool
	stopReason  string // set by stop()
	shutdown    bool   // the daemon is stopping: keep the run to resume it
	followUps   []string
	followIDs   []string        // message ids of followUps that have one
	seen        map[string]bool // message ids received, so a resend is dropped
	delivered   []string        // message ids that reached the agent's session
	paused      bool
	pauseReason string
	pauseCut    bool          // a pause cancelled the current turn
	wake        chan struct{} // a paused run has something to do
	approvals   map[string]chan string
	notes       map[string]string // why the user or the approver refused, by approval id
	// refused lists what was refused during the current turn. Some agents
	// (OpenCode) end the turn as "cancelled" when a permission is refused;
	// the run then tells the agent and lets it continue (maxRefusalTurns).
	refused      []string
	refusalTurns int
	// refusedAll is every refusal of the run, reported when it ends.
	refusedAll []string
	// toolCalls are the tool calls the agent made in this run.
	toolCalls  map[string]bool
	toolTitles map[string]string
	// failStreak counts tool calls in a row that failed with the same title;
	// stuckSent marks that the streak was reported (end-to-end review 4.16).
	failTitle      string
	failStreak     int
	stuckSent      bool
	turnText       strings.Builder
	baseline       map[string]string
	approvalLaunch []string
	approvalEnv    []string
	workdir        string
	loading        bool // replaying a loaded session: its updates are old news
	// resumed is set for a run restored after a restart of the daemon.
	resumed *savedRun
	// continued is set when ResumeSession was loaded: the agent remembers
	// the earlier conversation, so the prompt need not repeat it.
	continued bool
	// idle: a consultant between questions.
	idle bool
	// slot: the run holds one of the machine's slots (d.sem).
	slot bool
	// sous: the run's side of sous's MCP server (decision 0025).
	sous sousState
}

func newRun(d *Daemon, s proto.RunStart) *run {
	ctx, cancel := context.WithCancel(context.Background())
	return &run{d: d, spec: s, ctx: ctx, cancel: cancel, approvals: map[string]chan string{}, notes: map[string]string{}, seen: map[string]bool{}, toolCalls: map[string]bool{}, toolTitles: map[string]string{},
		wake: make(chan struct{}, 1)}
}

func (r *run) started() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.isStarted
}

func (r *run) status(text string) {
	r.d.ob.reliable(proto.New(proto.TRunUpdate, proto.RunUpdate{RunID: r.spec.RunID, Kind: proto.UpdateStatus, Text: text}))
}

// end reports the run's end, unless the daemon is stopping: then the run
// stays saved, and the next daemon resumes it or reports it interrupted.
func (r *run) end(status, stopReason, summary, diffstat string, err error) {
	if r.isShutdown() {
		slog.Info("daemon: run left for the next start", "run", r.spec.RunID)
		return
	}
	e := proto.RunEnded{RunID: r.spec.RunID, Status: status, StopReason: stopReason, Summary: summary, DiffStat: diffstat}
	if err != nil {
		e.Error = err.Error()
		if status == "failed" {
			e.ErrorKind = errorKind(err, "")
		}
	}
	r.mu.Lock()
	e.Refused = append([]string(nil), r.refusedAll...)
	if r.canLoad && r.spec.Purpose == proto.PurposeWork {
		e.SessionID = r.sessionID // a later rework or retry can go on in it (0025)
	}
	r.mu.Unlock()
	r.flushStreams()
	e.Summary = mask(e.Summary)
	e.Error = mask(e.Error)
	e.DiffStat = mask(e.DiffStat)
	r.d.ob.reliable(proto.New(proto.TRunEnded, e))
	r.d.forget(r.spec.RunID)
	slog.Info("daemon: run ended", "run", r.spec.RunID, "status", status, "err", err)
}

func (r *run) isShutdown() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.shutdown
}

// execute runs the agent until its turn ends, then reports the diff.
func (r *run) execute() {
	defer r.cancel()
	// A consultant is read only and waits idle most of its life: it does
	// not take one of the machine's slots (decision 0022).
	// Nor does a git operation: fixed commands, no agent.
	if r.spec.Purpose != proto.PurposeConsult && r.spec.Purpose != proto.PurposeGit {
		if !r.takeSlot() {
			r.end("cancelled", "", "", "", errors.New(r.stopReasonText()))
			return
		}
		defer r.releaseSlot()
	}
	if r.spec.Purpose == proto.PurposeDeliver || r.spec.Purpose == proto.PurposeRebase {
		// Automatic delivery is gone (decision 0022): a Hub of this version
		// never sends these; an old queued one ends here.
		r.end("failed", "", "", "", errors.New("自动交付已经去掉了：让 sous 按你的要求提交、合并、推送或开 PR"))
		return
	}
	if r.spec.Purpose == proto.PurposeGit {
		r.gitOp()
		return
	}
	if r.spec.Purpose == proto.PurposeClone || r.spec.Purpose == proto.PurposeCreate {
		r.setup()
		return
	}
	spec, ok := r.d.findSpec(r.spec.Agent)
	if !ok {
		r.end("failed", "", "", "", fmt.Errorf("这台机器不认识 agent %q", r.spec.Agent))
		return
	}
	if r.d.conf().AgentDisabled(spec.Name) {
		r.end("failed", "", "", "", fmt.Errorf("这台机器在 node.json 里停用了 %s（disabled_agents）", spec.Title))
		return
	}

	ws, err := r.prepare()
	if err != nil {
		r.end("failed", "", "", "", fmt.Errorf("准备工作目录失败：%w", err))
		return
	}
	if r.resumed == nil {
		r.markStarted(ws)
	} else {
		r.mu.Lock()
		r.isStarted, r.workdir = true, ws.Dir
		r.mu.Unlock()
	}

	status, stop, err := r.drive(spec, ws)
	if r.isShutdown() {
		return // the agent was killed with the daemon; nothing to report yet
	}
	if r.spec.Purpose == proto.PurposeConsult {
		r.endConsult(status, stop, err)
		return
	}

	if ws.Shadow != "" && r.spec.Purpose != proto.PurposeReview {
		// The directory as the agent left it: what an undo or a commit of
		// the task works from.
		key := r.spec.TaskID
		if key == "" {
			key = r.spec.RunID
		}
		if err := workspace.MarkEnd(context.Background(), ws, key); err != nil {
			slog.Warn("daemon: end snapshot failed", "run", r.spec.RunID, "err", err)
		}
	}
	// Report what changed even when the turn failed part way.
	diff, stat, derr := workspace.Diff(context.Background(), ws)
	if derr != nil {
		slog.Warn("daemon: diff failed", "run", r.spec.RunID, "err", derr)
	} else if diff != "" {
		r.sendDiff(diff)
	}
	summary := r.summary()
	if status == "succeeded" && (r.spec.Purpose == "" || r.spec.Purpose == proto.PurposeWork) {
		r.mu.Lock()
		calls := len(r.toolCalls)
		r.mu.Unlock()
		st, kind, out := outcome(calls, stat, summary)
		if st == "failed" {
			r.endWith(proto.RunEnded{Status: st, StopReason: stop, Summary: summary, DiffStat: stat, ErrorKind: kind,
				Error: "agent 没做任何操作就结束了，它说：" + truncate(summary, 300)})
			return
		}
		if out != "" {
			r.endWith(proto.RunEnded{Status: status, StopReason: stop, Summary: summary, DiffStat: stat, Outcome: out})
			return
		}
	}
	r.end(status, stop, summary, stat, err)
}

// stuckFailures is after how many failures in a row of the same tool call
// the node tells the Hub, which pauses the run: sooner than the inspector's
// round, and without a model.
const stuckFailures = 4

// trackFailure follows failed tool calls; it returns what to report once a
// streak reaches stuckFailures.
func (r *run) trackFailure(callID, title, status string) string {
	if status != "completed" && status != "failed" {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if title == "" {
		// Updates often carry only the id; the title came with the call.
		title = r.toolTitles[callID]
	}
	if status == "completed" {
		r.failTitle, r.failStreak, r.stuckSent = "", 0, false
		return ""
	}
	if title != r.failTitle {
		r.failTitle, r.failStreak, r.stuckSent = title, 0, false
	}
	r.failStreak++
	if r.failStreak >= stuckFailures && !r.stuckSent {
		r.stuckSent = true
		return fmt.Sprintf("「%s」连续失败了 %d 次", truncate(firstNonEmpty(title, "同一个操作"), 120), r.failStreak)
	}
	return ""
}

// endWith reports a run's end as given, with the run's refusals.
func (r *run) endWith(e proto.RunEnded) {
	if r.isShutdown() {
		return
	}
	e.RunID = r.spec.RunID
	r.mu.Lock()
	e.Refused = append([]string(nil), r.refusedAll...)
	if e.SessionID == "" && r.canLoad && r.spec.Purpose == proto.PurposeWork {
		e.SessionID = r.sessionID // a later rework or retry can go on in it (0025)
	}
	r.mu.Unlock()
	r.flushStreams()
	e.Summary = mask(e.Summary)
	e.Error = mask(e.Error)
	e.DiffStat = mask(e.DiffStat)
	r.d.ob.reliable(proto.New(proto.TRunEnded, e))
	r.d.forget(r.spec.RunID)
	slog.Info("daemon: run ended", "run", r.spec.RunID, "status", e.Status, "outcome", e.Outcome, "kind", e.ErrorKind)
}

// prepare creates or reuses the run's working directory. All runs of a
// task share one: its worktree and branch, or its scratch directory.
func (r *run) prepare() (workspace.Prepared, error) {
	key := r.spec.TaskID
	if key == "" {
		key = r.spec.RunID
	}
	if r.spec.Purpose == proto.PurposeConsult {
		// The consultant reads the project directory as it is: no snapshot,
		// it changes nothing.
		if r.spec.RepoPath == "" {
			return workspace.Prepared{}, errors.New("顾问需要项目在这台机器上的目录")
		}
		return workspace.PrepareInPlace(r.spec.RepoPath)
	}
	if r.spec.Kind == proto.KindOps || r.spec.Kind == proto.KindInPlace {
		if r.spec.RepoPath == "" {
			return workspace.Prepared{}, errors.New("就地执行的任务需要项目在这台机器上的目录")
		}
		ws, err := workspace.PrepareInPlaceTask(r.ctx, r.d.root, r.spec.RepoPath, key)
		if err == nil && ws.Note != "" && r.resumed == nil {
			r.status(ws.Note)
		}
		return ws, err
	}
	if r.spec.RepoPath != "" {
		if r.resumed == nil && r.spec.Purpose != proto.PurposeReview {
			r.status("正在准备独立的 git worktree")
		}
		ws, err := workspace.PrepareTaskOn(r.ctx, r.d.root, r.spec.RepoPath, key, r.spec.Branch, workspace.Slug(r.spec.Title),
			workspace.Start{Branch: r.spec.BaseBranch, From: r.spec.BaseFrom})
		if err == nil && ws.Note != "" {
			r.status(ws.Note)
		}
		return ws, err
	}
	return workspace.PrepareGeneral(r.d.root, key)
}

func (r *run) markStarted(ws workspace.Prepared) {
	r.mu.Lock()
	r.isStarted = true
	r.workdir = ws.Dir
	r.spec.Branch = ws.Branch // a resumed run finds the same worktree
	r.mu.Unlock()
	if r.resumed == nil {
		r.evidenceBaseline(ws.Dir)
	}
	r.save()
	r.d.ob.reliable(proto.New(proto.TRunStarted, proto.RunStarted{RunID: r.spec.RunID, Workdir: ws.Dir, Branch: ws.Branch, BaseBranch: ws.BaseBranch,
		BaseCommit: ws.Base}))
}

// open starts the agent and opens (or, after a restart, loads) its session.
// Transient failures are retried a few times.
func (r *run) open(spec agents.Spec, ws workspace.Prepared) (*acp.Client, acp.Session, error) {
	var lastErr error
	for attempt := 1; attempt <= startAttempts; attempt++ {
		client, session, err := r.openOnce(spec, ws)
		if err == nil {
			return client, session, nil
		}
		lastErr = err
		if r.ctx.Err() != nil || errors.Is(err, acp.ErrNotLoggedIn) || !transient(err) || attempt == startAttempts {
			break
		}
		wait := startBackoff[attempt-1]
		r.status(fmt.Sprintf("启动 %s 遇到临时故障，%d 秒后重试（第 %d 次）：%s", spec.Title, int(wait.Seconds()), attempt, truncate(err.Error(), 200)))
		select {
		case <-r.ctx.Done():
			return nil, acp.Session{}, err
		case <-time.After(wait):
		}
	}
	return nil, acp.Session{}, lastErr
}

func (r *run) openOnce(spec agents.Spec, ws workspace.Prepared) (*acp.Client, acp.Session, error) {
	cmd, err := spec.Command(ws.Dir)
	if err != nil {
		return nil, acp.Session{}, fmt.Errorf("找不到 %s 的启动命令 %q：%w", spec.Title, spec.Launch[0], err)
	}
	r.mu.Lock()
	r.approvalLaunch = append([]string{cmd.Path}, cmd.Args...)
	r.approvalEnv = append(append([]string{}, os.Environ()...), cmd.Env...)
	r.mu.Unlock()
	r.status("正在启动 " + spec.Title)
	client, err := acp.Start(r.ctx, cmd, acp.Callbacks{OnUpdate: r.onUpdate, OnPermission: r.onPermission})
	if err != nil {
		return nil, acp.Session{}, fmt.Errorf("启动 %s 失败：%w", spec.Title, err)
	}
	client.SetMCPServers(r.d.sousServers(r))
	if want := r.spec.ResumeSession; r.resumed == nil && want != "" {
		// A consultant that went idle, or a task that grew out of a
		// discussion: go on in the agent's earlier session when it can.
		if client.Init.LoadSession() {
			r.mu.Lock()
			r.loading = true
			r.mu.Unlock()
			session, err := client.LoadSession(r.ctx, want, ws.Dir)
			r.mu.Lock()
			r.loading = false
			r.mu.Unlock()
			if err == nil {
				r.mu.Lock()
				r.continued = true
				r.mu.Unlock()
				r.status("已接上 " + spec.Title + " 之前的会话")
				return client, session, nil
			}
			r.status("没能接上之前的会话（" + truncate(err.Error(), 120) + "），开一个新会话")
		} else {
			r.status(spec.Title + " 不支持恢复会话，开一个新会话")
		}
	}
	if sv := r.resumed; sv != nil && sv.SessionID != "" && client.Init.LoadSession() {
		r.mu.Lock()
		r.loading = true
		r.mu.Unlock()
		session, err := client.LoadSession(r.ctx, sv.SessionID, ws.Dir)
		r.mu.Lock()
		r.loading = false
		r.mu.Unlock()
		if err == nil {
			r.status("sous 重启过，已恢复 " + spec.Title + " 之前的会话，继续执行")
			return client, session, nil
		}
		client.Close()
		return nil, acp.Session{}, fmt.Errorf("恢复 %s 的会话失败：%w", spec.Title, err)
	}
	session, err := client.NewSession(r.ctx, ws.Dir)
	if err != nil {
		client.Close()
		if errors.Is(err, acp.ErrNotLoggedIn) {
			return nil, acp.Session{}, fmt.Errorf("%s 还没有登录，请先在这台电脑上登录：%w", spec.Title, err)
		}
		return nil, acp.Session{}, err
	}
	return client, session, nil
}

// transientRE matches failures worth another try: network trouble and
// overloaded services (HTTP 408, 429, 5xx).
var transientRE = regexp.MustCompile(`(?i)ECONNRESET|ECONNREFUSED|ETIMEDOUT|EAI_AGAIN|connection (reset|refused)|timed? ?out|timeout|\b(408|429|50[0-9])\b|too many requests|overloaded|service unavailable|bad gateway|temporarily`)

func transient(err error) bool { return err != nil && transientRE.MatchString(err.Error()) }

// drive starts the agent, opens a session and sends the prompt, then any
// follow-up messages that arrived meanwhile. A paused run waits between
// turns until it is resumed.
func (r *run) drive(spec agents.Spec, ws workspace.Prepared) (status, stop string, err error) {
	client, session, err := r.open(spec, ws)
	if err != nil {
		if r.resumed != nil && r.ctx.Err() == nil {
			// The session could not be resumed: the Hub starts the task
			// again with a recovery note.
			return "interrupted", "", fmt.Errorf("sous 重启后没能恢复 agent 的会话：%w", err)
		}
		return r.failure(err)
	}
	defer client.Close()
	r.mu.Lock()
	r.client, r.sessionID, r.canLoad = client, session.ID, client.Init.LoadSession()
	r.mu.Unlock()
	r.save()
	if len(session.Options) > 0 {
		forced := spec.AskOptions
		if r.spec.PermissionMode == proto.ModeOpen {
			forced = spec.OpenOptions
		}
		opts, err := r.d.admin.applyDefaults(r.ctx, client, session, spec, forced, r.spec.Options, r.status)
		if err != nil {
			return r.failure(err)
		}
		session.Options = opts
		r.d.admin.remember(spec, session.Options)
	}
	if mode := pickMode(session, r.spec.PermissionMode, spec); mode != "" && mode != session.CurrentMode {
		if err := client.SetMode(r.ctx, session.ID, mode); err != nil {
			slog.Warn("daemon: set mode failed", "run", r.spec.RunID, "mode", mode, "err", err)
		}
	}

	var prompt string
	var promptIDs []string
	if r.resumed != nil {
		prompt = "（sous 在这台电脑上重启过，你上一轮的执行被打断了。先看一下工作目录和 git 状态，确认做到了哪一步，再接着完成任务；不要重复已经完成的对外操作，比如 push。）"
		if more, ids := r.takeFollowUps(); len(more) > 0 {
			prompt += "\n\n" + strings.Join(more, "\n\n")
			promptIDs = ids
		}
	} else {
		prompt = r.firstPrompt(ws)
	}
	for {
		text, ids, ok := r.waitIfPaused()
		if !ok {
			return "cancelled", "cancelled", errors.New(r.stopReasonText())
		}
		if text != "" {
			if prompt != "" {
				text = prompt + "\n\n" + text
				ids = append(promptIDs, ids...)
			}
			prompt, promptIDs = text, ids
		}
		if prompt == "" {
			if more, ids := r.takeFollowUps(); len(more) > 0 {
				prompt, promptIDs = strings.Join(more, "\n\n"), ids
			}
		}
		if prompt == "" {
			prompt = "用户让你继续。请接着之前的工作做下去；如果刚才是在重复同一个操作，换一个办法。"
		}
		r.mu.Lock()
		r.turnText.Reset()
		r.inTurn = true
		r.pauseCut = false
		r.refused = nil
		r.mu.Unlock()
		r.markDelivered(promptIDs)
		stop, err = client.Prompt(r.ctx, session.ID, prompt)
		r.mu.Lock()
		r.inTurn = false
		paused := r.paused || r.pauseCut
		r.pauseCut = false
		refused, stopped := r.refused, r.stopReason != ""
		r.mu.Unlock()
		if err != nil {
			return r.failure(err)
		}
		prompt, promptIDs = "", nil
		if stop == "cancelled" && r.ctx.Err() == nil && paused {
			continue // wait for the resume at the top of the loop
		}
		if stop == "cancelled" && (stopped || r.ctx.Err() != nil) {
			return "cancelled", stop, errors.New(r.stopReasonText())
		}
		if stop == "cancelled" {
			// Nobody stopped the run: the agent gave up its turn, usually
			// because an operation was refused. Tell it and let it go on
			// another way, a few times; then report a failure, not a
			// cancellation, so the task is reported instead of shelved.
			if len(refused) == 0 || r.refusalTurns >= maxRefusalTurns {
				why := "agent 自己结束了这一轮"
				if len(refused) > 0 {
					why = "agent 的操作多次被拒绝后停了下来：" + strings.Join(refused, "；")
				}
				return "failed", stop, errors.New(why)
			}
			r.refusalTurns++
			prompt = refusalPrompt(refused)
			if more, ids := r.takeFollowUps(); len(more) > 0 {
				prompt += "\n\n" + strings.Join(more, "\n\n")
				promptIDs = ids
			}
			r.status("操作被拒绝，让 agent 换个办法继续")
			continue
		}
		if r.spec.Purpose == proto.PurposeConsult && stop == "end_turn" {
			// A consultant answers, then waits for the next question.
			r.reportTurn()
			text, ids, ok := r.waitQuestion()
			if !ok {
				if r.ctx.Err() != nil || r.stopRequested() {
					return "cancelled", "cancelled", errors.New(r.stopReasonText())
				}
				return "succeeded", "idle", nil
			}
			prompt, promptIDs = text, ids
			continue
		}
		more, ids2 := r.takeFollowUps()
		if len(more) == 0 {
			if r.isPaused() {
				continue
			}
			break
		}
		prompt, promptIDs = strings.Join(more, "\n\n"), ids2
	}
	if stop == "end_turn" {
		return "succeeded", stop, nil
	}
	return "failed", stop, fmt.Errorf("agent 停止：%s", stop)
}

// waitIfPaused blocks while the run is paused. Once it is resumed it
// returns the follow-ups that arrived meanwhile (maybe none); it returns
// false when the run is stopped instead.
func (r *run) waitIfPaused() (string, []string, bool) {
	announced := false
	for {
		r.mu.Lock()
		paused, reason := r.paused, r.pauseReason
		r.mu.Unlock()
		if !paused {
			if !announced {
				return "", nil, true
			}
			more, ids := r.takeFollowUps()
			r.status("继续执行")
			return strings.Join(more, "\n\n"), ids, true
		}
		if !announced {
			announced = true
			r.status("已暂停：" + firstNonEmpty(reason, "等你处理") + "。发一条消息或点“继续”就会接着做。")
		}
		select {
		case <-r.ctx.Done():
			return "", nil, false
		case <-r.wake:
		}
	}
}

func (r *run) isPaused() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paused
}

// pause stops the agent's current turn and holds the run.
func (r *run) pause(reason string) {
	r.mu.Lock()
	if r.paused {
		r.mu.Unlock()
		return
	}
	r.paused, r.pauseReason = true, reason
	client, sid, inTurn := r.client, r.sessionID, r.inTurn
	r.pauseCut = inTurn
	r.mu.Unlock()
	r.save()
	r.cancelApprovals()
	if client != nil && sid != "" && inTurn {
		client.Cancel(sid)
	}
	slog.Info("daemon: run paused", "run", r.spec.RunID, "reason", reason)
}

// resume continues a paused run.
func (r *run) resume() {
	r.mu.Lock()
	r.paused, r.pauseReason = false, ""
	r.mu.Unlock()
	r.save()
	r.signal()
}

func (r *run) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *run) takeFollowUps() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	more, ids := r.followUps, r.followIDs
	r.followUps, r.followIDs = nil, nil
	return more, ids
}

// markDelivered tells the Hub which follow-ups are going into the agent's
// session now, so after a restart only the others are sent again (SA-16).
func (r *run) markDelivered(ids []string) {
	if len(ids) == 0 {
		return
	}
	r.mu.Lock()
	r.delivered = append(r.delivered, ids...)
	r.mu.Unlock()
	r.save()
	r.d.ob.reliable(proto.New(proto.TRunDelivered, proto.RunDelivered{RunID: r.spec.RunID, MessageIDs: ids}))
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
	r.mu.Lock()
	continued := r.continued
	r.mu.Unlock()
	if continued && r.spec.ContinuePrompt != "" {
		// The agent remembers the task and its earlier rounds (0025).
		b.WriteString(r.spec.ContinuePrompt)
		return b.String()
	}
	b.WriteString(r.spec.Prompt)
	switch {
	case r.spec.Purpose == proto.PurposeConsult || r.spec.Purpose == proto.PurposeReview:
		// The Hub's prompt says it all; the session is read only.
	case r.spec.Kind == proto.KindOps || r.spec.Kind == proto.KindInPlace:
		fmt.Fprintf(&b, "\n\n（说明：你直接在项目的目录 %s 里工作，没有 worktree。sous 在你开始前拍了快照，改动可以查看和撤销。不要提交、push 或切换分支，git 操作由 sous 按用户的要求处理。）", ws.Dir)
		if r.d.role() == proto.RoleServer {
			b.WriteString("\n（这台机器是服务器：改文件、删除、启停服务都要用户批准；按部署脚本执行，不要临时修改服务器上的文件。）")
		}
	case ws.Branch != "":
		fmt.Fprintf(&b, "\n\n（说明：你在 sous 为这个任务新建的 git worktree 里工作，分支是 %s。可以提交到这个分支，但不要 push、不要切换分支；合并、推送、开 PR 由 sous 按用户的要求处理。完成后简要说明改了什么。）", ws.Branch)
	}
	return b.String()
}

func pickMode(s acp.Session, permission string, spec agents.Spec) string {
	want := askModes
	if spec.AskMode != "" {
		want = []string{spec.AskMode}
	}
	if permission == proto.ModeOpen {
		want = openModes
		if spec.OpenMode != "" {
			want = []string{spec.OpenMode}
		}
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
	r.mu.Lock()
	loading := r.loading
	r.mu.Unlock()
	if loading {
		return // the replay of a loaded session: already reported before the restart
	}
	id := r.spec.RunID
	switch u.Kind {
	case "agent_message_chunk":
		r.mu.Lock()
		r.turnText.WriteString(u.Text)
		r.mu.Unlock()
		r.streamText(proto.UpdateText, u.Text, false)
	case "agent_thought_chunk":
		r.streamText(proto.UpdateThought, u.Text, false)
	case "tool_call", "tool_call_update":
		if u.ToolCallID != "" {
			r.mu.Lock()
			r.toolCalls[u.ToolCallID] = true
			if u.Title != "" {
				r.toolTitles[u.ToolCallID] = u.Title
			}
			r.mu.Unlock()
		}
		up := proto.RunUpdate{RunID: id, Kind: proto.UpdateTool, ToolCallID: u.ToolCallID, Title: mask(u.Title), Status: u.Status, ToolKind: u.ToolKind}
		if u.Status == "completed" || u.Status == "failed" {
			up.Output = toolOutput(u.Raw)
		}
		r.d.ob.reliable(proto.New(proto.TRunUpdate, up))
		if what := r.trackFailure(u.ToolCallID, u.Title, u.Status); what != "" {
			r.d.ob.reliable(proto.New(proto.TRunUpdate, proto.RunUpdate{RunID: id, Kind: proto.UpdateStuck, Text: what}))
		}
	case "plan":
		plan := make([]proto.PlanEntry, len(u.Plan))
		for i, e := range u.Plan {
			plan[i] = proto.PlanEntry{Content: mask(e.Content), Priority: e.Priority, Status: e.Status}
		}
		r.d.ob.reliable(proto.New(proto.TRunUpdate, proto.RunUpdate{RunID: id, Kind: proto.UpdatePlan, Plan: plan}))
	}
}

// onPermission classifies what the agent asks to do and decides by the
// run's permission mode (design §8): allow or refuse at once, or ask the
// Hub, where the user or the approver agent answers.
func (r *run) onPermission(ctx context.Context, req acp.PermissionRequest) acp.Decision {
	r.mu.Lock()
	workdir, paused := r.workdir, r.paused
	r.mu.Unlock()
	if paused {
		return acp.Decision{} // the turn is being cancelled
	}
	paths := make([]string, len(req.ToolCall.Locations))
	for i, l := range req.ToolCall.Locations {
		paths[i] = l.Path
	}
	preq := policy.Request{
		ToolKind: req.ToolCall.Kind, Title: req.ToolCall.Title, RawInput: req.ToolCall.RawInput, Paths: paths, Workdir: workdir,
	}
	cat, why := r.d.pol().Classify(preq)
	title := req.ToolCall.Title
	if title == "" {
		title = "agent 请求执行一个操作"
	}
	facts, evidence, newCat, assess := r.approvalEvidence(preq, cat)
	cat = newCat
	act, reason := policy.Judge(r.d.role(), r.spec.PermissionMode, cat, assess)
	if cat != policy.Floor && r.isBuiltinSousRequest(req) {
		r.recordPolicy(title+"（内建 Sous 通知工具）", policy.Read, "allow")
		return acp.Decision{OptionID: pickOption(req.Options, "allow_once")}
	}
	review := "user"
	switch act {
	case policy.Allow:
		r.recordPolicy(title+reasonSuffix(reason), cat, "allow")
		return acp.Decision{OptionID: pickOption(req.Options, "allow_once", "allow_always")}
	case policy.Deny:
		r.recordPolicy(title+"（"+firstNonEmpty(reason, why)+"）", cat, "deny")
		r.noteRefusal(title, "sous 的安全规则禁止："+firstNonEmpty(reason, why))
		return acp.Decision{OptionID: pickOption(req.Options, "reject_once", "reject_always")}
	case policy.AskAI:
		review = "ai"
	}
	if note := assess.Note(); note != "" {
		why = strings.TrimSpace(note + "\n" + why)
	}
	detail := permissionDetail(why, req.ToolCall.RawInput)
	opt, note := r.askWithEvidence(title, req.ToolCall.Kind, detail, cat, review, toOptions(req.Options), facts, evidence)

	if kind := optionKind(req.Options, opt); kind == "allow_once" || kind == "allow_always" {
		if !revalidateEvidence(evidence, workdir) {
			r.noteRefusal(title, "证据已变化，请重新申请")
			return acp.Decision{OptionID: pickOption(req.Options, "reject_once", "reject_always")}
		}
		if facts.Reusable {
			current, _, _, _ := r.approvalEvidence(preq, cat)
			if current.OperationKey != facts.OperationKey {
				r.noteRefusal(title, "执行身份已变化，请重新申请")
				return acp.Decision{OptionID: pickOption(req.Options, "reject_once", "reject_always")}
			}
		}
	}
	if kind := optionKind(req.Options, opt); kind == "reject_once" || kind == "reject_always" {
		r.noteRefusal(title, note)
	}
	return acp.Decision{OptionID: opt}
}

// judge is the mode table refined by the policy's assessment of the
// request: hard signals go to the user, routine actions skip the approver
// in the auto mode (docs/decisions/0019). reason says why when the
// assessment changed the table's answer.
func (r *run) judge(req policy.Request, cat policy.Category) (policy.Action, policy.Assessment, string) {
	a := r.d.pol().Assess(req, cat)
	act, reason := policy.Judge(r.d.role(), r.spec.PermissionMode, cat, a)
	return act, a, reason
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return "（" + reason + "）"
}

// maxRefusalTurns is how many times a run continues after the agent ended
// its turn because an operation was refused.
const maxRefusalTurns = 3

func (r *run) noteRefusal(title, note string) {
	r.recordPolicy(mask(title)+reasonSuffix(mask(note)), policy.ExecOther, "deny")
	title = mask(title)
	note = mask(note)
	s := "「" + truncate(title, 120) + "」"
	if note = strings.TrimSpace(note); note != "" {
		s += "（理由：" + truncate(note, 200) + "）"
	}
	r.mu.Lock()
	r.refused = append(r.refused, s)
	r.refusedAll = append(r.refusedAll, s)
	r.mu.Unlock()
}

func refusalPrompt(refused []string) string {
	return "你刚才请求的这些操作被拒绝了：" + strings.Join(refused, "；") + "。\n\n" +
		"不要再尝试同样的操作，换一种不需要它们的办法继续完成任务。如果没有它们确实做不下去，说明缺什么，然后结束这一轮。"
}

// optionKind returns the kind of the option with the given id.
func optionKind(opts []acp.PermissionOption, id string) string {
	for _, o := range opts {
		if o.OptionID == id {
			return o.Kind
		}
	}
	return ""
}

func permissionDetail(why string, raw json.RawMessage) string {
	d := maskJSON(raw)
	if why != "" && !strings.Contains(d, why) {
		d = why + "\n\n" + d
	}
	return truncate(mask(strings.TrimSpace(d)), detailRunes)
}

func toOptions(in []acp.PermissionOption) []proto.Option {
	out := make([]proto.Option, len(in))
	for i, o := range in {
		out[i] = proto.Option{OptionID: o.OptionID, Name: o.Name, Kind: o.Kind}
	}
	return out
}

// pickOption returns the first option of the given kinds, in order.
func pickOption(opts []acp.PermissionOption, kinds ...string) string {
	for _, k := range kinds {
		for _, o := range opts {
			if o.Kind == k {
				return o.OptionID
			}
		}
	}
	return ""
}

// recordPolicy reports a decision the node made by itself, so every
// approval, automatic or not, is in the event log (requirements R5.5).
func (r *run) recordPolicy(title string, cat policy.Category, decision string) {
	r.d.ob.reliable(proto.New(proto.TRunUpdate, proto.RunUpdate{
		RunID: r.spec.RunID, Kind: proto.UpdatePermission, Title: mask(title), Category: string(cat), Status: decision,
	}))
}

// ask sends an approval request to the Hub and waits for the answer: the
// chosen option id and the decider's note, or "" when the run stops or
// pauses first.
func (r *run) ask(title, toolKind, detail string, cat policy.Category, review string, opts []proto.Option) (string, string) {
	return r.askWithEvidence(title, toolKind, detail, cat, review, opts, nil, nil)
}
func (r *run) askWithEvidence(title, toolKind, detail string, cat policy.Category, review string, opts []proto.Option, facts *proto.PolicyFacts, evidence *proto.ApprovalEvidence) (string, string) {
	approvalID := ids.New()
	ch := make(chan string, 1)
	r.mu.Lock()
	r.approvals[approvalID] = ch
	if r.approvalScopes == nil {
		r.approvalScopes = map[string]approvalScope{}
	}
	scope := approvalScope{root: r.workdir}
	if evidence != nil {
		scope.scripts = evidence.Scripts
	}
	r.approvalScopes[approvalID] = scope
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.approvals, approvalID)
		delete(r.approvalScopes, approvalID)
		delete(r.notes, approvalID)
		r.mu.Unlock()
	}()
	r.d.ob.reliable(proto.New(proto.TApprovalRequest, proto.ApprovalRequest{
		ApprovalID: approvalID, RunID: r.spec.RunID, Title: mask(title), ToolKind: toolKind,
		Detail: mask(detail), Options: opts, Category: string(cat), Review: review, Facts: facts, Evidence: evidence,
	}))
	// Waiting for the user can take hours: give the machine's slot to other
	// runs meanwhile, and take one again once answered.
	released := review == "user" && r.releaseSlot()
	select {
	case opt := <-ch:
		r.mu.Lock()
		note := r.notes[approvalID]
		r.mu.Unlock()
		if released && !r.takeSlot() {
			return "", ""
		}
		return opt, note
	case <-r.ctx.Done():
		return "", ""
	}
}

// takeSlot waits for one of the machine's slots; false when the run was
// stopped meanwhile. A run that holds one already keeps it.
func (r *run) takeSlot() bool {
	r.mu.Lock()
	held := r.slot
	r.mu.Unlock()
	if held {
		return true
	}
	select {
	case r.d.sem <- struct{}{}:
		r.mu.Lock()
		r.slot = true
		r.mu.Unlock()
		return true
	case <-r.ctx.Done():
		return false
	}
}

// releaseSlot gives the run's slot back; false when it held none.
func (r *run) releaseSlot() bool {
	r.mu.Lock()
	held := r.slot
	r.slot = false
	r.mu.Unlock()
	if held {
		<-r.d.sem
	}
	return held
}

func (r *run) decide(approvalID, optionID, note string) {
	r.mu.Lock()
	ch := r.approvals[approvalID]
	if ch != nil && note != "" {
		r.notes[approvalID] = note
	}
	r.mu.Unlock()
	if ch != nil {
		select {
		case ch <- optionID:
		default:
		}
	}
}

// cancelApprovals answers every open approval with "cancelled".
func (r *run) cancelApprovals() {
	r.mu.Lock()
	chans := make([]chan string, 0, len(r.approvals))
	for _, ch := range r.approvals {
		chans = append(chans, ch)
	}
	r.mu.Unlock()
	for _, ch := range chans {
		select {
		case ch <- "":
		default:
		}
	}
}

// followUp queues an instruction for after the agent's current turn. A
// message the run already has (resent after a reconnect) is dropped. A
// message to a paused run resumes it.
func (r *run) followUp(text, messageID string) {
	r.mu.Lock()
	if messageID != "" {
		if r.seen[messageID] {
			r.mu.Unlock()
			return
		}
		r.seen[messageID] = true
	}
	r.mu.Unlock()
	if r.answerAsk(text) {
		// The agent asked the supervisor and waits in the tool call: the
		// message is the answer (decision 0025).
		r.mu.Lock()
		if messageID != "" {
			r.delivered = append(r.delivered, messageID)
		}
		r.mu.Unlock()
		return
	}
	r.mu.Lock()
	if messageID != "" {
		r.followIDs = append(r.followIDs, messageID)
	}
	r.followUps = append(r.followUps, text)
	wasPaused, idle := r.paused, r.idle
	r.paused, r.pauseReason = false, ""
	r.mu.Unlock()
	if wasPaused {
		r.save()
		r.signal()
		return
	}
	if idle {
		r.signal() // a consultant waiting for the next question
		return
	}
	r.status("收到追加指令，会在当前这一步结束后交给 agent")
}

// stop asks the agent to stop, and kills it if it does not stop in time.
func (r *run) stop(reason string) {
	r.mu.Lock()
	if r.stopReason == "" {
		r.stopReason = reason
	}
	client, sid, inTurn := r.client, r.sessionID, r.inTurn
	r.mu.Unlock()
	if client != nil && sid != "" && inTurn {
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

// kill stops the run without waiting for the agent to wind down. With
// keep, the run is left saved for the next daemon (a shutdown).
func (r *run) kill(reason string, keep bool) {
	r.mu.Lock()
	if r.stopReason == "" {
		r.stopReason = reason
	}
	r.shutdown = keep
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
	s := tail(strings.TrimSpace(r.turnText.String()), summaryRunes)
	if c := r.sous.result; c != nil && !strings.Contains(s, "## 结果") {
		s = strings.TrimSpace(s + "\n\n" + resultSection(c))
	}
	return s
}

// tail keeps the last n runes: an agent closes its turn with the summary
// and the "## 结果" section the Hub reads (decision 0025).
func tail(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return "…" + string(r[len(r)-n:])
	}
	return s
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
	// Shell commands are full of < > &: keep them readable.
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(v)
	return strings.TrimSpace(b.String())
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
