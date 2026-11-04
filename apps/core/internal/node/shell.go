package node

import (
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// Shells the user opens from a project's page: the machine's own shell on a
// pseudo-terminal, started in the project's directory. They reuse jobs for
// output, input, resizing and closing. Unlike agent runs they do not go
// through the permission policy: the user types every command. So only a
// project checkout inside a workspace can be the directory, node.json can
// turn shells off (remote_shell: false), and a shell belongs to the login
// session that opened it (owner): no other session can list or use it.

const maxShells = 8

func (a *admin) openShell(args proto.ShellArgs) (any, error) {
	if !a.d.conf().ShellEnabled() {
		return nil, nodeErr(proto.ErrCodeDisabled, "这台机器在 node.json 里关闭了远程终端（remote_shell: false）")
	}
	if args.Owner == "" {
		return nil, nodeErr(proto.ErrCodeInvalid, "缺少终端的所有者")
	}
	root, _, _, err := a.projectPath(proto.ProjectArgs{Root: args.Root})
	if err != nil {
		return nil, err
	}
	path, argv := shellCommand()
	command := strings.Join(append([]string{path}, argv...), " ")

	a.mu.Lock()
	live := 0
	for id, j := range a.jobs {
		j.mu.Lock()
		running, ended := j.running, j.endedAt
		j.mu.Unlock()
		if running && j.kind == proto.JobShell {
			live++
		} else if !running && time.Since(ended) > jobKeep {
			delete(a.jobs, id)
		}
	}
	if live >= maxShells {
		a.mu.Unlock()
		return nil, nodeErr(proto.ErrCodeBusy, "这台机器上已经开了 %d 个终端，先关掉一些", maxShells)
	}
	j := &job{a: a, id: ids.New(), kind: proto.JobShell, command: command, root: root, owner: args.Owner,
		started: time.Now(), running: true}
	a.jobs[j.id] = j
	a.mu.Unlock()

	env := append(os.Environ(), "SOUS_SHELL=1")
	if err := j.start(path, argv, root, env, args.Cols, args.Rows); err != nil {
		a.mu.Lock()
		delete(a.jobs, j.id)
		a.mu.Unlock()
		return nil, nodeErr(proto.ErrCodeFailed, "启动终端失败：%v", err)
	}
	slog.Info("daemon: shell opened", "job", j.id, "dir", root, "command", command)
	return proto.JobStarted{JobID: j.id, Kind: proto.JobShell, Command: command}, nil
}

// listShells returns the owner's shells in a directory, newest last.
func (a *admin) listShells(args proto.ShellArgs) (any, error) {
	if args.Owner == "" {
		return []proto.ShellInfo{}, nil
	}
	root, _, _, err := a.projectPath(proto.ProjectArgs{Root: args.Root})
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []proto.ShellInfo{}
	for _, j := range a.jobs {
		if j.kind != proto.JobShell || j.owner != args.Owner || j.root != root {
			continue
		}
		j.mu.Lock()
		out = append(out, proto.ShellInfo{JobID: j.id, Root: j.root, Command: j.command, StartedAt: j.started.UnixMilli(),
			Running: j.running, Exit: j.exit})
		j.mu.Unlock()
	}
	sort.Slice(out, func(i, k int) bool { return out[i].StartedAt < out[k].StartedAt })
	return out, nil
}

// ownedBy reports whether a request with owner may use the job: shells only
// their owner, other jobs anyone the Hub lets through.
func (j *job) ownedBy(owner string) bool {
	return j.kind != proto.JobShell || (owner != "" && owner == j.owner)
}
