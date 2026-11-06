package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// In-place tasks (decision 0022) work in a project's directory itself. Before
// the first run sous takes a snapshot in a shadow git repository kept under
// the node's home (root/shadow/<hash of the directory>), with the directory
// as its work tree. The project directory gets no .git of its own and the
// user's repository, when there is one, is never touched: its index, its
// branches and its stash stay as they are. The snapshot is the base of the
// task's diff and what an undo restores.

// shadowExclude keeps dependency and cache directories out of snapshots.
var shadowExclude = []string{"node_modules/", ".venv/", "venv/", "__pycache__/", ".DS_Store", ".sous-shadow/"}

// ShadowDir is the shadow repository of a directory.
func ShadowDir(root, dir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(dir)))
	return filepath.Join(root, "shadow", hex.EncodeToString(sum[:8]))
}

// PrepareInPlaceTask returns the working directory of an in-place task: dir
// itself, with Base set to the task's snapshot. The first call for a key
// takes the snapshot; later runs of the same task keep it, so the diff
// always covers the whole task.
func PrepareInPlaceTask(ctx context.Context, root, dir, key string) (Prepared, error) {
	p, err := PrepareInPlace(dir)
	if err != nil {
		return Prepared{}, err
	}
	p.Shadow = ShadowDir(root, p.Dir)
	if err := initShadow(ctx, p); err != nil {
		return Prepared{}, fmt.Errorf("准备快照失败：%w", err)
	}
	ref := "refs/sous/" + ShortID(key)
	if base, err := p.git(ctx, "rev-parse", "--verify", "--quiet", ref); err == nil && base != "" {
		p.Base = base
		return p, nil
	}
	base, err := snapshot(ctx, p, "sous: 任务开始前的快照 "+key)
	if err != nil {
		return Prepared{}, fmt.Errorf("拍快照失败：%w", err)
	}
	if _, err := p.git(ctx, "update-ref", ref, base); err != nil {
		return Prepared{}, err
	}
	p.Base = base
	p.Note = "已在改动前给 " + p.Dir + " 拍了快照，可以随时看改动、撤销"
	return p, nil
}

func initShadow(ctx context.Context, p Prepared) error {
	if _, err := os.Stat(filepath.Join(p.Shadow, "HEAD")); err != nil {
		if err := os.MkdirAll(p.Shadow, 0o700); err != nil {
			return err
		}
		if _, err := p.git(ctx, "init", "-q"); err != nil {
			return err
		}
	}
	info := filepath.Join(p.Shadow, "info")
	if err := os.MkdirAll(info, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(info, "exclude"), []byte(strings.Join(shadowExclude, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	// Snapshots keep the bytes they were taken from: without this, git
	// converts line endings on checkout (core.autocrlf on Windows) and an
	// undo would put back different files than the ones the task found.
	// info/attributes wins over the project's own .gitattributes.
	return os.WriteFile(filepath.Join(info, "attributes"), []byte("* -text\n"), 0o600)
}

// snapshot records the directory as it is now and returns the commit.
func snapshot(ctx context.Context, p Prepared, message string) (string, error) {
	if _, err := p.git(ctx, "add", "-A"); err != nil {
		return "", err
	}
	tree, err := p.git(ctx, "write-tree")
	if err != nil {
		return "", err
	}
	return p.git(ctx, "-c", "user.name=sous", "-c", "user.email=sous@localhost", "commit-tree", tree, "-m", message)
}

// endRef names the snapshot taken when the task's agent last finished.
func endRef(key string) string { return "refs/sous/" + ShortID(key) + "-end" }

// MarkEnd snapshots an in-place task's directory as its agent left it. The
// files that differ between the start and this snapshot are the task's; an
// undo or a commit touches only those, and only as the task left them.
func MarkEnd(ctx context.Context, p Prepared, key string) error {
	if p.Shadow == "" || p.Base == "" {
		return nil
	}
	end, err := snapshot(ctx, p, "sous: 任务结束时的快照 "+key)
	if err != nil {
		return err
	}
	_, err = p.git(ctx, "update-ref", endRef(key), end)
	return err
}

// taskChanges opens the shadow repository of an in-place task and lists the
// files the task changed: between its start snapshot and its end snapshot
// (or the directory as it is now, for a task from before end snapshots).
func taskChanges(ctx context.Context, root, dir, base, key string) (Prepared, string, []string, error) {
	p, err := PrepareInPlace(dir)
	if err != nil {
		return p, "", nil, err
	}
	p.Shadow, p.Base = ShadowDir(root, p.Dir), base
	if base == "" {
		return p, "", nil, errors.New("这个任务没有快照，分不清哪些是它的改动")
	}
	if _, err := os.Stat(filepath.Join(p.Shadow, "HEAD")); err != nil {
		return p, "", nil, errors.New("找不到这个目录的快照仓库")
	}
	if _, err := p.git(ctx, "cat-file", "-e", base+"^{commit}"); err != nil {
		return p, "", nil, fmt.Errorf("快照 %s 已经不在了", short(base))
	}
	end := ""
	if key != "" {
		end, _ = p.git(ctx, "rev-parse", "--verify", "--quiet", endRef(key))
	}
	if end == "" {
		if end, err = snapshot(ctx, p, "sous: 现在的目录"); err != nil {
			return p, "", nil, err
		}
	}
	out, err := p.git(ctx, "diff", "--name-only", "--no-renames", "-z", base, end)
	if err != nil {
		return p, "", nil, err
	}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	return p, end, files, nil
}

// blobAt is the object of path in a commit or tree of the shadow repository,
// or "" when it has no such file.
func blobAt(ctx context.Context, p Prepared, rev, path string) string {
	sha, err := p.git(ctx, "rev-parse", "--verify", "--quiet", rev+":"+path)
	if err != nil {
		return ""
	}
	return sha
}

// Restore undoes an in-place task: the files it changed get the content
// they had before it, the files it added are deleted. Nothing else is
// touched: files the user added or changed meanwhile stay, and a file the
// user changed again after the task finished is left alone and listed, so
// no later work is lost (end-to-end review H1).
func Restore(ctx context.Context, root, dir, base, key string) (string, error) {
	p, end, files, err := taskChanges(ctx, root, dir, base, key)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "任务没有留下改动，没有可以撤销的", nil
	}
	if _, err := p.git(ctx, "add", "-A"); err != nil {
		return "", err
	}
	now, err := p.git(ctx, "write-tree")
	if err != nil {
		return "", err
	}
	var restored, removed int
	var kept []string
	for _, f := range files {
		if blobAt(ctx, p, end, f) != blobAt(ctx, p, now, f) {
			kept = append(kept, f)
			continue
		}
		if blobAt(ctx, p, base, f) != "" {
			if _, err := p.git(ctx, "checkout", base, "--", f); err != nil {
				return "", fmt.Errorf("恢复 %s 失败：%w", f, err)
			}
			restored++
			continue
		}
		abs := filepath.Join(p.Dir, filepath.FromSlash(f))
		if rel, err := filepath.Rel(p.Dir, abs); err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("删除 %s 失败：%w", f, err)
		}
		removed++
	}
	msg := fmt.Sprintf("恢复了 %d 个文件，删除了任务新增的 %d 个文件", restored, removed)
	if len(kept) > 0 {
		msg += fmt.Sprintf("；%d 个文件在任务结束后又被改过，没有动：%s", len(kept), strings.Join(kept, "、"))
	}
	return msg, nil
}

// CommitInPlace commits what an in-place task changed in a git checkout, on
// the branch it has checked out: only the files the task changed, with the
// content the task left them in. The user's own changes, elsewhere or made
// to the same files after the task, stay uncommitted in the work tree, and
// whatever else the user staged stays staged (end-to-end review M3).
func CommitInPlace(ctx context.Context, root, dir, base, key, message string) (proto.GitResult, error) {
	res := proto.GitResult{Op: proto.GitCommit}
	top, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return res, fmt.Errorf("%s 不是 git 仓库，没有可以提交的地方", dir)
	}
	branch, _ := git(ctx, top, "symbolic-ref", "--short", "-q", "HEAD")
	if branch == "" {
		return res, errors.New("这个目录的 HEAD 是 detached，不在任何分支上；先切到要提交的分支")
	}
	res.Branch = branch
	p, end, files, err := taskChanges(ctx, root, dir, base, key)
	if err != nil {
		return res, err
	}
	if len(files) == 0 {
		res.Message = "这个任务没有留下改动，没有可以提交的东西"
		return res, nil
	}
	// The shadow repository's work tree is dir; the paths are relative to it
	// (both resolved: /var and /private/var are the same place on macOS).
	realDir, err := filepath.EvalSymlinks(p.Dir)
	if err != nil {
		realDir = p.Dir
	}
	realTop, err := filepath.EvalSymlinks(top)
	if err != nil {
		realTop = top
	}
	rel, err := filepath.Rel(realTop, realDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return res, fmt.Errorf("%s 不在仓库 %s 里", p.Dir, top)
	}
	res.Before, _ = git(ctx, top, "rev-parse", "--verify", "--quiet", "HEAD")

	// Build the commit in a private index, so the user's index is not used.
	tmp, err := os.CreateTemp("", "sous-index-*")
	if err != nil {
		return res, err
	}
	tmp.Close()
	os.Remove(tmp.Name()) // git wants to create the index itself
	defer os.Remove(tmp.Name())
	env := []string{"GIT_INDEX_FILE=" + tmp.Name()}
	if res.Before != "" {
		_, err = gitRaw(ctx, top, env, nil, "read-tree", "HEAD")
	} else {
		_, err = gitRaw(ctx, top, env, nil, "read-tree", "--empty")
	}
	if err != nil {
		return res, fmt.Errorf("准备提交失败：%w", err)
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		path := filepath.ToSlash(filepath.Join(rel, f))
		paths = append(paths, path)
		if blobAt(ctx, p, end, f) == "" {
			if _, err := gitRaw(ctx, top, env, nil, "update-index", "--force-remove", "--", path); err != nil {
				return res, fmt.Errorf("暂存 %s 失败：%w", f, err)
			}
			continue
		}
		content, err := shadowRaw(ctx, p, "cat-file", "blob", end+":"+f)
		if err != nil {
			return res, fmt.Errorf("读取 %s 失败：%w", f, err)
		}
		mode := "100644"
		if line, err := p.git(ctx, "ls-tree", end, "--", f); err == nil && len(line) > 6 {
			mode = line[:6]
		}
		blob, err := gitRaw(ctx, top, nil, content, "hash-object", "-w", "--stdin")
		if err != nil {
			return res, fmt.Errorf("写入 %s 失败：%w", f, err)
		}
		if _, err := gitRaw(ctx, top, env, nil, "update-index", "--add", "--cacheinfo", mode+","+strings.TrimSpace(string(blob))+","+path); err != nil {
			return res, fmt.Errorf("暂存 %s 失败：%w", f, err)
		}
	}
	tree, err := gitRaw(ctx, top, env, nil, "write-tree")
	if err != nil {
		return res, err
	}
	treeID := strings.TrimSpace(string(tree))
	if res.Before != "" {
		if cur, _ := git(ctx, top, "rev-parse", "HEAD^{tree}"); cur == treeID {
			res.Message = "任务的改动已经都在 " + branch + " 上了，没有可以提交的东西"
			return res, nil
		}
	}
	pr := Prepared{Dir: top}
	args := []string{"-c", "user.name=" + authorName(ctx, pr), "-c", "user.email=" + authorEmail(ctx, pr), "commit-tree", treeID, "-m", message}
	if res.Before != "" {
		args = append(args, "-p", res.Before)
	}
	commit, err := git(ctx, top, args...)
	if err != nil {
		return res, fmt.Errorf("提交失败：%w", err)
	}
	// Move the branch only if nobody moved it meanwhile.
	if _, err := git(ctx, top, "update-ref", "-m", "sous: "+message, "refs/heads/"+branch, commit, res.Before); err != nil {
		return res, fmt.Errorf("提交失败：%w", err)
	}
	// The user's index follows the new commit for these paths; the work tree
	// keeps whatever the user changed since.
	if _, err := git(ctx, top, append([]string{"reset", "-q", "--"}, paths...)...); err != nil {
		slog.Warn("workspace: reset the index after an in-place commit", "err", err)
	}
	res.After = commit
	res.Commits = 1
	res.Message = fmt.Sprintf("已在 %s 上提交任务的改动（%d 个文件），没有推送", branch, len(files))
	return res, nil
}

// gitRaw runs git with extra environment and input and returns its raw
// output (file contents keep their bytes).
func gitRaw(ctx context.Context, dir string, env []string, stdin []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, err
	}
	return out, nil
}

// shadowRaw is gitRaw in an in-place task's shadow repository.
func shadowRaw(ctx context.Context, p Prepared, args ...string) ([]byte, error) {
	return gitRaw(ctx, p.Dir, []string{"GIT_DIR=" + p.Shadow, "GIT_WORK_TREE=" + p.Dir}, nil, args...)
}
