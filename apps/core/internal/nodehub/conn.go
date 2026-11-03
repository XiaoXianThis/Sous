package nodehub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

const (
	helloTimeout = 10 * time.Second
	pingEvery    = 20 * time.Second
	writeTimeout = 10 * time.Second
	maxFrame     = 4 << 20
)

type nodeConn struct {
	hub    *Hub
	nodeID string
	ws     *websocket.Conn
	out    chan proto.Frame
	ctx    context.Context
	cancel context.CancelFunc

	mu         sync.Mutex
	dispatched map[string]bool
	threads    map[string]string // run id -> thread id, for relaying output
}

// ServeNode handles GET /node: a daemon's connection, authenticated by its
// device token in the Authorization header.
func (h *Hub) ServeNode(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		http.Error(w, "missing device token", http.StatusUnauthorized)
		return
	}
	node, err := h.log.Store.NodeByToken(r.Context(), hashToken(token))
	if err != nil {
		http.Error(w, "unknown or revoked device token", http.StatusUnauthorized)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(maxFrame)
	ctx, cancel := context.WithCancel(context.Background())
	c := &nodeConn{hub: h, nodeID: node.ID, ws: ws, out: make(chan proto.Frame, sendBuffer), ctx: ctx, cancel: cancel,
		dispatched: map[string]bool{}, threads: map[string]string{}}
	defer cancel()
	defer ws.CloseNow()

	helloCtx, done := context.WithTimeout(ctx, helloTimeout)
	var hello proto.Hello
	f, err := c.read(helloCtx)
	done()
	if err != nil || f.T != proto.THello || f.Decode(&hello) != nil {
		ws.Close(websocket.StatusPolicyViolation, "expected hello")
		return
	}

	h.mu.Lock()
	if old := h.conns[node.ID]; old != nil {
		old.close("replaced by a new connection")
	}
	h.conns[node.ID] = c
	h.mu.Unlock()
	slog.Info("nodehub: machine connected", "node", node.ID, "name", node.Name, "version", hello.Version)

	go c.writer()
	h.log.Append(ctx, event.New(event.NodeOnline, event.ActorSystem, "", event.NodePayload{
		NodeID: node.ID, OS: hello.OS, Arch: hello.Arch, Hostname: hello.Hostname, Version: hello.Version,
	}))
	c.welcome(ctx)
	h.dispatchQueued(ctx, node.ID)

	for {
		f, err := c.read(ctx)
		if err != nil {
			break
		}
		if err := c.handle(ctx, f); err != nil {
			slog.Warn("nodehub: frame failed", "node", node.ID, "type", f.T, "err", err)
			continue // no ack: the node resends it
		}
		if f.Seq > 0 {
			c.send(proto.New(proto.TAck, proto.Ack{Seq: f.Seq}))
		}
	}

	h.mu.Lock()
	current := h.conns[node.ID] == c
	if current {
		delete(h.conns, node.ID)
	}
	h.mu.Unlock()
	if current {
		h.log.Append(context.Background(), event.New(event.NodeOffline, event.ActorSystem, "", event.NodePayload{NodeID: node.ID}))
		slog.Info("nodehub: machine disconnected", "node", node.ID)
	}
}

func (c *nodeConn) read(ctx context.Context) (proto.Frame, error) {
	var f proto.Frame
	typ, raw, err := c.ws.Read(ctx)
	if err != nil {
		return f, err
	}
	if typ != websocket.MessageText {
		return f, errors.New("binary frame")
	}
	return f, json.Unmarshal(raw, &f)
}

func (c *nodeConn) writer() {
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case f := <-c.out:
			raw, _ := json.Marshal(f)
			wctx, cancel := context.WithTimeout(c.ctx, writeTimeout)
			err := c.ws.Write(wctx, websocket.MessageText, raw)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(c.ctx, writeTimeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		}
	}
}

func (c *nodeConn) send(f proto.Frame) {
	select {
	case c.out <- f:
	case <-c.ctx.Done():
	}
}

func (c *nodeConn) close(reason string) {
	c.ws.Close(websocket.StatusNormalClosure, reason)
	c.cancel()
}

// welcome tells the node which runs the Hub thinks it is running, and
// repeats decisions on their approvals in case the node missed them.
func (c *nodeConn) welcome(ctx context.Context) {
	st := c.hub.log.Store
	running, err := st.Runs(ctx, store.RunFilter{NodeID: c.nodeID, Statuses: []string{event.RunStatusRunning}})
	if err != nil {
		slog.Error("nodehub: list running runs", "err", err)
	}
	resume := []string{}
	for _, r := range running {
		resume = append(resume, r.ID)
		c.mu.Lock()
		c.dispatched[r.ID] = true
		c.threads[r.ID] = r.ThreadID
		c.mu.Unlock()
	}
	c.send(proto.New(proto.TWelcome, proto.Welcome{NodeID: c.nodeID, ResumeRuns: resume}))
	for _, r := range running {
		decided, _ := st.Approvals(ctx, "decided", r.ID)
		for _, a := range decided {
			c.send(proto.New(proto.TApprovalDecide, proto.ApprovalDecide{ApprovalID: a.ID, RunID: r.ID, OptionID: a.OptionID}))
		}
	}
}

func (c *nodeConn) dispatch(r store.Run) {
	c.mu.Lock()
	if c.dispatched[r.ID] {
		c.mu.Unlock()
		return
	}
	c.dispatched[r.ID] = true
	c.threads[r.ID] = r.ThreadID
	c.mu.Unlock()
	c.send(proto.New(proto.TRunStart, proto.RunStart{
		RunID: r.ID, Agent: r.Agent, Kind: r.Kind, RepoPath: r.RepoPath, Prompt: r.Prompt, PermissionMode: proto.ModeDefault,
	}))
}

// threadOf returns the thread a run belongs to.
func (c *nodeConn) threadOf(ctx context.Context, runID string) (string, error) {
	c.mu.Lock()
	t, ok := c.threads[runID]
	c.mu.Unlock()
	if ok {
		return t, nil
	}
	r, err := c.hub.log.Store.Run(ctx, runID)
	if err != nil {
		return "", err
	}
	if r.NodeID != c.nodeID {
		return "", fmt.Errorf("run %s belongs to another machine", runID)
	}
	c.mu.Lock()
	c.threads[runID] = r.ThreadID
	c.mu.Unlock()
	return r.ThreadID, nil
}

// store appends an event whose id is the frame id, so a resent frame is
// stored once.
func (c *nodeConn) store(ctx context.Context, f proto.Frame, ev event.Event) error {
	ev.ID = f.ID
	_, err := c.hub.log.Append(ctx, ev)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: events.id") {
		return nil
	}
	return err
}

func (c *nodeConn) handle(ctx context.Context, f proto.Frame) error {
	switch f.T {
	case proto.TInventory:
		var inv proto.Inventory
		if err := f.Decode(&inv); err != nil {
			return err
		}
		agents, _ := json.Marshal(inv.Agents)
		return c.store(ctx, f, event.New(event.NodeInventory, event.ActorSystem, "", event.NodePayload{NodeID: c.nodeID, Agents: agents}))

	case proto.TRunStarted:
		var p proto.RunStarted
		if err := f.Decode(&p); err != nil {
			return err
		}
		thread, err := c.threadOf(ctx, p.RunID)
		if err != nil {
			return err
		}
		ev := event.New(event.RunStarted, event.ActorSystem, thread, event.RunStartedPayload{Workdir: p.Workdir, Branch: p.Branch})
		ev.RunID = p.RunID
		return c.store(ctx, f, ev)

	case proto.TRunUpdate, proto.TRunOutput:
		var p proto.RunUpdate
		if err := f.Decode(&p); err != nil {
			return err
		}
		thread, err := c.threadOf(ctx, p.RunID)
		if err != nil {
			return err
		}
		plan, _ := json.Marshal(p.Plan)
		payload := event.RunUpdatePayload{Kind: p.Kind, Text: p.Text, ToolCallID: p.ToolCallID, Title: p.Title, Status: p.Status, ToolKind: p.ToolKind}
		if len(p.Plan) > 0 {
			payload.Plan = plan
		}
		if f.T == proto.TRunOutput {
			c.appendLog(p)
			ev := event.New(event.RunOutput, event.ActorSystem, thread, payload)
			ev.RunID = p.RunID
			c.hub.log.Emit(ev)
			return nil
		}
		c.appendLog(p)
		ev := event.New(event.RunUpdate, event.ActorSystem, thread, payload)
		ev.RunID = p.RunID
		return c.store(ctx, f, ev)

	case proto.TRunDiff:
		var p proto.RunDiff
		if err := f.Decode(&p); err != nil {
			return err
		}
		if _, err := c.threadOf(ctx, p.RunID); err != nil {
			return err
		}
		return c.writeDiff(p)

	case proto.TRunEnded:
		var p proto.RunEnded
		if err := f.Decode(&p); err != nil {
			return err
		}
		thread, err := c.threadOf(ctx, p.RunID)
		if err != nil {
			return err
		}
		if r, err := c.hub.log.Store.Run(ctx, p.RunID); err == nil && isFinal(r.Status) {
			return nil // already ended, for example cancelled while the node was away
		}
		ev := event.New(event.RunEnded, event.ActorSystem, thread, event.RunEndedPayload{
			Status: p.Status, StopReason: p.StopReason, Summary: p.Summary, DiffStat: p.DiffStat, Error: p.Error,
		})
		ev.RunID = p.RunID
		return c.store(ctx, f, ev)

	case proto.TApprovalRequest:
		var p proto.ApprovalRequest
		if err := f.Decode(&p); err != nil {
			return err
		}
		thread, err := c.threadOf(ctx, p.RunID)
		if err != nil {
			return err
		}
		opts := make([]event.ApprovalOption, len(p.Options))
		for i, o := range p.Options {
			opts[i] = event.ApprovalOption{OptionID: o.OptionID, Name: o.Name, Kind: o.Kind}
		}
		ev := event.New(event.ApprovalRequested, event.ActorSystem, thread, event.ApprovalRequestedPayload{
			ApprovalID: p.ApprovalID, Title: p.Title, ToolKind: p.ToolKind, Detail: p.Detail, Options: opts,
		})
		ev.RunID = p.RunID
		return c.store(ctx, f, ev)
	}
	return nil // unknown frames from newer daemons are ignored
}

func isFinal(status string) bool {
	switch status {
	case event.RunStatusSucceeded, event.RunStatusFailed, event.RunStatusCancelled, event.RunStatusInterrupted:
		return true
	}
	return false
}

// appendLog keeps a readable transcript of the run in runs/<id>.log.
func (c *nodeConn) appendLog(p proto.RunUpdate) {
	var line string
	switch p.Kind {
	case proto.UpdateText:
		line = p.Text
	case proto.UpdateThought:
		return
	case proto.UpdateTool:
		line = fmt.Sprintf("\n[%s] %s %s\n", p.Status, p.ToolKind, p.Title)
	case proto.UpdatePlan:
		var b strings.Builder
		b.WriteString("\n[plan]\n")
		for _, e := range p.Plan {
			fmt.Fprintf(&b, "  - [%s] %s\n", e.Status, e.Content)
		}
		line = b.String()
	case proto.UpdateStatus:
		line = "\n[sous] " + p.Text + "\n"
	}
	f, err := os.OpenFile(c.hub.LogPath(p.RunID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Error("nodehub: open run log", "err", err)
		return
	}
	defer f.Close()
	f.WriteString(line)
}

func (c *nodeConn) writeDiff(p proto.RunDiff) error {
	flag := os.O_CREATE | os.O_WRONLY
	if p.Offset == 0 {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(c.hub.DiffPath(p.RunID), flag, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteAt([]byte(p.Chunk), p.Offset)
	return err
}

func parseAgents(raw json.RawMessage) []proto.AgentInfo {
	var out []proto.AgentInfo
	json.Unmarshal(raw, &out)
	return out
}
