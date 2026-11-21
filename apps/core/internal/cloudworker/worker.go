//go:build cgo && ((darwin && arm64) || (linux && (amd64 || arm64) && sous_linux_worker))

package cloudworker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/cloudwork"
	"github.com/XiaoXianThis/Sous/apps/core/internal/localrun"
	"golang.org/x/sys/unix"
)

// PrepareImage is an explicit provisioning step; it runs without a Hub
// connection, an execution lease or a runtime account.
func PrepareImage(ctx context.Context, output string) error {
	return localrun.DownloadImageArchive(ctx, output)
}

func Run(ctx context.Context, c *Client, o Options) error {
	if !filepath.IsAbs(o.State) || !filepath.IsAbs(o.Runtime) || !filepath.IsAbs(o.Image) || o.State == o.Runtime {
		return errors.New("worker 必须使用独立的绝对状态路径、运行时路径和已核验的离线镜像")
	}
	if err := os.Mkdir(o.State, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(o.State)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("worker 状态目录必须由服务账户独占（0700）")
	}
	actual, err := filepath.EvalSymlinks(o.State)
	if err != nil || actual != o.State {
		return errors.New("worker 状态目录不能经过链接")
	}
	fd, err := unix.Open(o.State, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || int(st.Uid) != os.Getuid() || st.Mode&0o077 != 0 {
		return errors.New("worker 状态目录必须由当前服务账户独占（0700）")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.New("已经有 worker 持有此检查点目录")
	}
	root, err := os.OpenRoot(o.State)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := bindState(root, c.origin, o.Runtime, c.generation); err != nil {
		return err
	}
	opts := localrun.GuestOptions{Home: o.Runtime, RuntimePath: o.MSB, FirmwarePath: o.Firmware, ImageArchive: o.Image, Timeout: 3 * time.Minute}
	pending, err := readPending(root)
	if err != nil {
		return err
	}
	if pending != nil {
		if c.generation > 0 {
			if c.token, err = workerSession(root, false); err != nil {
				return err
			}
		}
		if err := localrun.Recover(ctx, opts); err != nil {
			return err
		}
		if err := publish(ctx, c, root, *pending); err != nil {
			return err
		}
		if o.Once {
			return nil
		}
	}
	// Do not consume a lease on a worker that has no real KVM, missing
	// runtime binaries, or an unverified image. Recovery above does not need
	// the image, so returning an existing checkpoint remains possible.
	if err := localrun.InitializeHosted(ctx, opts); err != nil {
		return fmt.Errorf("云执行领取前预检失败：%w", err)
	}
	if c.generation > 0 {
		if c.token, err = workerSession(root, c.bootstrap != ""); err != nil {
			return err
		}
		if err := c.ready(ctx); err != nil {
			return fmt.Errorf("云执行 worker 注册未确认：%w", err)
		}
	}
	for ctx.Err() == nil {
		job, err := c.Claim(ctx)
		if err != nil {
			if errors.Is(err, errLease) || o.Once {
				return err
			}
			slog.Warn("cloud worker cannot reach Hub; no work started")
		}
		if job != nil {
			if err := execute(ctx, c, root, opts, job); err != nil {
				return err
			}
			if o.Once {
				return nil
			}
			continue
		}
		if o.Once {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
	return nil
}

func execute(ctx context.Context, c *Client, root *os.Root, opts localrun.GuestOptions, job *cloudwork.Claim) error {
	if job.Workload != cloudwork.Workload || !cloudwork.ValidToken(job.Lease) || job.JobID == "" || !cloudwork.ValidScript(job.Script) || job.Files.Validate(false) != nil {
		return errors.New("Hub 发送了无效的云执行授权")
	}
	stop, err := c.Heartbeat(ctx, job.JobID, job.Lease)
	if err != nil {
		return err
	}
	if stop {
		p := cloudwork.Complete{JobID: job.JobID, Lease: job.Lease, Outcome: "cancelled", ExitCode: -1}
		if err := savePending(root, p); err != nil {
			return err
		}
		flush, done := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer done()
		return publish(flush, c, root, p)
	}
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	monitor := make(chan bool, 1)
	go func() {
		for {
			stop, err := c.Heartbeat(run, job.JobID, job.Lease)
			if err != nil || stop {
				cancel()
				monitor <- stop
				return
			}
			select {
			case <-run.Done():
				monitor <- false
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
	opts.Command = []string{"/bin/sh", "-ec", job.Script}
	var result cloudwork.Complete
	checkpoint := func(exit int, materials map[string][]byte) error {
		outcome := "failed"
		if exit == 0 {
			outcome = "succeeded"
		}
		result = cloudwork.Complete{JobID: job.JobID, Lease: job.Lease, Outcome: outcome, ExitCode: exit, Files: materials}
		return savePending(root, result)
	}
	opts.Progress = func(msg string) { slog.Info("cloud worker progress", "job", job.JobID, "phase", msg) }
	err = localrun.RunHosted(run, opts, job.Files, checkpoint)
	cancel()
	cancelled := <-monitor
	if errors.Is(err, localrun.ErrCleanupUnconfirmed) {
		return errors.New("Guest 未确认回收或检查点未保存；保留状态，不领取新任务")
	}
	if err != nil {
		outcome := "interrupted"
		if cancelled {
			outcome = "cancelled"
		}
		result = cloudwork.Complete{JobID: job.JobID, Lease: job.Lease, Outcome: outcome, ExitCode: -1}
		if err := savePending(root, result); err != nil {
			return err
		}
	}
	if result.JobID == "" {
		return errors.New("没有可发布的持久检查点")
	}
	flush, done := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer done()
	return publish(flush, c, root, result)
}

// A pending result must be published to the same named workspace, only after
// this worker has reconciled the exact runtime that may still own its Guest.
func bindState(root *os.Root, origin, runtime string, generation int64) error {
	const file = "worker-binding.json"
	expected, err := json.Marshal(struct {
		Origin, Runtime string
		Generation      int64 `json:",omitempty"`
	}{origin, runtime, generation})
	if err != nil {
		return err
	}
	if st, err := root.Lstat(file); err == nil {
		if !st.Mode().IsRegular() || st.Mode().Perm()&0o077 != 0 || st.Size() > 4096 {
			return errors.New("worker 绑定文件无效")
		}
		actual, err := root.ReadFile(file)
		if err != nil || !bytes.Equal(actual, expected) {
			return errors.New("检查点目录已经绑定到其他工作区或运行时；拒绝自动迁移")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := root.Stat(pendingName); !errors.Is(err, os.ErrNotExist) {
		return errors.New("未绑定的检查点需要运维核对，拒绝自动迁移")
	}
	return writeState(root, file, expected)
}

// Session bytes belong only to the current worker's locked state directory.
// Persist them BEFORE registration so a crash cannot orphan an active identity.
func workerSession(root *os.Root, create bool) (string, error) {
	const file = "worker-session"
	st, err := root.Lstat(file)
	if err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o077 == 0 && st.Size() == 64 {
		token, err := root.ReadFile(file)
		if err == nil && cloudwork.ValidToken(string(token)) {
			return string(token), nil
		}
	}
	if !create || !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("worker 会话密钥缺失或损坏，需要运维对账")
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw[:])
	if err := writeState(root, file, []byte(tok)); err != nil {
		return "", err
	}
	return tok, nil
}

func writeState(root *os.Root, name string, data []byte) error {
	part := name + ".partial"
	f, err := root.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err := root.Rename(part, name); err != nil {
		return err
	}
	return syncState(root)
}

func syncState(root *os.Root) error {
	d, err := root.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

const pendingName = "pending.json"

func readPending(root *os.Root) (*cloudwork.Complete, error) {
	const limit = 2 << 20
	before, err := root.Lstat(pendingName)
	if errors.Is(err, os.ErrNotExist) {
		if _, partialErr := root.Lstat(pendingName + ".partial"); !errors.Is(partialErr, os.ErrNotExist) {
			return nil, errors.New("检查点写入曾中断，需要运维对账")
		}
		return nil, nil
	}
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0o077 != 0 || before.Size() > limit {
		return nil, errors.New("检查点文件无效")
	}
	f, err := root.Open(pendingName)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if after, err := f.Stat(); err != nil || !os.SameFile(before, after) {
		return nil, errors.New("检查点文件已变化，需要运维对账")
	}
	var c cloudwork.Complete
	decoder := json.NewDecoder(io.LimitReader(f, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil || decoder.Decode(new(json.RawMessage)) != io.EOF || c.Validate() != nil {
		return nil, errors.New("检查点无效，需要运维对账")
	}
	return &c, nil
}
func savePending(root *os.Root, p cloudwork.Complete) error {
	if err := p.Validate(); err != nil {
		return err
	}
	// This file is fsynced before any VM can be reclaimed. A crash after
	// rename but before upload is resumed as publish-only, never execution.
	if _, err := root.Stat(pendingName); !errors.Is(err, os.ErrNotExist) {
		return errors.New("尚有待发布检查点")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return writeState(root, pendingName, raw)
}
func publish(ctx context.Context, c *Client, root *os.Root, p cloudwork.Complete) error {
	for {
		err := c.Complete(ctx, p)
		if err == nil {
			if err := root.Remove(pendingName); err != nil {
				return err
			}
			if err := syncState(root); err != nil {
				return err
			}
			slog.Info("cloud worker finished: guest released and Hub stored receipt", "job", p.JobID, "outcome", p.Outcome)
			return nil
		}
		if errors.Is(err, errLease) {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.New("未确认 Hub 已保存结果，检查点已保留")
		case <-time.After(time.Second):
		}
	}
}
