//go:build cgo && ((darwin && arm64) || (linux && (amd64 || arm64) && sous_linux_worker))

package localrun

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
	"golang.org/x/sys/unix"
)

const (
	guestProject = "/sous-project"
	maxLog       = 128 << 10
	namePrefix   = "sous-run-"
)

// GuestOptions contains operator-controlled runtime paths and Guest argv.
// It never carries a project path or instructs the host to execute argv.
type GuestOptions struct {
	Home, RuntimePath, FirmwarePath, ImageArchive string
	Command                                       []string
	Timeout                                       time.Duration
	Progress                                      func(string)
}

func (o GuestOptions) progress(phase string) {
	if o.Progress != nil {
		o.Progress(phase)
	}
}

func (o GuestOptions) validateCommand() error {
	if len(o.Command) == 0 || strings.TrimSpace(o.Command[0]) == "" || len(o.Command) > 100 {
		return errors.New("请提供一个要在 Guest 内运行的命令")
	}
	argBytes := 0
	for _, arg := range o.Command {
		argBytes += len(arg)
		if argBytes > 16<<10 || strings.ContainsRune(arg, 0) {
			return errors.New("Guest 命令参数无效或过长")
		}
	}
	if o.Timeout < time.Second || o.Timeout > 5*time.Minute {
		return errors.New("执行时限须在 1 秒至 5 分钟之间")
	}
	return nil
}

// The SDK selects its local state and runtime through process environment.
// A helper normally handles just one invocation, and callers cannot overlap it.
var processLock sync.Mutex

// ErrCleanupUnconfirmed means a caller must not treat interruption as an
// observed cancellation; the next local run needs recovery first.
var ErrCleanupUnconfirmed = errors.New("Guest 销毁未确认")

// Recover reconciles a private runtime after interruption; no project is read.
func Recover(ctx context.Context, o GuestOptions) error {
	if !processLock.TryLock() {
		return errors.New("另一个本机沙盒运行正在进行")
	}
	defer processLock.Unlock()
	unlock, err := prepare(ctx, o, true)
	if err != nil {
		return err
	}
	defer unlock()
	return nil
}

func verifyBinary(p, want string) error {
	if !filepath.IsAbs(p) {
		return errors.New("运行时文件需要绝对路径")
	}
	st, err := os.Lstat(p)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 256<<20 {
		return errors.New("未找到所需的正式运行时文件，或路径不是普通文件")
	}
	fd, err := unix.Open(p, safeOpen, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), p)
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(st, actual) {
		return errors.New("运行时文件在校验期间被替换")
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(file, 256<<20)); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return errors.New("运行时校验失败：仅允许固定的官方 Microsandbox v0.7.16 文件")
	}
	return nil
}

func prepare(ctx context.Context, o GuestOptions, allowRecovery bool) (func(), error) {
	if !filepath.IsAbs(o.Home) || filepath.Clean(o.Home) != o.Home || len(o.Home) > 48 {
		return nil, errors.New("需要独立且较短的绝对运行时目录（最多 48 字节）")
	}
	if err := checkHypervisor(); err != nil {
		return nil, err
	}
	if err := verifyBinary(o.RuntimePath, runtimeSHA256); err != nil {
		return nil, fmt.Errorf("msb: %w", err)
	}
	if err := verifyBinary(o.FirmwarePath, firmwareSHA256); err != nil {
		return nil, fmt.Errorf("libkrunfw: %w", err)
	}
	if err := os.Mkdir(o.Home, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	home, err := filepath.EvalSymlinks(o.Home)
	if err != nil || home != o.Home {
		return nil, errors.New("运行时目录不能经过链接")
	}
	fd, err := unix.Open(home, safeOpen|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil || int(info.Uid) != os.Getuid() || info.Mode&0o077 != 0 {
		return nil, errors.New("运行时目录须由当前用户独占（0700）")
	}
	// Lock the directory inode, not a replaceable .lock path. Another helper
	// run must wait outside the authorization window and retry explicitly.
	lockfd, err := unix.Openat(fd, ".", safeOpen|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(lockfd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = unix.Close(lockfd)
		return nil, errors.New("另一个本机沙盒运行或清理正在进行")
	}
	unlock := func() { _ = unix.Close(lockfd) }
	ok := false
	defer func() {
		if !ok {
			unlock()
		}
	}()
	// Refuse an existing SDK home that Sous didn't initialize; never reclaim
	// another application's guests, even if it uses similar names or labels.
	marker := filepath.Join(home, ".sous-runtime-v1")
	expected := []byte("sous-local-execution:1\n")
	if buf, err := os.ReadFile(marker); err != nil || !bytes.Equal(buf, expected) {
		es, readErr := os.ReadDir(home)
		if readErr != nil || len(es) != 0 {
			return nil, errors.New("请选择空目录或专用于 Sous 的运行时目录")
		}
		if err := os.WriteFile(marker, expected, 0o600); err != nil {
			return nil, err
		}
	}
	for _, kv := range [][2]string{{"MSB_HOME", home}, {"MSB_PATH", o.RuntimePath}, {"MSB_LIBKRUNFW_PATH", o.FirmwarePath}} {
		key, value := kv[0], kv[1]
		prior, had := os.LookupEnv(key)
		if err := os.Setenv(key, value); err != nil {
			return nil, err
		}
		closeLock := unlock
		unlock = func() {
			if had {
				_ = os.Setenv(key, prior)
			} else {
				_ = os.Unsetenv(key)
			}
			closeLock()
		}
	}
	backend, err := msb.DefaultBackendInfo()
	if err != nil || backend.Kind != msb.BackendLocal {
		return nil, errors.New("只有本机 Microsandbox 后端可以运行")
	}
	runtime, err := msb.ResolveRuntime(msb.RuntimeConfig{})
	if err != nil || runtime.MSBPath != o.RuntimePath || runtime.LibkrunfwPath != o.FirmwarePath {
		return nil, errors.New("无法确认运行时使用已经校验的二进制")
	}
	if allowRecovery {
		if err := recoverGuest(ctx, home); err != nil {
			return nil, fmt.Errorf("上一次的 Guest 未确认销毁，拒绝开始新任务：%w", errors.Join(ErrCleanupUnconfirmed, err))
		}
	} else if err := requireNoGuest(home); err != nil {
		return nil, err
	}
	ok = true
	return unlock, nil
}

func journal(home string) string { return filepath.Join(home, ".sous-active-run") }

// Hosted startup and execution must not reap an uncertain attempt. The
// owning worker must first supply a checkpoint, or an operator reconcile it.
// Call while holding the runtime directory lock.
func requireNoGuest(home string) error {
	if _, err := os.Lstat(journal(home)); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("上次 Guest 需要对账；不会自动销毁或重新领取：%w", ErrCleanupUnconfirmed)
	}
	return nil
}

func recoverGuest(parent context.Context, home string) error {
	f, err := os.Open(journal(home))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	buf, err := io.ReadAll(io.LimitReader(f, 64))
	_ = f.Close()
	if err != nil {
		return err
	}
	name := string(buf)
	if !strings.HasPrefix(name, namePrefix) || len(name) != len(namePrefix)+10 || !safeName(name) {
		return errors.New("运行时对账记录无效")
	}
	return reclaim(parent, home, name)
}

func reclaim(parent context.Context, home, name string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 35*time.Second)
	defer cancel()
	h, err := msb.GetSandbox(ctx, name)
	if err == nil {
		if err := h.Destroy(ctx, msb.WithDestroyForce()); err != nil && !msb.IsKind(err, msb.ErrSandboxNotFound) {
			return err
		}
	} else if !msb.IsKind(err, msb.ErrSandboxNotFound) {
		return err
	}
	if _, err := msb.GetSandbox(ctx, name); !msb.IsKind(err, msb.ErrSandboxNotFound) {
		return fmt.Errorf("Guest 仍存在或无法确认状态：%v", err)
	}
	if err := os.Remove(journal(home)); err != nil {
		return err
	}
	return syncRuntimeDir(home)
}

// Publish the fully written journal before asking the runtime to create a VM.
// An interrupted write leaves only an unused temporary file, never a partial
// active entry pointing to a VM that cannot be reconciled.
func recordGuest(home, id string) error {
	if _, err := os.Lstat(journal(home)); !errors.Is(err, os.ErrNotExist) {
		return errors.New("运行时还有未对账的 Guest")
	}
	f, err := os.CreateTemp(home, ".sous-run-pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.WriteString(id)
	if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), journal(home)); err != nil {
		return err
	}
	return syncRuntimeDir(home)
}

// Journal creation must survive a worker host reboot before the Guest starts;
// removal must likewise be durable before publishing a completion receipt.
func syncRuntimeDir(home string) error {
	d, err := os.Open(home)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

func guestRun(ctx context.Context, id string, before snapshot, o GuestOptions, offline bool, checkpoint func(int, map[string][]byte) error) (after snapshot, exit int, logs []byte, err error) {
	if e := recordGuest(o.Home, id); e != nil {
		return after, -1, nil, errors.Join(ErrCleanupUnconfirmed, e)
	}
	preserve := false
	defer func() {
		if preserve {
			err = errors.Join(err, ErrCleanupUnconfirmed)
			return
		}
		if e := reclaim(ctx, o.Home, id); e != nil {
			err = errors.Join(err, fmt.Errorf("未确认 Guest 销毁；请用同一个运行时目录重试对账：%w", errors.Join(ErrCleanupUnconfirmed, e)))
		}
	}()
	pull := msb.PullPolicyIfMissing
	if offline {
		if e := verifyCachedImage(ctx); e != nil {
			return after, -1, nil, errors.New("离线模式未找到固定镜像，请在获准联网时先完成一次镜像准备")
		}
		pull = msb.PullPolicyNever
	}
	o.progress("启动隔离 Guest（镜像未缓存时需准备）")
	box, e := msb.CreateSandbox(ctx, id, msb.WithImage(image), msb.WithPullPolicy(pull), msb.WithRegistryAnonymous(), msb.WithNetwork(msb.NetworkPolicy.None()), msb.WithCPUs(1), msb.WithMemory(384), msb.WithMaxDuration(o.Timeout+2*time.Minute))
	if e != nil {
		return after, -1, nil, fmt.Errorf("无法启动隔离 Guest：%w", e)
	}
	defer box.Close()
	o.progress("导入获准文件的副本")
	fs := box.FS()
	if e := fs.Mkdir(ctx, guestProject); e != nil {
		return after, -1, nil, e
	}
	for _, d := range before.dirs {
		if e := fs.Mkdir(ctx, path.Join(guestProject, d)); e != nil {
			return after, -1, nil, e
		}
	}
	for rel, data := range before.files {
		if e := fs.Write(ctx, path.Join(guestProject, rel), data); e != nil {
			return after, -1, nil, e
		}
	}
	exit, logs, e = runCommand(ctx, box, o)
	if e != nil {
		return after, exit, nil, e
	}
	after = newSnapshot()
	if e := collect(ctx, fs, &after, "", 0); e != nil {
		preserve = checkpoint != nil && ctx.Err() == nil
		return after, exit, nil, fmt.Errorf("Guest 产物未导出：%w", e)
	}
	if checkpoint != nil {
		o.progress("正在持久化 Guest 检查点")
		if e := checkpoint(exit, after.files); e != nil {
			preserve = true
			return after, exit, nil, fmt.Errorf("Guest 检查点未保存，保留运行时供对账：%w", e)
		}
	}
	return after, exit, logs, nil
}

func runCommand(ctx context.Context, box *msb.Sandbox, o GuestOptions) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	exec, err := box.ExecStream(ctx, o.Command[0], o.Command[1:], msb.WithExecCwd(guestProject), msb.WithExecTimeout(o.Timeout))
	if err != nil {
		return -1, nil, err
	}
	defer exec.Close()
	var log bytes.Buffer
	exit, exited := -1, false
	for {
		ev, err := exec.Recv(ctx)
		if err != nil {
			return -1, nil, err
		}
		if ev == nil || ev.Kind == msb.ExecEventDone {
			break
		}
		switch ev.Kind {
		case msb.ExecEventStarted:
			o.progress("正在 Guest 内运行命令")
		case msb.ExecEventExited:
			exit, exited = ev.ExitCode, true
		case msb.ExecEventFailed:
			return -1, nil, errors.New("无法在 Guest 内启动指定命令")
		case msb.ExecEventStdout, msb.ExecEventStderr:
			if data := ev.Data; len(data) > 0 && log.Len() < maxLog {
				_, _ = log.Write(data[:min(len(data), maxLog-log.Len())])
			}
		}
	}
	if !exited || ctx.Err() != nil {
		return -1, nil, errors.New("Guest 命令未正常结束")
	}
	if log.Len() == maxLog {
		log.WriteString("\n[Sous: log truncated at " + strconv.Itoa(maxLog) + " bytes]\n")
	}
	return exit, log.Bytes(), nil
}

// collect accepts only regular files and directories with bounded bytes. It
// treats all names/metadata as untrusted and never copies to a path chosen by
// a guest. Excluded entries (and generated dotfiles) are not returned.
func collect(ctx context.Context, fs *msb.SandboxFSOps, s *snapshot, prefix string, depth int) error {
	if depth > maxDepth {
		return errors.New("产物目录过深")
	}
	entries, err := fs.List(ctx, path.Join(guestProject, prefix))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := path.Base(entry.Path)
		if entry.Path != name && entry.Path != path.Join(guestProject, prefix, name) {
			return errors.New("Guest 返回了意外的产物路径")
		}
		if err := s.entry(); err != nil {
			return err
		}
		if excluded(name) {
			continue
		}
		rel := path.Join(prefix, name)
		if !safeName(name) || !relative(rel) {
			return errors.New("Guest 返回了不安全的文件名称")
		}
		p := path.Join(guestProject, rel)
		switch entry.Kind {
		case msb.FsEntryKindDirectory:
			if err := collect(ctx, fs, s, rel, depth+1); err != nil {
				return err
			}
		case msb.FsEntryKindFile:
			if entry.Size > int64(min(MaxFile, maxBytes-s.total)) {
				return errors.New("Guest 文件超过产物大小限制")
			}
			stream, err := fs.ReadStream(ctx, p)
			if err != nil {
				return err
			}
			err = s.read(ctx, rel, &guestReader{ctx: ctx, stream: stream})
			_ = stream.Close()
			if err != nil {
				return err
			}
		default:
			return errors.New("不导出 Guest 中的链接或特殊文件")
		}
	}
	return nil
}

type guestReader struct {
	ctx       context.Context
	stream    *msb.FsReadStream
	remaining []byte
}

func (r *guestReader) Read(out []byte) (int, error) {
	if len(r.remaining) == 0 {
		chunk, err := r.stream.Recv(r.ctx)
		if err != nil {
			return 0, err
		}
		if chunk == nil {
			return 0, io.EOF
		}
		r.remaining = chunk
	}
	n := copy(out, r.remaining)
	r.remaining = r.remaining[n:]
	return n, nil
}
