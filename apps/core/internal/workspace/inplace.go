package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
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
	if _, err := os.Stat(filepath.Join(p.Shadow, "HEAD")); err == nil {
		return nil
	}
	if err := os.MkdirAll(p.Shadow, 0o700); err != nil {
		return err
	}
	if _, err := p.git(ctx, "init", "-q"); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(p.Shadow, "info"), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(p.Shadow, "info", "exclude"), []byte(strings.Join(shadowExclude, "\n")+"\n"), 0o600)
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

// Restore puts an in-place task's directory back to its snapshot: changed
// files get their old content, files the task added are deleted. Ignored
// files (dependencies, build output) are left alone.
func Restore(ctx context.Context, root, dir, base string) (changed string, err error) {
	p, err := PrepareInPlace(dir)
	if err != nil {
		return "", err
	}
	p.Shadow, p.Base = ShadowDir(root, p.Dir), base
	if base == "" {
		return "", errors.New("这个任务没有快照，无法撤销")
	}
	if _, err := os.Stat(filepath.Join(p.Shadow, "HEAD")); err != nil {
		return "", errors.New("找不到这个目录的快照仓库，无法撤销")
	}
	if _, err := p.git(ctx, "cat-file", "-e", base+"^{commit}"); err != nil {
		return "", fmt.Errorf("快照 %s 已经不在了", short(base))
	}
	if _, err := p.git(ctx, "add", "-A"); err != nil {
		return "", err
	}
	stat, _ := p.git(ctx, "diff", "--cached", "--shortstat", base)
	if _, err := p.git(ctx, "reset", "-q", "--hard", base); err != nil {
		return "", err
	}
	return strings.TrimSpace(stat), nil
}

// CommitInPlace commits what an in-place task changed in a git checkout, on
// the branch it has checked out: only the files that differ from the task's
// snapshot, so the user's own changes elsewhere stay uncommitted (and
// whatever else the user staged stays staged).
func CommitInPlace(ctx context.Context, root, dir, base, message string) (proto.GitResult, error) {
	res := proto.GitResult{Op: proto.GitCommit}
	top, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return res, fmt.Errorf("%s 不是 git 仓库，没有可以提交的地方", dir)
	}
	branch, _ := git(ctx, top, "rev-parse", "--abbrev-ref", "HEAD")
	if branch == "" || branch == "HEAD" {
		return res, errors.New("这个目录的 HEAD 是 detached，不在任何分支上；先切到要提交的分支")
	}
	res.Branch = branch
	p, err := PrepareInPlace(dir)
	if err != nil {
		return res, err
	}
	p.Shadow, p.Base = ShadowDir(root, p.Dir), base
	if base == "" {
		return res, errors.New("这个任务没有快照，分不清哪些是它的改动")
	}
	if _, err := p.git(ctx, "add", "-A"); err != nil {
		return res, err
	}
	out, err := p.git(ctx, "diff", "--cached", "--name-only", "--no-renames", base)
	if err != nil {
		return res, err
	}
	var files []string
	for _, f := range strings.Split(out, "\n") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
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
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = filepath.ToSlash(filepath.Join(rel, f))
	}
	res.Before, _ = git(ctx, top, "rev-parse", "HEAD")
	if _, err := git(ctx, top, append([]string{"add", "-A", "--"}, paths...)...); err != nil {
		return res, fmt.Errorf("暂存任务的改动失败：%w", err)
	}
	pr := Prepared{Dir: top}
	args := append([]string{"-c", "user.name=" + authorName(ctx, pr), "-c", "user.email=" + authorEmail(ctx, pr), "commit", "-q", "-m", message, "--"}, paths...)
	if _, err := git(ctx, top, args...); err != nil {
		return res, fmt.Errorf("提交失败：%w", err)
	}
	res.After, _ = git(ctx, top, "rev-parse", "HEAD")
	res.Commits = 1
	res.Message = fmt.Sprintf("已在 %s 上提交任务的改动（%d 个文件），没有推送", branch, len(files))
	return res, nil
}
