// Package nodehub is the Hub's side of its machines: pairing, the /node
// WebSocket that daemons connect to, dispatching runs, and relaying what
// runs report (progress, approvals, diffs) into the event log.
//
// The Hub never runs anything itself (docs/decisions/0002): it only sends
// run.start to a node and records what comes back.
package nodehub

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/redact"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

const (
	pairingCodeTTL = 10 * time.Minute
	sendBuffer     = 256
	// maxPairFailures wrong codes in a row burn every outstanding code, so
	// six digits cannot be guessed: an attacker gets this many tries out
	// of a million per code the user creates.
	maxPairFailures = 5
)

var (
	ErrNoNode      = errors.New("no machine can take this run")
	ErrUnknownNode = errors.New("unknown machine")
)

type Hub struct {
	log     *eventlog.Log
	runsDir string

	mu       sync.Mutex
	conns    map[string]*nodeConn // by node id
	codes    map[string]time.Time // pairing code -> expiry
	failures int                  // wrong pairing codes since the last good one

	// Set by the Hub from its auth: the login session a request belongs to
	// (Owner, for shells), and the check that it logged in recently
	// (Fresh, answering 401 itself when not).
	Owner             func(*http.Request) string
	Fresh             func(http.ResponseWriter, *http.Request) bool
	MaxAIAllowsPerRun int // local Hub configuration; zero uses 150
}

func (h *Hub) owner(r *http.Request) string {
	if h.Owner == nil {
		return ""
	}
	return h.Owner(r)
}

// RequireFresh reports whether the request may do something that needs a
// recent passkey login, and answers it otherwise.
func (h *Hub) RequireFresh(w http.ResponseWriter, r *http.Request) bool {
	return h.Fresh == nil || h.Fresh(w, r)
}

// SessionOwner is the opaque id of the request's login session.
func (h *Hub) SessionOwner(r *http.Request) string { return h.owner(r) }

// New creates the node hub. Run logs and diffs go to dataDir/runs.
func New(log *eventlog.Log, dataDir string) (*Hub, error) {
	dir := filepath.Join(dataDir, "runs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Hub{log: log, runsDir: dir, conns: map[string]*nodeConn{}, codes: map[string]time.Time{}}, nil
}

// NewPairingCode returns a six-digit code a daemon can trade for a device
// token within ten minutes.
func (h *Hub) NewPairingCode() (string, time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for c, exp := range h.codes {
		if now.After(exp) {
			delete(h.codes, c)
		}
	}
	for {
		n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
		code := fmt.Sprintf("%06d", n.Int64())
		if _, taken := h.codes[code]; !taken {
			exp := now.Add(pairingCodeTTL)
			h.codes[code] = exp
			return code, exp
		}
	}
}

func (h *Hub) takeCode(code string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	exp, ok := h.codes[code]
	delete(h.codes, code)
	if ok && time.Now().Before(exp) {
		h.failures = 0
		return true
	}
	h.failures++
	if h.failures >= maxPairFailures && len(h.codes) > 0 {
		slog.Warn("nodehub: too many wrong pairing codes, every outstanding code is revoked", "failures", h.failures)
		clear(h.codes)
	}
	return false
}

// Pair registers a new machine and returns its device token.
func (h *Hub) Pair(ctx context.Context, req proto.PairRequest) (proto.PairResponse, error) {
	if !h.takeCode(strings.TrimSpace(req.Code)) {
		return proto.PairResponse{}, errors.New("配对码无效或已过期")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = req.Hostname
	}
	if name == "" {
		name = "未命名机器"
	}
	nodeID := ids.New()
	token := randomToken()
	if _, err := h.log.Append(ctx, event.New(event.NodePaired, event.ActorUser, "", event.NodePayload{
		NodeID: nodeID, Name: name, OS: req.OS, Arch: req.Arch, Hostname: req.Hostname,
	})); err != nil {
		return proto.PairResponse{}, err
	}
	if err := h.log.Store.SetNodeToken(ctx, nodeID, hashToken(token)); err != nil {
		return proto.PairResponse{}, err
	}
	slog.Info("nodehub: machine paired", "node", nodeID, "name", name)
	return proto.PairResponse{NodeID: nodeID, Token: token}, nil
}

// Revoke removes a machine and disconnects it.
func (h *Hub) Revoke(ctx context.Context, nodeID string) error {
	if _, err := h.log.Append(ctx, event.New(event.NodeRevoked, event.ActorUser, "", event.NodePayload{NodeID: nodeID})); err != nil {
		return err
	}
	if c := h.conn(nodeID); c != nil {
		c.close("revoked")
	}
	return nil
}

// Online reports whether a node is connected.
func (h *Hub) Online(nodeID string) bool { return h.conn(nodeID) != nil }

func (h *Hub) conn(nodeID string) *nodeConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conns[nodeID]
}

// CreateRun is a request to run an agent.
type CreateRun struct {
	ThreadID string
	NodeID   string // empty: pick the online machine that has the agent
	Agent    string
	RepoPath string // empty: a general run in a scratch directory
	Prompt   string

	// Task runs (the task engine fills these in).
	TaskID         string
	Purpose        string // work / rebase / deliver
	PermissionMode string // empty: default
	Branch         string
	BaseBranch     string
	Title          string
	Force          bool
	RecoveryNote   string // a run that picks up after an interrupted one

	// M5
	Kind    string            // empty: code with a RepoPath, else general; "ops" / "in_place" run in place
	Options map[string]string // the persona's session options
	Persona string            // the persona's name
	Setup   *proto.Setup      // clone and create runs

	// RunID, when set, is the new run's id (a caller that waits for the run
	// registers for it first).
	RunID string

	// Decision 0022.
	BaseFrom      string       // where a new task branch starts: local / remote
	Git           *proto.GitOp // git runs
	ProjectID     string       // consult runs
	ResumeSession string       // an agent session to load
	// ContinuePrompt replaces Prompt when ResumeSession loads (0025).
	ContinuePrompt string
}

// Create queues a run and dispatches it if its machine is connected.
func (h *Hub) Create(ctx context.Context, r CreateRun) (store.Run, error) {
	r.Agent = strings.TrimSpace(r.Agent)
	r.Prompt = strings.TrimSpace(r.Prompt)
	r.RepoPath = strings.TrimSpace(r.RepoPath)
	setup := r.Purpose == proto.PurposeClone || r.Purpose == proto.PurposeCreate
	noAgent := setup || r.Purpose == proto.PurposeDeliver || r.Purpose == proto.PurposeGit
	if noAgent {
		r.Agent = "sous" // the node itself runs fixed commands
	}
	if setup && r.Setup == nil {
		return store.Run{}, errors.New("a setup run needs what to set up")
	}
	if r.Purpose == proto.PurposeGit && (r.Git == nil || r.RepoPath == "" || r.NodeID == "") {
		return store.Run{}, errors.New("a git run needs its operation, machine and repository")
	}
	if r.Agent == "" || (r.Prompt == "" && !noAgent) {
		return store.Run{}, errors.New("agent and prompt are required")
	}
	if r.PermissionMode == "" {
		r.PermissionMode = proto.ModeAuto
	}
	if r.NodeID == "" {
		id, err := h.pickNode(ctx, r.Agent)
		if err != nil {
			return store.Run{}, err
		}
		r.NodeID = id
	} else if n, err := h.log.Store.Node(ctx, r.NodeID); err != nil || n.Revoked {
		return store.Run{}, ErrUnknownNode
	}
	kind := event.RunKindGeneral
	if r.RepoPath != "" {
		kind = event.RunKindCode
	}
	if r.Kind == event.RunKindOps || r.Kind == proto.KindInPlace {
		kind = r.Kind
	}
	ev := event.New(event.RunQueued, event.ActorSupervisor, r.ThreadID, event.RunQueuedPayload{
		NodeID: r.NodeID, Agent: r.Agent, Kind: kind, RepoPath: r.RepoPath, Prompt: r.Prompt, PermissionMode: r.PermissionMode,
		Purpose: r.Purpose, Branch: r.Branch, BaseBranch: r.BaseBranch, Title: r.Title, Force: r.Force, RecoveryNote: r.RecoveryNote,
		Options: r.Options, Persona: r.Persona, Setup: r.Setup,
		BaseFrom: r.BaseFrom, Git: r.Git, ProjectID: r.ProjectID, ResumeSession: r.ResumeSession, ContinuePrompt: r.ContinuePrompt,
	})
	ev.RunID = r.RunID
	if ev.RunID == "" {
		ev.RunID = ids.New()
	}
	ev.TaskID = r.TaskID
	if _, err := h.log.Append(ctx, ev); err != nil {
		return store.Run{}, err
	}
	h.dispatchQueued(ctx, r.NodeID)
	return h.log.Store.Run(ctx, ev.RunID)
}

// pickNode chooses the first online machine that has the agent installed and
// not known to be logged out.
func (h *Hub) pickNode(ctx context.Context, agent string) (string, error) {
	nodes, err := h.log.Store.Nodes(ctx)
	if err != nil {
		return "", err
	}
	for _, n := range nodes {
		if !h.Online(n.ID) {
			continue
		}
		for _, a := range parseAgents(n.Agents) {
			if a.Name == agent && a.Installed && (a.LoggedIn == nil || *a.LoggedIn) {
				return n.ID, nil
			}
		}
	}
	return "", fmt.Errorf("%w: 没有在线的机器装了 %s 并且已登录", ErrNoNode, agent)
}

// dispatchQueued sends every queued run of a node to it, if it is connected.
func (h *Hub) dispatchQueued(ctx context.Context, nodeID string) {
	c := h.conn(nodeID)
	if c == nil {
		return
	}
	runs, err := h.log.Store.Runs(ctx, store.RunFilter{NodeID: nodeID, Statuses: []string{event.RunStatusQueued}})
	if err != nil {
		slog.Error("nodehub: list queued runs", "err", err)
		return
	}
	for _, r := range runs {
		c.dispatch(r)
	}
}

// Cancel stops a run: a queued run ends at once; a running one is asked to stop.
func (h *Hub) Cancel(ctx context.Context, runID, reason string) error {
	r, err := h.log.Store.Run(ctx, runID)
	if err != nil {
		return err
	}
	switch r.Status {
	case event.RunStatusQueued:
		ev := event.New(event.RunEnded, event.ActorUser, r.ThreadID, event.RunEndedPayload{Status: event.RunStatusCancelled, Summary: "派发前已取消"})
		ev.RunID, ev.TaskID = runID, r.TaskID
		_, err := h.log.Append(ctx, ev)
		return err
	case event.RunStatusRunning:
		c := h.conn(r.NodeID)
		if c == nil {
			// The node ends it when it reconnects: it is no longer in resume_runs.
			ev := event.New(event.RunEnded, event.ActorUser, r.ThreadID, event.RunEndedPayload{Status: event.RunStatusCancelled, Summary: "机器离线时已取消"})
			ev.RunID, ev.TaskID = runID, r.TaskID
			_, err := h.log.Append(ctx, ev)
			return err
		}
		c.send(proto.New(proto.TRunCancel, proto.RunCancel{RunID: runID, Reason: reason}))
		return nil
	}
	return fmt.Errorf("run is already %s", r.Status)
}

// Message forwards a follow-up instruction to a running run. It is stored
// first, so a machine that is away gets it when it reconnects, and a run
// that is retried after an interruption gets what its agent never read.
func (h *Hub) Message(ctx context.Context, runID, text string) error {
	return h.MessageAs(ctx, runID, text, event.ActorUser)
}

// MessageAs is Message on behalf of the user or the supervisor.
func (h *Hub) MessageAs(ctx context.Context, runID, text, by string) error {
	r, err := h.log.Store.Run(ctx, runID)
	if err != nil {
		return err
	}
	if r.Status != event.RunStatusRunning {
		return fmt.Errorf("run is %s", r.Status)
	}
	msgID := ids.New()
	ev := event.New(event.RunMessage, by, r.ThreadID, event.RunMessagePayload{MessageID: msgID, Text: text})
	ev.RunID, ev.TaskID = runID, r.TaskID
	if _, err := h.log.Append(ctx, ev); err != nil {
		return err
	}
	if r.Paused {
		// A message to a paused run resumes it with that message.
		h.appendResumed(ctx, r, text, by)
	}
	if c := h.conn(r.NodeID); c != nil {
		c.send(proto.New(proto.TRunMessage, proto.RunMessage{RunID: runID, Text: text, MessageID: msgID}))
	}
	return nil
}

// Pause stops a run's current turn and holds it until it is resumed, sent a
// message, or cancelled. The inspector pauses runs that loop.
func (h *Hub) Pause(ctx context.Context, runID, reason, by string) error {
	r, err := h.log.Store.Run(ctx, runID)
	if err != nil {
		return err
	}
	if r.Status != event.RunStatusRunning {
		return fmt.Errorf("run is %s", r.Status)
	}
	if r.Paused {
		return nil
	}
	ev := event.New(event.RunPaused, by, r.ThreadID, event.RunPausePayload{Reason: reason})
	ev.RunID, ev.TaskID = runID, r.TaskID
	if _, err := h.log.Append(ctx, ev); err != nil {
		return err
	}
	if c := h.conn(r.NodeID); c != nil {
		c.send(proto.New(proto.TRunPause, proto.RunPause{RunID: runID, Reason: reason}))
	}
	return nil
}

// Resume continues a paused run, optionally with an instruction.
func (h *Hub) Resume(ctx context.Context, runID, text, by string) error {
	r, err := h.log.Store.Run(ctx, runID)
	if err != nil {
		return err
	}
	if r.Status != event.RunStatusRunning || !r.Paused {
		return errors.New("这个 Run 没有暂停")
	}
	if text != "" {
		return h.MessageAs(ctx, runID, text, by)
	}
	if err := h.appendResumed(ctx, r, "", by); err != nil {
		return err
	}
	if c := h.conn(r.NodeID); c != nil {
		c.send(proto.New(proto.TRunResume, proto.RunResume{RunID: runID}))
	}
	return nil
}

func (h *Hub) appendResumed(ctx context.Context, r store.Run, text, by string) error {
	ev := event.New(event.RunResumed, by, r.ThreadID, event.RunPausePayload{Text: text})
	ev.RunID, ev.TaskID = r.ID, r.TaskID
	_, err := h.log.Append(ctx, ev)
	return err
}

// Undelivered returns the follow-up messages of a run its agent never read.
func (h *Hub) Undelivered(ctx context.Context, runID string) []store.RunMessage {
	ms, _ := h.log.Store.RunMessages(ctx, runID)
	var out []store.RunMessage
	for _, m := range ms {
		if !m.Delivered {
			out = append(out, m)
		}
	}
	return out
}

// RequestUpdate asks a machine to install a release and restart.
func (h *Hub) RequestUpdate(ctx context.Context, nodeID, version, repo string) error {
	c := h.conn(nodeID)
	if c == nil {
		return errors.New("机器不在线")
	}
	if _, err := h.log.Append(ctx, event.New(event.NodeUpdateRequested, event.ActorUser, "", event.NodeUpdatePayload{NodeID: nodeID, Version: version})); err != nil {
		return err
	}
	c.send(proto.New(proto.TNodeUpdate, proto.NodeUpdate{Version: version, Repo: repo}))
	return nil
}

// Decide answers an approval for the user and forwards the answer to its machine.
func (h *Hub) Decide(ctx context.Context, approvalID, optionID, note string) error {
	return h.DecideAs(ctx, approvalID, optionID, note, event.ActorUser)
}

// DecideAs answers an approval on behalf of by: the user or the approver agent.
func (h *Hub) DecideAs(ctx context.Context, approvalID, optionID, note, by string) error {
	note = redact.Mask(note)
	a, err := h.log.Store.Approval(ctx, approvalID)
	if err != nil {
		return err
	}
	if a.Status != "pending" {
		return store.ErrApprovalClosed
	}
	if optionID != "" {
		valid := false
		for _, o := range a.Options {
			valid = valid || o.OptionID == optionID
		}
		if !valid {
			return fmt.Errorf("unknown option %q", optionID)
		}
	}
	ev := event.New(event.ApprovalDecided, by, a.ThreadID, event.ApprovalDecidedPayload{
		ApprovalID: approvalID, OptionID: optionID, DecidedBy: by, Note: note,
	})
	ev.RunID = a.RunID
	ev.TaskID = a.TaskID
	if _, err := h.log.Write(ctx, func(w *store.Writer) error {
		limit := h.MaxAIAllowsPerRun
		if limit == 0 {
			limit = 150
		}
		if err := w.CheckApprovalDecision(a.ID, optionID, by, limit); err != nil {
			return err
		}
		_, err := w.Append(ev)
		return err
	}); err != nil {
		return err
	}
	r, err := h.log.Store.Run(ctx, a.RunID)
	if err != nil {
		return err
	}
	if c := h.conn(r.NodeID); c != nil {
		c.send(proto.New(proto.TApprovalDecide, proto.ApprovalDecide{ApprovalID: approvalID, RunID: a.RunID, OptionID: optionID, Note: note}))
	}
	return nil
}

// Escalate hands an approval the approver agent was unsure of to the user.
func (h *Hub) Escalate(ctx context.Context, approvalID, note string) error {
	note = redact.Mask(note)
	a, err := h.log.Store.Approval(ctx, approvalID)
	if err != nil {
		return err
	}
	if a.Status != "pending" {
		return nil
	}
	ev := event.New(event.ApprovalEscalated, event.ActorApprover, a.ThreadID, event.ApprovalEscalatedPayload{ApprovalID: approvalID, Note: note})
	ev.RunID = a.RunID
	ev.TaskID = a.TaskID
	_, err = h.log.Append(ctx, ev)
	return err
}

// Busy counts the queued and running runs of a node.
func (h *Hub) Busy(ctx context.Context, nodeID string) int {
	runs, err := h.log.Store.Runs(ctx, store.RunFilter{NodeID: nodeID, Statuses: []string{event.RunStatusQueued, event.RunStatusRunning}})
	if err != nil {
		return 0
	}
	waiting := map[string]bool{}
	if pending, err := h.log.Store.Approvals(ctx, "pending", ""); err == nil {
		for _, a := range pending {
			if a.Review == "user" {
				waiting[a.RunID] = true
			}
		}
	}
	n := 0
	for _, r := range runs {
		switch {
		case r.Purpose == event.PurposeConsult, r.Purpose == event.PurposeGit:
			// Consultants and git operations run no agent that works: they
			// take no slot (decision 0022, end-to-end review).
		case r.Status == event.RunStatusRunning && waiting[r.ID]:
			// A run waiting for the user gives its slot up until answered:
			// two unanswered cards must not stop a machine.
		default:
			n++
		}
	}
	return n
}

// RemoveWorkspace asks a node to delete a closed task's worktree.
func (h *Hub) RemoveWorkspace(nodeID, taskID, repoPath string) error {
	c := h.conn(nodeID)
	if c == nil {
		return errors.New("机器不在线")
	}
	c.send(proto.New(proto.TWorkspaceRemove, proto.WorkspaceRemove{TaskID: taskID, RepoPath: repoPath}))
	return nil
}

// LogPath and DiffPath locate a run's files.
func (h *Hub) LogPath(runID string) string  { return filepath.Join(h.runsDir, safeName(runID)+".log") }
func (h *Hub) DiffPath(runID string) string { return filepath.Join(h.runsDir, safeName(runID)+".diff") }

func safeName(id string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' {
			return r
		}
		return '_'
	}, id)
}

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// stuck pauses a run whose agent keeps failing the same tool call, as the
// node saw it, and tells the user: sooner than the inspector's round and
// without a model (end-to-end review 4.16). The finding and the message go
// to the main thread, like the inspector's.
func (h *Hub) stuck(ctx context.Context, ref runRef, runID, what string) {
	r, err := h.log.Store.Run(ctx, runID)
	if err != nil || r.Status != event.RunStatusRunning || r.Paused {
		return
	}
	reason := "agent 卡住了：" + what
	done := h.Pause(ctx, runID, reason, event.ActorInspector) == nil
	main, err := h.log.Store.MainThreadID(ctx)
	if err != nil {
		return
	}
	f := event.FindingPayload{FindingID: ids.New(), Rule: "repeat", Severity: "warning", Action: event.ActionPause, Reason: reason, Done: done}
	_, err = h.log.Write(ctx, func(w *store.Writer) error {
		ev := event.New(event.InspectorFinding, event.ActorInspector, main, f)
		ev.RunID, ev.TaskID = runID, r.TaskID
		if _, err := w.Append(ev); err != nil {
			return err
		}
		text := fmt.Sprintf("「%s」的 agent %s，已经暂停。可以发一句话告诉它换个办法（会接着做），或者换一个 agent 重试。", firstNonEmpty(r.Title, "这个任务"), what)
		msg := event.New(event.MessageAssistant, event.ActorInspector, main,
			event.MessagePayload{MessageID: ids.New(), Content: text, Kind: event.KindInspector, TopicID: h.log.Store.TaskTopic(ctx, main, r.TaskID)})
		msg.RunID, msg.TaskID = runID, r.TaskID
		_, err := w.Append(msg)
		return err
	})
	if err != nil {
		slog.Error("nodehub: record a stuck run", "run", runID, "err", err)
	}
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
