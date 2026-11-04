package node

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/update"
	"github.com/XiaoXianThis/Sous/apps/core/internal/version"
)

// savedRun is what a daemon keeps on disk about a running run, so the next
// daemon can resume it after a restart (design §9): its spec, the agent's
// session, and which follow-ups the agent already read.
type savedRun struct {
	Spec        proto.RunStart `json:"spec"`
	SessionID   string         `json:"session_id,omitempty"`
	CanLoad     bool           `json:"can_load,omitempty"`
	Workdir     string         `json:"workdir,omitempty"`
	Delivered   []string       `json:"delivered,omitempty"`
	Paused      bool           `json:"paused,omitempty"`
	PauseReason string         `json:"pause_reason,omitempty"`
}

func (d *Daemon) runsDir() string { return filepath.Join(d.root, "runs") }

func (d *Daemon) savedPath(runID string) string {
	return filepath.Join(d.runsDir(), safeFile(runID)+".json")
}

func safeFile(id string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' {
			return r
		}
		return '_'
	}, id)
}

// save writes the run's state; errors are logged, not fatal.
func (r *run) save() {
	r.mu.Lock()
	sv := savedRun{Spec: r.spec, SessionID: r.sessionID, CanLoad: r.canLoad, Workdir: r.workdir,
		Delivered: append([]string(nil), r.delivered...), Paused: r.paused, PauseReason: r.pauseReason}
	r.mu.Unlock()
	if err := os.MkdirAll(r.d.runsDir(), 0o700); err != nil {
		slog.Warn("daemon: save run", "err", err)
		return
	}
	raw, _ := json.MarshalIndent(sv, "", "  ")
	path := r.d.savedPath(r.spec.RunID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err == nil {
		err = os.Rename(tmp, path)
		if err != nil {
			slog.Warn("daemon: save run", "err", err)
		}
	}
}

func (d *Daemon) forget(runID string) { os.Remove(d.savedPath(runID)) }

// savedRuns reads every saved run.
func (d *Daemon) savedRuns() map[string]savedRun {
	out := map[string]savedRun{}
	entries, _ := os.ReadDir(d.runsDir())
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(d.runsDir(), e.Name()))
		if err != nil {
			continue
		}
		var sv savedRun
		if json.Unmarshal(raw, &sv) == nil && sv.Spec.RunID != "" {
			out[sv.Spec.RunID] = sv
		}
	}
	return out
}

func (d *Daemon) setOnline(on bool) {
	d.mu.Lock()
	d.online = on
	d.mu.Unlock()
}

// RunInfo describes a run for `sous status` and the IPC.
type RunInfo struct {
	RunID   string `json:"run_id"`
	TaskID  string `json:"task_id,omitempty"`
	Agent   string `json:"agent"`
	Purpose string `json:"purpose,omitempty"`
	Workdir string `json:"workdir,omitempty"`
	Paused  bool   `json:"paused,omitempty"`
}

// Status is the daemon's state for the IPC.
type Status struct {
	Version      string    `json:"version"`
	NodeID       string    `json:"node_id"`
	Name         string    `json:"name"`
	HubURL       string    `json:"hub_url"`
	HubConnected bool      `json:"hub_connected"`
	Pending      int       `json:"pending_frames"`
	Runs         []RunInfo `json:"runs"`
	StartedAt    int64     `json:"started_at"`
}

func (d *Daemon) Status() Status {
	d.mu.Lock()
	st := Status{Version: version.Version, NodeID: d.conf().NodeID, Name: d.conf().Name, HubURL: d.conf().HubURL, HubConnected: d.online,
		StartedAt: d.startedAt.UnixMilli(), Runs: []RunInfo{}}
	runs := make([]*run, 0, len(d.runs))
	for _, r := range d.runs {
		runs = append(runs, r)
	}
	d.mu.Unlock()
	for _, r := range runs {
		r.mu.Lock()
		st.Runs = append(st.Runs, RunInfo{RunID: r.spec.RunID, TaskID: r.spec.TaskID, Agent: r.spec.Agent, Purpose: r.spec.Purpose, Workdir: r.workdir, Paused: r.paused})
		r.mu.Unlock()
	}
	st.Pending = d.ob.pendingCount()
	return st
}

// Probe checks that the daemon's locks answer, for the watchdog.
func (d *Daemon) Probe(context.Context) error {
	d.mu.Lock()
	d.mu.Unlock()
	d.ob.probe()
	return nil
}

// Config returns the daemon's config (the IPC proxies with its token).
func (d *Daemon) Config() Config { return d.conf() }

// selfUpdate installs a release and restarts, keeping running runs saved so
// the new daemon resumes them.
func (d *Daemon) selfUpdate(u proto.NodeUpdate) {
	d.mu.Lock()
	if d.updating {
		d.mu.Unlock()
		return
	}
	d.updating = true
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.updating = false
		d.mu.Unlock()
	}()
	tag, err := d.Update(context.Background(), u.Repo, u.Version)
	result := proto.NodeUpdateResult{Version: tag}
	if err != nil {
		result.Error = err.Error()
		slog.Error("daemon: update failed", "version", u.Version, "err", err)
	}
	d.ob.reliable(proto.New(proto.TNodeUpdateResult, result))
	if err != nil {
		return
	}
	// Give the result a moment to reach the Hub before restarting.
	deadline := time.Now().Add(5 * time.Second)
	for d.ob.pendingCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if d.OnUpdated != nil {
		d.OnUpdated(tag)
	}
}

// Update replaces the executable with release tag (latest when empty) from
// repo and returns the installed tag.
func (d *Daemon) Update(ctx context.Context, repo, tag string) (string, error) {
	exe := d.Exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return "", err
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
	}
	c := update.Client{Repo: repo, APIBase: d.UpdateAPI}
	if tag != "" && !update.Newer(tag, version.Version) && version.Version != "dev" {
		return "", fmt.Errorf("已经是 %s，不需要更新到 %s", version.Version, tag)
	}
	slog.Info("daemon: installing an update", "version", tag, "exe", exe)
	return c.Install(ctx, tag, exe)
}
