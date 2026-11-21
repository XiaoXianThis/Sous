package hub

import (
	"fmt"
	"os"
	"path/filepath"
)

// lockWorkspace prevents a second local server, even on another port, from
// dispatching and scheduling against the same data. The lock is released by
// the OS on process exit; the file must not be deleted to avoid inode races.
func lockWorkspace(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "workspace.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := tryLock(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("cannot lock workspace (is another Sous Hub using this directory?): %w", err)
	}
	return f, nil
}
