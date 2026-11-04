package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// ScanDepth is how many directory levels below a workspace are searched.
const ScanDepth = 2

// skipDirs are never searched for repositories.
var skipDirs = map[string]bool{"node_modules": true, "vendor": true, "target": true, "dist": true, "build": true}

// Scan finds the git repositories in the workspaces, at most ScanDepth
// levels down. It does not look inside a repository, hidden directories,
// symbolic links or dependency directories. A workspace that does not exist
// is skipped.
func Scan(ctx context.Context, workspaces []string) []proto.Repo {
	var out []proto.Repo
	seen := map[string]bool{}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if ctx.Err() != nil || depth > ScanDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") || skipDirs[name] || e.Type()&os.ModeSymlink != 0 {
				continue
			}
			p := filepath.Join(dir, name)
			if isRepo(p) {
				if !seen[p] {
					seen[p] = true
					out = append(out, Describe(ctx, p))
				}
				continue
			}
			walk(p, depth+1)
		}
	}
	for _, ws := range workspaces {
		if isRepo(ws) && !seen[ws] {
			seen[ws] = true
			out = append(out, Describe(ctx, ws))
			continue
		}
		walk(ws, 1)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func isRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// Describe reads a repository's remote, branch, whether it has
// uncommitted changes, and whether it is a wokspace.
func Describe(ctx context.Context, dir string) proto.Repo {
	r := proto.Repo{Path: dir, Wokspace: IsWokspace(dir)}
	if out, err := git(ctx, dir, "remote"); err == nil {
		remotes := strings.Fields(out)
		name := ""
		for _, n := range remotes {
			if n == "origin" {
				name = n
			}
		}
		if name == "" && len(remotes) > 0 {
			name = remotes[0]
		}
		if name != "" {
			// The configured URL, before any insteadOf rewriting: what identifies the project.
			if url, err := git(ctx, dir, "config", "--get", "remote."+name+".url"); err == nil {
				r.Remote = strings.TrimSpace(url)
			}
		}
	}
	r.Branch, r.Git = State(ctx, dir)
	r.Dirty = r.Git != nil && r.Git.Changes+r.Git.Untracked > 0
	if r.Branch == "" {
		if b, err := git(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
			r.Branch = strings.TrimSpace(b)
		}
	}
	return r
}

// State reads a checkout's branch and where it stands against its upstream,
// from one `git status`, without network access. Nil when git fails.
func State(ctx context.Context, dir string) (branch string, st *proto.GitState) {
	out, err := git(ctx, dir, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return "", nil
	}
	st = &proto.GitState{}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.oid "):
			if oid := strings.TrimPrefix(line, "# branch.oid "); oid != "(initial)" {
				st.Head = oid
			}
		case strings.HasPrefix(line, "# branch.head "):
			if h := strings.TrimPrefix(line, "# branch.head "); h != "(detached)" {
				branch = h
			} else {
				branch = "HEAD"
			}
		case strings.HasPrefix(line, "# branch.upstream "):
			st.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			fmt.Sscanf(strings.TrimPrefix(line, "# branch.ab "), "+%d -%d", &st.Ahead, &st.Behind)
		case strings.HasPrefix(line, "? "):
			st.Untracked++
		case line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "!"):
			st.Changes++
		}
	}
	if gitDir, err := git(ctx, dir, "rev-parse", "--absolute-git-dir"); err == nil {
		if fi, err := os.Stat(filepath.Join(gitDir, "FETCH_HEAD")); err == nil {
			st.FetchedAt = fi.ModTime().UnixMilli()
		}
	}
	return branch, st
}

// IsWokspace reports whether dir follows the wokspace layout: an AGENTS.md
// and the apps, build, docs and work directories at its root.
func IsWokspace(dir string) bool {
	if fi, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil || fi.IsDir() {
		return false
	}
	for _, d := range []string{"apps", "build", "docs", "work"} {
		if fi, err := os.Stat(filepath.Join(dir, d)); err != nil || !fi.IsDir() {
			return false
		}
	}
	return true
}

// PrepareInPlace is the working directory of an operations run: the
// project's own directory, used as it is (no worktree, no branch).
func PrepareInPlace(dir string) (Prepared, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Prepared{}, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return Prepared{}, err
	}
	if !fi.IsDir() {
		return Prepared{}, os.ErrInvalid
	}
	return Prepared{Dir: abs}, nil
}

// Run runs a command in dir and returns its standard output.
func Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	return run(ctx, dir, name, args...)
}
