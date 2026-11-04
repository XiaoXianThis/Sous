package node

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
	"unicode/utf8"

	pty "github.com/aymanbagabas/go-pty"

	"github.com/XiaoXianThis/Sous/apps/core/internal/agents"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// A job runs one of an agent's fixed commands (install, upgrade, login,
// logout) on a pseudo-terminal, so interactive installers and login
// prompts behave as in a terminal. Its output streams to the Hub; the
// user's keystrokes come back as job.input.

const (
	jobTailBytes = 64 * 1024
	jobKeep      = 30 * time.Minute // a finished job can still be read
	maxJobs      = 3
)

var jobTimeouts = map[string]time.Duration{
	proto.JobInstall: 15 * time.Minute,
	proto.JobUpgrade: 15 * time.Minute,
	proto.JobLogin:   10 * time.Minute,
	proto.JobLogout:  2 * time.Minute,
	proto.JobShell:   24 * time.Hour, // a shell left open is closed after a day
}

type job struct {
	a       *admin
	id      string
	kind    string
	agent   string
	command string
	// Shells only: the directory, the session that owns it, when it started.
	root    string
	owner   string
	started time.Time

	pty    pty.Pty
	cmd    *pty.Cmd
	cancel context.CancelFunc

	mu      sync.Mutex
	tailBuf []byte
	offset  int64 // bytes sent so far
	carry   []byte
	running bool
	stopped bool
	exit    *int
	endedAt time.Time
}

func (a *admin) job(id string) *job {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.jobs[id]
}

func (a *admin) startJob(op string, args proto.AgentArgs) (any, error) {
	spec, err := a.spec(args.Agent)
	if err != nil {
		return nil, err
	}
	var (
		r    agents.Recipe
		ok   bool
		kind string
	)
	switch op {
	case proto.OpInstall:
		kind = proto.JobInstall
		if args.Upgrade {
			kind = proto.JobUpgrade
			r, ok = spec.UpgradeRecipe()
		} else {
			r, ok = spec.InstallRecipe()
		}
	case proto.OpLogin:
		kind = proto.JobLogin
		r, ok = spec.LoginRecipe()
	case proto.OpLogout:
		kind = proto.JobLogout
		r, ok = spec.LogoutRecipe()
	}
	if !ok {
		return nil, nodeErr(proto.ErrCodeInvalid, "sous 不知道在这个系统上怎样%s %s", map[string]string{
			proto.JobInstall: "安装", proto.JobUpgrade: "升级", proto.JobLogin: "登录", proto.JobLogout: "退出登录"}[kind], spec.Title)
	}
	path, argv, err := r.Command()
	if err != nil {
		return nil, nodeErr(proto.ErrCodeFailed, "找不到命令：%v", err)
	}

	a.mu.Lock()
	running := 0
	for id, j := range a.jobs {
		j.mu.Lock()
		live, ended := j.running, j.endedAt
		j.mu.Unlock()
		if live && j.kind != proto.JobShell {
			running++
			if j.agent == spec.Name {
				a.mu.Unlock()
				return nil, nodeErr(proto.ErrCodeBusy, "%s 正在%s，等它结束再试", spec.Title, jobTitle(j.kind))
			}
		} else if time.Since(ended) > jobKeep {
			delete(a.jobs, id)
		}
	}
	if running >= maxJobs {
		a.mu.Unlock()
		return nil, nodeErr(proto.ErrCodeBusy, "这台机器上同时进行的安装和登录太多了")
	}
	j := &job{a: a, id: ids.New(), kind: kind, agent: spec.Name, command: r.String(), running: true}
	a.jobs[j.id] = j
	a.mu.Unlock()

	home, _ := os.UserHomeDir()
	env := append(append(os.Environ(), spec.Env...), r.Env...)
	if err := j.start(path, argv, home, env, args.Cols, args.Rows); err != nil {
		a.mu.Lock()
		delete(a.jobs, j.id)
		a.mu.Unlock()
		return nil, nodeErr(proto.ErrCodeFailed, "启动失败：%v", err)
	}
	slog.Info("daemon: job started", "job", j.id, "kind", kind, "agent", spec.Name, "command", j.command)
	a.kick() // the inventory names the running job, for pages opened later
	return proto.JobStarted{JobID: j.id, Kind: kind, Agent: spec.Name, Command: j.command}, nil
}

func jobTitle(kind string) string {
	return map[string]string{proto.JobInstall: "安装", proto.JobUpgrade: "升级", proto.JobLogin: "登录", proto.JobLogout: "退出登录"}[kind]
}

// start runs path on a new pseudo-terminal in dir.
func (j *job) start(path string, argv []string, dir string, env []string, cols, rows int) error {
	p, err := pty.New()
	if err != nil {
		return err
	}
	if cols <= 0 || cols > 500 {
		cols = 100
	}
	if rows <= 0 || rows > 200 {
		rows = 30
	}
	p.Resize(cols, rows)
	ctx, cancel := context.WithTimeout(context.Background(), jobTimeouts[j.kind])
	c := p.CommandContext(ctx, path, argv...)
	c.Dir = dir
	c.Env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
	if err := c.Start(); err != nil {
		cancel()
		p.Close()
		return err
	}
	j.pty, j.cmd, j.cancel = p, c, cancel

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 8192)
		for {
			n, err := p.Read(buf)
			if n > 0 {
				j.emit(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		werr := c.Wait()
		// Let the last output drain before closing the terminal.
		select {
		case <-readDone:
		case <-time.After(500 * time.Millisecond):
		}
		p.Close()
		<-readDone
		cancel()
		code := 0
		msg := ""
		if werr != nil {
			code = -1
			var ee interface{ ExitCode() int }
			if errors.As(werr, &ee) {
				code = ee.ExitCode()
			}
			msg = werr.Error()
			if ctx.Err() == context.DeadlineExceeded {
				msg = fmt.Sprintf("超过 %v 没有结束，已停止", jobTimeouts[j.kind])
			}
		} else if c.ProcessState != nil {
			code = c.ProcessState.ExitCode()
		}
		j.finish(code, msg)
	}()
	return nil
}

// emit sends a piece of output, holding back an incomplete UTF-8 sequence
// at its end for the next piece.
func (j *job) emit(b []byte) {
	j.mu.Lock()
	data := append(j.carry, b...)
	cut := len(data)
	for i := len(data) - 1; i >= 0 && i >= len(data)-utf8.UTFMax; i-- {
		if utf8.RuneStart(data[i]) {
			if !utf8.FullRune(data[i:]) {
				cut = i
			}
			break
		}
	}
	j.carry = append([]byte(nil), data[cut:]...)
	data = data[:cut]
	if len(data) == 0 {
		j.mu.Unlock()
		return
	}
	off := j.offset
	j.offset += int64(len(data))
	j.tailBuf = append(j.tailBuf, data...)
	if over := len(j.tailBuf) - jobTailBytes; over > 0 {
		j.tailBuf = j.tailBuf[over:]
	}
	j.mu.Unlock()
	j.a.d.ob.ephemeral(proto.New(proto.TNodeStream, proto.NodeStream{JobID: j.id, Offset: off, Data: string(data)}))
}

func (j *job) finish(code int, msg string) {
	j.mu.Lock()
	if j.stopped {
		msg = JobStoppedError
	}
	j.running = false
	j.exit = &code
	j.endedAt = time.Now()
	j.mu.Unlock()
	slog.Info("daemon: job ended", "job", j.id, "kind", j.kind, "agent", j.agent, "exit", code, "err", msg)
	j.a.d.ob.reliable(proto.New(proto.TJobEnded, proto.JobEnded{JobID: j.id, Kind: j.kind, Agent: j.agent, ExitCode: code, Error: msg}))
	agents.AugmentPath() // an installer may have created a new bin directory
	j.a.kick()
}

func (j *job) input(data string) error {
	j.mu.Lock()
	live := j.running
	j.mu.Unlock()
	if !live {
		return nodeErr(proto.ErrCodeInvalid, "已经结束了")
	}
	_, err := j.pty.Write([]byte(data))
	return err
}

func (j *job) resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 || cols > 500 || rows > 200 {
		return nodeErr(proto.ErrCodeInvalid, "终端大小不对")
	}
	j.mu.Lock()
	live := j.running
	j.mu.Unlock()
	if !live {
		return nil
	}
	return j.pty.Resize(cols, rows)
}

func (j *job) stop() {
	j.mu.Lock()
	live := j.running
	j.mu.Unlock()
	if !live {
		return
	}
	j.mu.Lock()
	j.stopped = true
	j.mu.Unlock()
	if j.cmd.Process != nil {
		killTree(j.cmd.Process)
	}
	j.cancel()
}

// JobStoppedError is the error of a job the user stopped.
const JobStoppedError = "已停止"

func (j *job) tail() proto.JobTail {
	j.mu.Lock()
	defer j.mu.Unlock()
	data := j.tailBuf
	start := j.offset - int64(len(data))
	// Start on a character boundary.
	for len(data) > 0 && !utf8.RuneStart(data[0]) {
		data = data[1:]
		start++
	}
	return proto.JobTail{JobID: j.id, Kind: j.kind, Agent: j.agent, Command: j.command, Offset: start, Data: string(data), Running: j.running, Exit: j.exit}
}

// stopJobs ends every job when the daemon stops.
func (a *admin) stopJobs() {
	a.mu.Lock()
	jobs := make([]*job, 0, len(a.jobs))
	for _, j := range a.jobs {
		jobs = append(jobs, j)
	}
	a.mu.Unlock()
	for _, j := range jobs {
		j.stop()
	}
}
