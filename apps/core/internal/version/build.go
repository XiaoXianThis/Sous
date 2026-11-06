package version

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
)

// Commit is the commit the binary was built from: set with -ldflags, else
// read from the Go build info (vcs.revision, with "-dirty" when the tree
// had changes), else "".
var Commit = ""

var commitOnce sync.Once

// BuildCommit returns Commit, filling it from the build info once.
func BuildCommit() string {
	commitOnce.Do(func() {
		if Commit != "" {
			return
		}
		info, ok := debug.ReadBuildInfo()
		if !ok {
			return
		}
		rev, dirty := "", false
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if len(rev) > 12 {
			rev = rev[:12]
		}
		if rev != "" && dirty {
			rev += "-dirty"
		}
		Commit = rev
	})
	return Commit
}

// Web is what the built PWA says about itself (build-info.json, written by
// the web build): which commit its sources were at, and when it was built.
type Web struct {
	Commit  string `json:"commit"`
	Dirty   bool   `json:"dirty,omitempty"`
	BuiltAt string `json:"built_at"`
}

// ReadWeb reads dir/build-info.json; ok is false when there is none (a build
// from before build info, or no web directory).
func ReadWeb(dir string) (Web, bool) {
	if dir == "" {
		return Web{}, false
	}
	raw, err := os.ReadFile(filepath.Join(dir, "build-info.json"))
	if err != nil {
		return Web{}, false
	}
	var w Web
	if json.Unmarshal(raw, &w) != nil {
		return Web{}, false
	}
	return w, true
}
