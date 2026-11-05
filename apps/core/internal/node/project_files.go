package node

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// Browsing and editing a project's files from the page. The Hub names a
// directory (root) and a path under it (rel); the machine only serves roots
// inside its workspaces, never leaves the root (symlinks included), and
// never touches .git.

func (a *admin) projectOp(op string, args proto.ProjectArgs) (any, error) {
	_, abs, rel, err := a.projectPath(args)
	if err != nil {
		return nil, err
	}
	switch op {
	case proto.OpProjectLs:
		return listDir(abs, rel)
	case proto.OpProjectRead:
		return readProjectFile(abs, rel)
	}
	if rel == "" {
		return nil, nodeErr(proto.ErrCodeInvalid, "没有指定文件")
	}
	if len(args.Content) > maxFileBytes {
		return nil, nodeErr(proto.ErrCodeInvalid, "内容超过 1 MB")
	}
	a.fileMu.Lock()
	defer a.fileMu.Unlock()
	cur, err := readProjectFile(abs, rel)
	if err != nil {
		var ne *proto.NodeError
		if !(errors.As(err, &ne) && ne.Code == proto.ErrCodeNotFound) {
			return nil, err
		}
		cur = proto.ProjectFile{}
	}
	if cur.Binary {
		return nil, nodeErr(proto.ErrCodeInvalid, "二进制文件不能在页面上编辑")
	}
	if cur.SHA256 != args.BaseSHA256 {
		return nil, nodeErr(proto.ErrCodeConflict, "文件在你打开之后被改过了，请重新加载再改")
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(abs); err == nil {
		mode = fi.Mode().Perm()
	} else if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(abs, []byte(args.Content), mode); err != nil {
		return nil, err
	}
	return readProjectFile(abs, rel)
}

// projectPath validates the request and returns the absolute path.
func (a *admin) projectPath(args proto.ProjectArgs) (root, abs, rel string, err error) {
	root, err = filepath.EvalSymlinks(ExpandPath(args.Root))
	if err != nil || args.Root == "" {
		return "", "", "", nodeErr(proto.ErrCodeNotFound, "项目目录不存在")
	}
	inside := false
	for _, w := range a.d.conf().WorkspaceDirs() {
		if w, err := filepath.EvalSymlinks(w); err == nil && within(w, root) && w != root {
			inside = true
		}
	}
	if !inside {
		return "", "", "", nodeErr(proto.ErrCodeInvalid, "这个目录不在这台机器的工作空间里")
	}
	// Only a project's own checkout: a directory that happens to sit in a
	// workspace (~/.ssh when the workspace is the home directory) is not one.
	if _, err := os.Lstat(filepath.Join(root, ".git")); err != nil {
		return "", "", "", nodeErr(proto.ErrCodeInvalid, "这个目录不是 git 仓库")
	}
	rel = strings.Trim(filepath.ToSlash(filepath.Clean("/"+filepath.FromSlash(args.Rel))), "/")
	if rel == "." {
		rel = ""
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".git" {
			return "", "", "", nodeErr(proto.ErrCodeInvalid, ".git 里的文件不能在页面上看")
		}
	}
	abs = filepath.Join(root, filepath.FromSlash(rel))
	// Resolve what exists, so a symlink cannot lead out of the root.
	probe := abs
	for {
		if resolved, e := filepath.EvalSymlinks(probe); e == nil {
			if !within(root, resolved) {
				return "", "", "", nodeErr(proto.ErrCodeInvalid, "路径超出了项目目录")
			}
			break
		}
		next := filepath.Dir(probe)
		if next == probe {
			break
		}
		probe = next
	}
	return root, abs, rel, nil
}

func within(dir, p string) bool {
	r, err := filepath.Rel(dir, p)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

func listDir(abs, rel string) (proto.ProjectDir, error) {
	ents, err := os.ReadDir(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return proto.ProjectDir{}, nodeErr(proto.ErrCodeNotFound, "目录不存在")
	}
	if err != nil {
		return proto.ProjectDir{}, err
	}
	out := proto.ProjectDir{Rel: rel, Entries: []proto.ProjectEntry{}}
	for _, e := range ents {
		if e.Name() == ".git" {
			continue
		}
		fi, err := os.Stat(filepath.Join(abs, e.Name())) // follows symlinks
		if err != nil {
			continue
		}
		out.Entries = append(out.Entries, proto.ProjectEntry{Name: e.Name(), Dir: fi.IsDir(), Size: fi.Size()})
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		x, y := out.Entries[i], out.Entries[j]
		if x.Dir != y.Dir {
			return x.Dir
		}
		return strings.ToLower(x.Name) < strings.ToLower(y.Name)
	})
	return out, nil
}

func readProjectFile(abs, rel string) (proto.ProjectFile, error) {
	fi, err := os.Stat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return proto.ProjectFile{}, nodeErr(proto.ErrCodeNotFound, "文件不存在")
	}
	if err != nil {
		return proto.ProjectFile{}, err
	}
	if fi.IsDir() {
		return proto.ProjectFile{}, nodeErr(proto.ErrCodeInvalid, "%s 是一个目录", rel)
	}
	out := proto.ProjectFile{Rel: rel, Size: fi.Size()}
	if fi.Size() > maxFileBytes {
		return out, nodeErr(proto.ErrCodeInvalid, "%s 超过 1 MB，不能在页面上看", rel)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return proto.ProjectFile{}, err
	}
	out.SHA256 = digest(raw)
	if bytes.IndexByte(raw, 0) >= 0 || !utf8.Valid(raw) {
		out.Binary = true
		switch strings.ToLower(filepath.Ext(rel)) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".avif":
			out.Data = raw
		}
		return out, nil
	}
	out.Content = string(raw)
	return out, nil
}
