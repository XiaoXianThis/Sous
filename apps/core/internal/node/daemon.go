package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/XiaoXianThis/Sous/apps/core/internal/agents"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/version"
)

const (
	inventoryEvery = 10 * time.Minute
	maxBackoff     = 30 * time.Second
	welcomeTimeout = 15 * time.Second
)

// ErrRevoked means the Hub no longer accepts this machine's token.
var ErrRevoked = errors.New("the Hub rejected this machine's token; pair it again")

type Daemon struct {
	cfg   Config
	root  string
	specs []agents.Spec
	ob    *outbox
	sem   chan struct{}

	mu   sync.Mutex
	runs map[string]*run

	// connected is signalled when a session is established (tests use it).
	connected chan struct{}
}

// NewDaemon builds a daemon. Agent overrides from the config replace the
// built-in launch commands.
func NewDaemon(root string, cfg Config, specs []agents.Spec) *Daemon {
	for i, s := range specs {
		if cmd, ok := cfg.Agents[s.Name]; ok && len(cmd) > 0 {
			specs[i].Launch = cmd
		}
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 2
	}
	return &Daemon{cfg: cfg, root: root, specs: specs, ob: newOutbox(), sem: make(chan struct{}, cfg.Concurrency),
		runs: map[string]*run{}, connected: make(chan struct{}, 1)}
}

// Run keeps a connection to the Hub until ctx ends, reconnecting with backoff.
func (d *Daemon) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		start := time.Now()
		err := d.session(ctx)
		if ctx.Err() != nil {
			d.stopAll()
			return nil
		}
		if errors.Is(err, ErrRevoked) {
			d.stopAll()
			return err
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		slog.Warn("daemon: disconnected from Hub", "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			d.stopAll()
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (d *Daemon) session(ctx context.Context) error {
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+d.cfg.Token)
	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	ws, resp, err := websocket.Dial(dialCtx, wsURL(d.cfg.HubURL), &websocket.DialOptions{HTTPHeader: hdr})
	cancel()
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return ErrRevoked
		}
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(maxFrameBytes)

	host, _ := os.Hostname()
	if err := writeFrame(ctx, ws, proto.New(proto.THello, proto.Hello{
		Version: version.Version, OS: runtime.GOOS, Arch: runtime.GOARCH, Hostname: host,
	})); err != nil {
		return err
	}
	wctx, done := context.WithTimeout(ctx, welcomeTimeout)
	f, err := readFrame(wctx, ws)
	done()
	if err != nil {
		return fmt.Errorf("waiting for welcome: %w", err)
	}
	var welcome proto.Welcome
	if f.T != proto.TWelcome || f.Decode(&welcome) != nil {
		return fmt.Errorf("expected welcome, got %q", f.T)
	}

	sctx, stop := context.WithCancel(ctx)
	defer stop()
	epoch := d.ob.connect()
	defer d.ob.disconnect()
	go d.writer(sctx, ws, epoch, stop)
	d.reconcile(welcome.ResumeRuns)
	go d.inventoryLoop(sctx)
	slog.Info("daemon: connected to Hub", "hub", d.cfg.HubURL, "node", welcome.NodeID, "pending", d.ob.pendingCount())
	select {
	case d.connected <- struct{}{}:
	default:
	}

	for {
		f, err := readFrame(sctx, ws)
		if err != nil {
			return err
		}
		d.handle(sctx, f)
	}
}

const maxFrameBytes = 4 << 20

func (d *Daemon) writer(ctx context.Context, ws *websocket.Conn, epoch int, fail func()) {
	for {
		f, ok := d.ob.next(ctx, epoch)
		if !ok {
			return
		}
		if err := writeFrame(ctx, ws, f); err != nil {
			fail()
			return
		}
	}
}

func writeFrame(ctx context.Context, ws *websocket.Conn, f proto.Frame) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return ws.Write(ctx, websocket.MessageText, raw)
}

func readFrame(ctx context.Context, ws *websocket.Conn) (proto.Frame, error) {
	var f proto.Frame
	_, raw, err := ws.Read(ctx)
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal(raw, &f)
}

func (d *Daemon) inventoryLoop(ctx context.Context) {
	t := time.NewTicker(inventoryEvery)
	defer t.Stop()
	for {
		inv := proto.Inventory{Agents: agents.Detect(ctx, d.specs), Concurrency: d.cfg.Concurrency}
		if ctx.Err() != nil {
			return
		}
		d.ob.reliable(proto.New(proto.TInventory, inv))
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (d *Daemon) handle(ctx context.Context, f proto.Frame) {
	switch f.T {
	case proto.TAck:
		var a proto.Ack
		if f.Decode(&a) == nil {
			d.ob.ack(a.Seq)
		}
	case proto.TRunStart:
		var s proto.RunStart
		if f.Decode(&s) == nil {
			d.start(s)
		}
	case proto.TRunCancel:
		var c proto.RunCancel
		if f.Decode(&c) == nil {
			if r := d.run(c.RunID); r != nil {
				r.stop("用户取消")
			}
		}
	case proto.TRunMessage:
		var m proto.RunMessage
		if f.Decode(&m) == nil {
			if r := d.run(m.RunID); r != nil {
				r.followUp(m.Text)
			}
		}
	case proto.TApprovalDecide:
		var a proto.ApprovalDecide
		if f.Decode(&a) == nil {
			if r := d.run(a.RunID); r != nil {
				r.decide(a.ApprovalID, a.OptionID)
			}
		}
	}
}

func (d *Daemon) run(id string) *run {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.runs[id]
}

// reconcile compares the Hub's view with the runs on this machine. A run the
// Hub thinks is running but this daemon does not have was lost in a restart.
// A run this daemon has that the Hub no longer lists was cancelled while the
// machine was away, unless the Hub simply has not stored its start yet.
func (d *Daemon) reconcile(resume []string) {
	want := map[string]bool{}
	for _, id := range resume {
		want[id] = true
		if d.run(id) == nil {
			d.ob.reliable(proto.New(proto.TRunEnded, proto.RunEnded{
				RunID: id, Status: "interrupted", Error: "这台机器上的 sous 重启过，执行被中断了",
			}))
		}
	}
	d.mu.Lock()
	var stale []*run
	for id, r := range d.runs {
		if !want[id] && !d.ob.pendingFor(proto.TRunStarted, id) && r.started() {
			stale = append(stale, r)
		}
	}
	d.mu.Unlock()
	for _, r := range stale {
		r.stop("Hub 上已经取消")
	}
}

func (d *Daemon) start(s proto.RunStart) {
	d.mu.Lock()
	if _, exists := d.runs[s.RunID]; exists {
		d.mu.Unlock()
		return // the Hub sent it again after a reconnect
	}
	r := newRun(d, s)
	d.runs[s.RunID] = r
	d.mu.Unlock()
	go func() {
		r.execute()
		d.mu.Lock()
		delete(d.runs, s.RunID)
		d.mu.Unlock()
	}()
}

func (d *Daemon) stopAll() {
	d.mu.Lock()
	runs := make([]*run, 0, len(d.runs))
	for _, r := range d.runs {
		runs = append(runs, r)
	}
	d.mu.Unlock()
	for _, r := range runs {
		r.stop("sous daemon 已停止")
	}
}
