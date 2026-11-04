package agents

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// Recipe is a fixed command sous runs for the user: an install, an upgrade,
// a login. Either Argv runs directly, or Script runs in the system shell
// (sh on macOS and Linux, PowerShell on Windows); official installers are
// one-liners like `curl … | bash`.
type Recipe struct {
	Argv   []string
	Script string
	Env    []string
}

// String is the command as the user would type it.
func (r Recipe) String() string {
	if r.Script != "" {
		return r.Script
	}
	parts := make([]string, len(r.Argv))
	for i, a := range r.Argv {
		if strings.ContainsAny(a, " \t\"'") {
			a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		}
		parts[i] = a
	}
	s := strings.Join(parts, " ")
	if len(r.Env) > 0 {
		s = strings.Join(r.Env, " ") + " " + s
	}
	return s
}

// Command resolves what to execute: the program path and its arguments.
func (r Recipe) Command() (string, []string, error) {
	if r.Script != "" {
		if runtime.GOOS == "windows" {
			ps, err := exec.LookPath("powershell")
			if err != nil {
				return "", nil, err
			}
			return ps, []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", r.Script}, nil
		}
		sh, err := exec.LookPath("bash")
		if err != nil {
			if sh, err = exec.LookPath("sh"); err != nil {
				return "", nil, err
			}
		}
		return sh, []string{"-c", r.Script}, nil
	}
	if len(r.Argv) == 0 {
		return "", nil, errors.New("empty command")
	}
	path, err := exec.LookPath(r.Argv[0])
	if err != nil {
		return "", nil, err
	}
	return path, r.Argv[1:], nil
}

// pick chooses the recipe for this system.
func pick(m map[string]Recipe) (Recipe, bool) {
	if r, ok := m[runtime.GOOS]; ok {
		return r, true
	}
	if runtime.GOOS != "windows" {
		if r, ok := m["unix"]; ok {
			return r, true
		}
	}
	r, ok := m["*"]
	return r, ok
}

func (s Spec) InstallRecipe() (Recipe, bool) { return pick(s.Install) }
func (s Spec) LoginRecipe() (Recipe, bool)   { return pick(s.Login) }
func (s Spec) LogoutRecipe() (Recipe, bool)  { return pick(s.Logout) }

// UpgradeRecipe is the agent's own updater, or its installer again.
func (s Spec) UpgradeRecipe() (Recipe, bool) {
	if r, ok := pick(s.Upgrade); ok {
		return r, true
	}
	return pick(s.Install)
}

// ConfigFile finds one of the agent's editable files and expands its path.
func (s Spec) ConfigFile(id string) (proto.AgentConfigFile, bool) {
	for _, c := range s.Configs {
		if c.ID == id {
			c.Path = ExpandHome(c.Path)
			return c, true
		}
	}
	return proto.AgentConfigFile{}, false
}

// ExpandHome replaces a leading ~ with the user's home directory.
func ExpandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return filepath.FromSlash(p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.FromSlash(p)
	}
	return filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(p, "~")))
}

var pathMu sync.Mutex

// AugmentPath adds the directories where agent installers put their
// commands to PATH, when they exist and are missing. A daemon started by
// a service manager often has a shorter PATH than the user's shell, and an
// agent installed a minute ago lands in a directory that did not exist
// when the daemon started. Safe to call again.
func AugmentPath() {
	pathMu.Lock()
	defer pathMu.Unlock()
	cur := filepath.SplitList(os.Getenv("PATH"))
	have := map[string]bool{}
	for _, d := range cur {
		have[filepath.Clean(d)] = true
	}
	var add []string
	for _, d := range extraPaths() {
		d = filepath.Clean(d)
		if have[d] {
			continue
		}
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			add = append(add, d)
			have[d] = true
		}
	}
	if len(add) > 0 {
		os.Setenv("PATH", strings.Join(append(cur, add...), string(os.PathListSeparator)))
	}
}

func extraPaths() []string {
	home, _ := os.UserHomeDir()
	var dirs []string
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".opencode", "bin"),
			filepath.Join(home, ".bun", "bin"),
			filepath.Join(home, ".npm-global", "bin"),
			filepath.Join(home, ".volta", "bin"),
		)
		// nvm: the newest installed Node.js.
		if vs, _ := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin")); len(vs) > 0 {
			sort.Slice(vs, func(i, j int) bool { return versionLess(vs[i], vs[j]) })
			dirs = append(dirs, vs[len(vs)-1])
		}
	}
	switch runtime.GOOS {
	case "darwin":
		dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin")
	case "linux":
		dirs = append(dirs, "/usr/local/bin")
	case "windows":
		if ad := os.Getenv("APPDATA"); ad != "" {
			dirs = append(dirs, filepath.Join(ad, "npm"))
		}
		if home != "" {
			dirs = append(dirs, filepath.Join(home, ".local", "bin"))
		}
	}
	return dirs
}

// versionLess compares nvm directories like .../v24.21.0/bin numerically.
func versionLess(a, b string) bool {
	va := strings.Split(strings.TrimPrefix(filepath.Base(filepath.Dir(a)), "v"), ".")
	vb := strings.Split(strings.TrimPrefix(filepath.Base(filepath.Dir(b)), "v"), ".")
	for i := 0; i < len(va) && i < len(vb); i++ {
		if len(va[i]) != len(vb[i]) {
			return len(va[i]) < len(vb[i])
		}
		if va[i] != vb[i] {
			return va[i] < vb[i]
		}
	}
	return len(va) < len(vb)
}
