package localrun

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// Review contains the portable manifest and the version the owner approved.
// Source is intentionally not exported: it belongs to private authorization.
type Review struct {
	Report
	Revision string `json:"revision"`
	CanApply bool   `json:"can_apply"`
}

type Applied struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
	// Backup is a relative path to the original inode. Do not discard it
	// until editors/background processes have stopped using the original.
	Backup string `json:"local_backup,omitempty"`
}

func InspectReview(dir string) (Review, error) {
	f, review, _, err := openReview(dir)
	if f != nil {
		_ = f.Close()
	}
	return review, err
}

// FilePreview is bounded, plain-text review data. The original artifact
// bytes remain private on disk and binary data is never rendered as text.
type FilePreview struct {
	Change
	Revision   string  `json:"revision"`
	BeforeText *string `json:"before_text,omitempty"`
	AfterText  *string `json:"after_text,omitempty"`
	Binary     bool    `json:"binary"`
	Truncated  bool    `json:"truncated"`
}

func PreviewFile(dir, revision, target string) (FilePreview, error) {
	var result FilePreview
	if !validDigest(revision) || !relative(target) {
		return result, errors.New("需要指定审阅版本和一个相对文件路径")
	}
	f, r, _, err := openReview(dir)
	if err != nil {
		return result, err
	}
	defer f.Close()
	if r.Revision != revision {
		return result, errors.New("审阅版本已经改变")
	}
	change, err := pickChange(r, target)
	if err != nil {
		return result, err
	}
	result = FilePreview{Change: change, Revision: r.Revision}
	for i, part := range []struct{ name, hash string }{{change.BeforeFile, change.Before}, {change.AfterFile, change.After}} {
		if part.name == "" {
			continue
		}
		data, err := verifiedArtifact(int(f.Fd()), part.name, part.hash)
		if err != nil {
			return FilePreview{}, err
		}
		if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
			result.Binary = true
			continue
		}
		const displayLimit = 64 << 10
		text := string(data)
		if len(text) > displayLimit {
			text = strings.ToValidUTF8(text[:displayLimit], "")
			result.Truncated = true
		}
		text = strings.Map(func(r rune) rune {
			if r != '\t' && r != '\r' && r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
				return '�'
			}
			return r
		}, text)
		if i == 0 {
			result.BeforeText = &text
		} else {
			result.AfterText = &text
		}
	}
	return result, nil
}

func PreviewLog(dir, revision string) (string, error) {
	f, r, _, err := openReview(dir)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if r.Revision != revision {
		return "", errors.New("审阅版本已经改变")
	}
	data, err := readPrivate(int(f.Fd()), "guest.log", (128<<10)+1024)
	if err != nil {
		return "", err
	}
	return strings.Map(func(r rune) rune {
		if r != '\t' && r != '\n' && r != '\r' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			return '�'
		}
		return r
	}, string(data)), nil
}

func pickChange(r Review, target string) (Change, error) {
	for _, c := range r.Changes {
		if c.Path == target {
			return c, nil
		}
	}
	return Change{}, errors.New("未找到已审阅的变更")
}

func verifiedArtifact(fd int, name, hash string) ([]byte, error) {
	data, err := readPrivate(fd, name, MaxFile)
	if err != nil || digest(data) != hash {
		return nil, errors.New("文件内容与审阅清单的校验值不匹配")
	}
	return data, nil
}

func openReview(dir string) (*os.File, Review, binding, error) {
	var r Review
	var b binding
	parent, name, err := openParent(dir)
	if err != nil {
		return nil, r, b, err
	}
	defer parent.Close()
	if !strings.HasPrefix(name, "sous-result-") || !safeName(name) {
		return nil, r, b, errors.New("请选择 Sous 生成的审阅目录")
	}
	fd, err := unix.Openat(int(parent.Fd()), name, safeOpen|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, r, b, err
	}
	f := os.NewFile(uintptr(fd), dir)
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
		}
	}()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Uid != uint32(os.Getuid()) || st.Mode&0o077 != 0 {
		return nil, r, b, errors.New("审阅目录必须仅本机用户可访问")
	}
	data, err := readPrivate(fd, "manifest.json", 1<<20)
	if err != nil {
		return nil, r, b, err
	}
	if json.Unmarshal(data, &r.Report) != nil || r.Version != 1 || len(r.Changes) > 2*maxEntries || r.RunID == "" {
		return nil, r, b, errors.New("无法识别审阅清单")
	}
	var seen = map[string]bool{}
	for i, c := range r.Changes {
		valid := relative(c.Path) && !seen[c.Path] && len(strings.Split(c.Path, "/")) <= maxDepth+1 && c.Before != c.After
		seen[c.Path] = true
		before := validDigest(c.Before) && c.BeforeFile == fmt.Sprintf("change-%04d-before", i+1)
		after := validDigest(c.After) && c.AfterFile == fmt.Sprintf("change-%04d-after", i+1)
		if !valid || !(c.Operation == "modified" && before && after || c.Operation == "added" && c.Before == "" && c.BeforeFile == "" && after || c.Operation == "removed" && before && c.After == "" && c.AfterFile == "") {
			return nil, r, b, errors.New("审阅清单包含无效或重复的文件变更")
		}
	}
	r.Revision = digest(data)
	state, err := readPrivate(fd, ".local-authorization.json", 512<<10)
	if err == nil {
		if json.Unmarshal(state, &b) != nil || b.Revision != r.Revision || !filepath.IsAbs(b.Source) || filepath.Clean(b.Source) != b.Source {
			return nil, r, b, errors.New("审阅清单与本机授权不匹配")
		}
		r.CanApply = r.ExitCode == 0
	} else if !errors.Is(err, unix.ENOENT) {
		return nil, r, b, err
	}
	ok = true
	return f, r, b, nil
}

func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func readPrivate(fd int, name string, limit int) ([]byte, error) {
	f, st, err := openChecked(fd, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st.Uid != uint32(os.Getuid()) || st.Mode&0o077 != 0 {
		return nil, errors.New("审阅产物必须仅本机用户可访问")
	}
	return readVerified(f, st, limit)
}

func readVerified(f *os.File, st unix.Stat_t, limit int) ([]byte, error) {
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Size > int64(limit) {
		return nil, errors.New("文件类型不支持或大小超出上限")
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	var now unix.Stat_t
	if len(data) > limit || int64(len(data)) != st.Size || unix.Fstat(int(f.Fd()), &now) != nil || !same(st, now) {
		return nil, errors.New("读取期间文件发生变化")
	}
	return data, nil
}

// ApplyReview applies exactly one owner-approved file from one completed run.
// It does not grant new authorization to a guest or accept a destination.
func ApplyReview(ctx context.Context, dir, revision, target string) (Applied, error) {
	return applyReview(ctx, dir, revision, target, nil)
}

// beforeCommit is an internal test seam for exercising exact race windows.
func applyReview(ctx context.Context, dir, revision, target string, beforeCommit func()) (Applied, error) {
	var result Applied
	if !validDigest(revision) || !relative(target) {
		return result, errors.New("需要确认审阅版本和一个变更的精确相对路径")
	}
	rf, r, b, err := openReview(dir)
	if err != nil {
		return result, err
	}
	defer rf.Close()
	if err := unix.Flock(int(rf.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return result, errors.New("同一次运行已有写回在进行，请稍后再试")
	}
	// Refresh private authorization after locking. Another reviewed addition
	// could have just created a directory in the same report.
	state, stateErr := readPrivate(int(rf.Fd()), ".local-authorization.json", 512<<10)
	if stateErr != nil || json.Unmarshal(state, &b) != nil || b.Revision != r.Revision {
		return result, errors.New("没有匹配的本机写回授权")
	}
	if r.Revision != revision || !r.CanApply {
		return result, errors.New("此运行没有可写回的本机授权，或审阅版本已改变")
	}
	change, err := pickChange(r, target)
	if err != nil {
		return result, err
	}
	var data []byte
	if change.AfterFile != "" {
		data, err = verifiedArtifact(int(rf.Fd()), change.AfterFile, change.After)
		if err != nil {
			return result, err
		}
	}
	p, err := resolveChange(b, change)
	if err != nil {
		return result, err
	}
	defer p.dir.Close()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result = Applied{Path: change.Path, Operation: change.Operation}
	fd := int(p.dir.Fd())
	stage := ".sous-writeback-" + strings.ToLower(rand.Text()[:20])
	if change.Operation == "added" {
		dirs, finish, err := stageAddition(fd, stage, p.tail, data)
		if err != nil {
			return Applied{}, err
		}
		installed := false
		defer func() { finish(installed) }()
		if beforeCommit != nil {
			beforeCommit()
		}
		if err := p.confirm(ctx, b, change); err != nil {
			return Applied{}, err
		}
		if err := unix.RenameatxNp(fd, stage, fd, p.name, unix.RENAME_EXCL|unix.RENAME_NOFOLLOW_ANY); err != nil {
			return Applied{}, fmt.Errorf("不能新建 %s，可能已有本机文件或目录：%w", target, err)
		}
		installed = true
		if len(dirs) > 0 {
			if b.Directories == nil {
				b.Directories = map[string]identity{}
			}
			name := path.Join(p.prefix, p.name)
			for i, id := range dirs {
				if i > 0 {
					name = path.Join(name, p.tail[i-1])
				}
				b.Directories[name] = id
			}
			raw, err := json.Marshal(b)
			if err == nil {
				err = replacePrivate(int(rf.Fd()), ".local-authorization.json", raw)
			}
			if err != nil {
				return result, fmt.Errorf("已新建文件，但无法记录新目录授权；请重新运行后再写入同目录：%w", err)
			}
		}
		return result, nil
	}

	original, st, err := openChecked(fd, p.name)
	if err != nil {
		return Applied{}, err
	}
	defer original.Close()
	if st.Mode&(unix.S_ISUID|unix.S_ISGID) != 0 {
		return Applied{}, errors.New("不写回设置了提权位的文件")
	}
	baseline, err := readVerified(original, st, MaxFile)
	if err != nil || fileIdentity(st) != b.Files[target] || digest(baseline) != change.Before {
		return Applied{}, errors.New("本机文件已变化，不会覆盖或移除，请重新运行并审阅")
	}
	backup := stage + ".original"
	// Record the original's possible locations before any namespace change.
	// A host crash can interrupt the rename but never leaves an original
	// without a durable mapping back to its report and approved path.
	ticketName := "writeback-" + strings.TrimPrefix(stage, ".sous-writeback-") + ".json"
	ticket, err := json.Marshal(struct {
		Path      string `json:"path"`
		Operation string `json:"operation"`
		Staging   string `json:"possible_staged_original"`
		Backup    string `json:"local_backup"`
	}{change.Path, change.Operation, p.relative(stage), p.relative(backup)})
	if err != nil {
		return Applied{}, err
	}
	reportFD := int(rf.Fd())
	if err := createNew(reportFD, ticketName, ticket); err != nil {
		return Applied{}, err
	}
	if err := unix.Fsync(reportFD); err != nil {
		_ = unix.Unlinkat(reportFD, ticketName, 0)
		return Applied{}, err
	}
	keepTicket := false
	defer func() {
		if !keepTicket {
			_ = unix.Unlinkat(reportFD, ticketName, 0)
		}
	}()
	if change.Operation == "modified" {
		// Clone by descriptor, preserving local permissions, ACL and extended
		// attributes instead of copying mode or metadata out of the Guest.
		if err := unix.Fclonefileat(int(original.Fd()), fd, stage, 0); err != nil {
			return Applied{}, fmt.Errorf("此文件系统不支持安全替换并保留元数据：%w", err)
		}
		// Before atomic exchange this temp entry is only our staged copy.
		// Afterwards it is the displaced inode and must never be blindly unlinked.
		displaced := false
		defer func() {
			if !displaced {
				_ = unix.Unlinkat(fd, stage, 0)
			}
		}()
		if err := overwriteStaged(fd, stage, data); err != nil {
			return Applied{}, err
		}
		if beforeCommit != nil {
			beforeCommit()
		}
		if err := p.confirm(ctx, b, change); err != nil {
			return Applied{}, err
		}
		if err := verifyEntry(fd, p.name, st); err != nil {
			return Applied{}, errors.New("本机文件已变化，未应用")
		}
		if err := unix.RenameatxNp(fd, stage, fd, p.name, unix.RENAME_SWAP|unix.RENAME_NOFOLLOW_ANY); err != nil {
			return Applied{}, err
		}
		displaced = true
		keepTicket = true
		// Keep displaced data even if an editor held the original open or if
		// checking its bytes after the exchange fails. No host data is lost.
		if !matchesBaseline(fd, stage, st, change.Before) {
			result.Backup = p.relative(stage)
			return result, fmt.Errorf("替换时检测到并发修改，原件仍在 %s；请先核对，不会自动覆盖第二次", result.Backup)
		}
		if err := unix.RenameatxNp(fd, stage, fd, backup, unix.RENAME_EXCL|unix.RENAME_NOFOLLOW_ANY); err != nil {
			result.Backup = p.relative(stage)
			return result, fmt.Errorf("已替换，原件保留在 %s：%w", result.Backup, err)
		}
	} else {
		if beforeCommit != nil {
			beforeCommit()
		}
		if err := p.confirm(ctx, b, change); err != nil {
			return Applied{}, err
		}
		if err := verifyEntry(fd, p.name, st); err != nil {
			return Applied{}, errors.New("本机文件已变化，未移除")
		}
		// A reviewed deletion is a move, never destruction of the original.
		if err := unix.RenameatxNp(fd, p.name, fd, backup, unix.RENAME_EXCL|unix.RENAME_NOFOLLOW_ANY); err != nil {
			return Applied{}, err
		}
		keepTicket = true
		if !matchesBaseline(fd, backup, st, change.Before) {
			if err := unix.RenameatxNp(fd, backup, fd, p.name, unix.RENAME_EXCL|unix.RENAME_NOFOLLOW_ANY); err == nil {
				keepTicket = false
				return Applied{}, errors.New("移除时检测到并发修改，已恢复原文件")
			}
			result.Backup = p.relative(backup)
			return result, fmt.Errorf("移除时检测到并发修改且目标已被占用；原件仍在 %s", result.Backup)
		}
	}
	result.Backup = p.relative(backup)
	return result, nil
}

func matchesBaseline(fd int, name string, saved unix.Stat_t, hash string) bool {
	f, st, err := openChecked(fd, name)
	if err != nil {
		return false
	}
	defer f.Close()
	b, err := readVerified(f, st, MaxFile)
	return err == nil && fileIdentity(st) == fileIdentity(saved) && st.Mode == saved.Mode && st.Mtim == saved.Mtim && digest(b) == hash
}

func overwriteStaged(fd int, name string, data []byte) error {
	fresh, err := unix.Openat(fd, name, unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fresh), name)
	defer f.Close()
	var st unix.Stat_t
	if err := unix.Fstat(fresh, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		return errors.New("临时文件已变化")
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

// stageAddition constructs an absent subtree privately, then its caller uses
// rename-excl to publish it without touching any pre-existing entry.
func stageAddition(fd int, stage string, tail []string, data []byte) ([]identity, func(bool), error) {
	if len(tail) == 0 {
		if err := createNew(fd, stage, data); err != nil {
			return nil, nil, err
		}
		return nil, func(success bool) {
			if !success {
				_ = unix.Unlinkat(fd, stage, 0)
			}
		}, nil
	}
	type stagedDir struct {
		fd, parent int
		name       string
	}
	var staged []stagedDir
	var ids []identity
	finish := func(success bool) {
		for i := len(staged) - 1; i >= 0; i-- {
			d := staged[i]
			if !success {
				if i == len(tail)-1 && d.fd >= 0 {
					_ = unix.Unlinkat(d.fd, tail[len(tail)-1], 0)
				}
				_ = unix.Unlinkat(d.parent, d.name, unix.AT_REMOVEDIR)
			}
			if d.fd >= 0 {
				_ = unix.Close(d.fd)
			}
		}
	}
	for i, name := range append([]string{stage}, tail[:len(tail)-1]...) {
		if err := unix.Mkdirat(fd, name, 0o700); err != nil {
			finish(false)
			return nil, nil, err
		}
		staged = append(staged, stagedDir{fd: -1, parent: fd, name: name})
		child, err := unix.Openat(fd, name, safeOpen|unix.O_DIRECTORY, 0)
		if err != nil {
			finish(false)
			return nil, nil, err
		}
		staged[i].fd = child
		var stat unix.Stat_t
		if err := unix.Fstat(child, &stat); err != nil {
			finish(false)
			return nil, nil, err
		}
		ids = append(ids, fileIdentity(stat))
		fd = child
	}
	if err := createNew(fd, tail[len(tail)-1], data); err != nil {
		finish(false)
		return nil, nil, err
	}
	return ids, finish, nil
}

func replacePrivate(fd int, name string, data []byte) error {
	temp := ".pending-" + strings.ToLower(rand.Text()[:20])
	if err := createNew(fd, temp, data); err != nil {
		return err
	}
	defer unix.Unlinkat(fd, temp, 0)
	return unix.RenameatxNp(fd, temp, fd, name, unix.RENAME_NOFOLLOW_ANY)
}

type destination struct {
	dir          *os.File
	id           identity
	prefix, name string
	tail         []string
}

func (p destination) relative(name string) string { return path.Join(p.prefix, name) }

func (p destination) confirm(ctx context.Context, b binding, c Change) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now, err := resolveChange(b, c)
	if err != nil {
		return err
	}
	defer now.dir.Close()
	if p.id != now.id || p.name != now.name || p.prefix != now.prefix || len(p.tail) != len(now.tail) {
		return errors.New("原项目中的目标位置已变化，未应用")
	}
	return nil
}

func resolveChange(b binding, c Change) (destination, error) {
	var out destination
	if c.Operation != "added" && b.Files[c.Path].Inode == 0 {
		return out, errors.New("不在这次运行导入的文件范围内")
	}
	parent, name, err := openParent(b.Source)
	if err != nil {
		return out, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = parent.Close()
		}
	}()
	f, st, err := openChecked(int(parent.Fd()), name)
	if err != nil {
		return out, err
	}
	if fileIdentity(st) != b.Root || (st.Mode&unix.S_IFMT == unix.S_IFDIR) != b.Directory {
		_ = f.Close()
		return out, errors.New("原本授权的文件或目录已被替换")
	}
	if !b.Directory {
		_ = f.Close()
		if c.Operation == "added" || c.Path != name {
			return out, errors.New("单文件授权不可写回其他文件")
		}
		var pst unix.Stat_t
		if err := unix.Fstat(int(parent.Fd()), &pst); err != nil {
			return out, err
		}
		keep = true
		return destination{dir: parent, id: fileIdentity(pst), name: name}, nil
	}
	_ = parent.Close()
	parent = f
	prefix := ""
	parts := strings.Split(c.Path, "/")
	for i, part := range parts[:len(parts)-1] {
		next := path.Join(prefix, part)
		child, s, err := openChecked(int(parent.Fd()), part)
		if errors.Is(err, unix.ENOENT) && c.Operation == "added" && b.Directories[next].Inode == 0 {
			keep = true
			return destination{dir: parent, id: fileIdentity(st), prefix: prefix, name: part, tail: parts[i+1:]}, nil
		}
		if err != nil {
			return out, err
		}
		if s.Mode&unix.S_IFMT != unix.S_IFDIR || s.Dev != b.Root.Device || fileIdentity(s) != b.Directories[next] {
			_ = child.Close()
			return out, errors.New("目标目录未获授权、发生替换或跨越挂载点")
		}
		_ = parent.Close()
		parent, st, prefix = child, s, next
	}
	name = parts[len(parts)-1]
	if c.Operation == "added" {
		var existing unix.Stat_t
		if err := unix.Fstatat(int(parent.Fd()), name, &existing, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(err, unix.ENOENT) {
			return out, errors.New("该路径已出现本机文件或目录，不覆盖")
		}
	}
	keep = true
	return destination{dir: parent, id: fileIdentity(st), prefix: prefix, name: name}, nil
}
