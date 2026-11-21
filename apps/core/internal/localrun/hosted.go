//go:build cgo && ((darwin && arm64) || (linux && (amd64 || arm64) && sous_linux_worker))

package localrun

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"path"
	"slices"
	"strings"
	"time"
)

// InitializeHosted prepares the pinned offline image and probes the actual
// execution backend before the worker asks Hub for an authorized lease.
func InitializeHosted(ctx context.Context, o GuestOptions) error {
	if o.ImageArchive == "" {
		return errors.New("云执行要求已核验的离线镜像")
	}
	if !processLock.TryLock() {
		return errors.New("另一个沙盒运行正在进行")
	}
	defer processLock.Unlock()
	unlock, err := prepare(ctx, o, false)
	if err != nil {
		return err
	}
	defer unlock()
	return importImage(ctx, o.Home, o.ImageArchive)
}

// RunHosted accepts only portable bytes; unlike the desktop broker it never
// reads or stages a host project, nor does it make local review directories.
// An authorized worker must durably checkpoint before Guest destruction.
func RunHosted(ctx context.Context, o GuestOptions, files map[string][]byte, checkpoint func(exitCode int, files map[string][]byte) error) error {
	if checkpoint == nil {
		return errors.New("云执行需要持久检查点")
	}
	if err := o.validateCommand(); err != nil {
		return err
	}
	if !processLock.TryLock() {
		return errors.New("另一个沙盒运行正在进行")
	}
	defer processLock.Unlock()
	before, err := portableSnapshot(ctx, files)
	if err != nil {
		return err
	}
	unlock, err := prepare(ctx, o, false)
	if err != nil {
		return err
	}
	defer unlock()
	run, cancel := context.WithTimeout(ctx, 2*time.Minute+o.Timeout)
	defer cancel()
	id := namePrefix + strings.ToLower(rand.Text()[:10])
	_, _, _, err = guestRun(run, id, before, o, true, checkpoint)
	return err
}

func portableSnapshot(ctx context.Context, files map[string][]byte) (snapshot, error) {
	s := newSnapshot()
	if len(files) == 0 {
		return s, errors.New("没有获准导入的云执行材料")
	}
	dirs := map[string]bool{}
	for name, data := range files {
		if !PortablePath(name) {
			return s, errors.New("云执行材料包含无效路径")
		}
		if err := s.entry(); err != nil {
			return s, err
		}
		if err := s.read(ctx, name, bytes.NewReader(data)); err != nil {
			return s, err
		}
		for d := path.Dir(name); d != "." && !dirs[d]; d = path.Dir(d) {
			if _, exists := files[d]; exists {
				return s, errors.New("云执行材料路径同时是文件和目录")
			}
			if err := s.entry(); err != nil {
				return s, err
			}
			dirs[d] = true
			s.dirs = append(s.dirs, d)
		}
	}
	// Parents must exist before a write reaches the Guest filesystem.
	slices.Sort(s.dirs)
	return s, nil
}
