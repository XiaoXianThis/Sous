// Package localrun implements the owner-operated Microsandbox run boundary.
// Its caller is a local command, not an RPC handler in Hub or legacy Node.
package localrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxFile    = 2 << 20
	maxBytes   = 32 << 20
	maxEntries = 1024
	maxDepth   = 16
)

// Identity is host-only authorization state. It never leaves the broker's
// private results directory or appears in the portable review manifest.
type identity struct {
	Device   int32  `json:"device"`
	Inode    uint64 `json:"inode"`
	BornSec  int64  `json:"born_sec"`
	BornNsec int64  `json:"born_nsec"`
}

type binding struct {
	Source      string              `json:"source"`
	Root        identity            `json:"root"`
	Directory   bool                `json:"directory"`
	Directories map[string]identity `json:"directories,omitempty"`
	Files       map[string]identity `json:"files,omitempty"`
	Revision    string              `json:"manifest_sha256,omitempty"`
}

type snapshot struct {
	binding                 *binding
	files                   map[string][]byte
	dirs                    []string
	entries, total, skipped int
}

func newSnapshot() snapshot { return snapshot{files: make(map[string][]byte)} }

func (s *snapshot) entry() error {
	s.entries++
	if s.entries > maxEntries {
		return errors.New("目录条目数量超过预览上限（1024）")
	}
	return nil
}

// safeName defines a portable subset. No control characters, drive prefixes,
// aliases for a referring directory, or URI/Windows separator ambiguity.
func safeName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 200 || !utf8.ValidString(name) || strings.ContainsAny(name, `/\:*?"<>|`) || strings.TrimRight(name, " .") != name {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	stem, _, _ := strings.Cut(strings.ToLower(name), ".")
	if stem == "con" || stem == "prn" || stem == "aux" || stem == "nul" || len(stem) == 4 && (strings.HasPrefix(stem, "com") || strings.HasPrefix(stem, "lpt")) && stem[3] >= '1' && stem[3] <= '9' {
		return false
	}
	return true
}

// Private data and dependency/build trees are deliberately not selectable in
// this preview. This is not secret detection: owners must review their inputs.
func excluded(name string) bool {
	name = strings.ToLower(name)
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".pem") || strings.HasSuffix(name, ".key") || strings.HasSuffix(name, ".p12") || strings.HasSuffix(name, ".pfx") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "dist", "build", "target", "credentials.json", "secrets.json", "id_ed25519", "id_rsa":
		return true
	}
	return false
}

func relative(name string) bool {
	if !fs.ValidPath(name) || len(name) > 512 {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if !safeName(part) || excluded(part) {
			return false
		}
	}
	return true
}

func (s *snapshot) read(ctx context.Context, name string, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !relative(name) {
		return fmt.Errorf("不能导入或导出此相对路径：%q", name)
	}
	if _, present := s.files[name]; present {
		return errors.New("文件清单包含重复条目")
	}
	left := min(MaxFile, maxBytes-s.total)
	data, err := io.ReadAll(io.LimitReader(r, int64(left)+1))
	if err != nil {
		return err
	}
	if len(data) > left {
		return fmt.Errorf("文件或目录总大小超过限制：%q（单个 2 MiB，总计 32 MiB）", name)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.files[name] = data
	s.total += len(data)
	return nil
}

func digest(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// PortablePath is the shared, bounded file-name policy used by the cloud
// snapshot transport before any worker stages bytes into its private tree.
func PortablePath(name string) bool {
	return relative(name) && len(strings.Split(name, "/")) <= maxDepth
}
