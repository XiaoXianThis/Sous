package node

import (
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// A project's consultant (decision 0022) is a read-only run that stays open
// between questions: after each answer it reports the turn and waits. When
// no question comes for a while it ends, keeping the agent's session id so
// the Hub can load the session for the next question.

// ConsultIdle, when set, overrides how long a consultant waits (tests).
var ConsultIdle time.Duration

// consultIdle is how long a consultant waits for the next question before
// its agent is stopped (node.json consult_idle_minutes).
func (d *Daemon) consultIdle() time.Duration {
	if ConsultIdle > 0 {
		return ConsultIdle
	}
	if m := d.conf().ConsultIdleMinutes; m > 0 {
		return time.Duration(m) * time.Minute
	}
	return 30 * time.Minute
}

// reportTurn sends the answer of the turn that just ended.
func (r *run) reportTurn() {
	r.mu.Lock()
	text := strings.TrimSpace(r.turnText.String())
	sid := r.sessionID
	r.mu.Unlock()
	if text == "" {
		text = "（顾问这一轮没有给出文字回答）"
	}
	r.flushStreams()
	r.d.ob.reliable(proto.New(proto.TRunTurn, proto.RunTurn{RunID: r.spec.RunID, Text: mask(text), SessionID: sid}))
}

// waitQuestion waits for the next question; false when the run is stopped
// or nothing came within the idle time.
func (r *run) waitQuestion() (string, []string, bool) {
	if more, ids := r.takeFollowUps(); len(more) > 0 {
		return strings.Join(more, "\n\n"), ids, true
	}
	r.mu.Lock()
	r.idle = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.idle = false
		r.mu.Unlock()
	}()
	r.status("顾问在等下一个问题")
	t := time.NewTimer(r.d.consultIdle())
	defer t.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return "", nil, false
		case <-t.C:
			if more, ids := r.takeFollowUps(); len(more) > 0 {
				return strings.Join(more, "\n\n"), ids, true
			}
			return "", nil, false
		case <-r.wake:
			if r.stopRequested() {
				return "", nil, false
			}
			if more, ids := r.takeFollowUps(); len(more) > 0 {
				return strings.Join(more, "\n\n"), ids, true
			}
		}
	}
}

func (r *run) stopRequested() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopReason != ""
}

// endConsult reports the end of a consultant run with its session id, so
// the next question can load the session.
func (r *run) endConsult(status, stop string, err error) {
	if r.isShutdown() {
		return
	}
	r.mu.Lock()
	sid, canLoad := r.sessionID, r.canLoad
	r.mu.Unlock()
	e := proto.RunEnded{RunID: r.spec.RunID, Status: status, StopReason: stop}
	if canLoad {
		e.SessionID = sid
	}
	if err != nil {
		e.Error = err.Error()
	}
	if stop == "idle" {
		e.Summary = "顾问空闲太久，已经停下；下次提问会接上这次的会话"
		if !canLoad {
			e.Summary = "顾问空闲太久，已经停下；这个 agent 不支持恢复会话，下次提问会带上之前的结论新开一个"
		}
	}
	r.d.ob.reliable(proto.New(proto.TRunEnded, e))
	r.d.forget(r.spec.RunID)
	slog.Info("daemon: consultant ended", "run", r.spec.RunID, "status", status, "stop", stop)
}

var errNoTask = errors.New("这个 git 操作需要任务的分支")
