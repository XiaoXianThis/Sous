//go:build darwin || (linux && (amd64 || arm64) && cgo && sous_linux_worker)

package localrun

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const safeOpen = unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK

// openParent anchors all traversal at the host root and does not interpret
// symlinks in any component, including ancestors of a selected directory.
func openParent(abs string) (*os.File, string, error) {
	if !filepath.IsAbs(abs) || filepath.Clean(abs) != abs || abs == "/" {
		return nil, "", errors.New("需要明确的绝对路径")
	}
	parts := strings.Split(strings.TrimPrefix(abs, "/"), "/")
	fd, err := unix.Open("/", safeOpen|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, "", err
	}
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat(fd, part, safeOpen|unix.O_DIRECTORY, 0)
		_ = unix.Close(fd)
		if err != nil {
			return nil, "", fmt.Errorf("无法打开选中路径的上级目录；不跟随链接：%w", err)
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), filepath.Dir(abs)), parts[len(parts)-1], nil
}

func openChecked(parent int, name string) (*os.File, unix.Stat_t, error) {
	var before, after unix.Stat_t
	if err := unix.Fstatat(parent, name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, after, err
	}
	flags := safeOpen
	switch before.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		flags |= unix.O_DIRECTORY
	case unix.S_IFREG:
		if before.Nlink != 1 {
			return nil, after, fmt.Errorf("不读取硬链接：%q", name)
		}
	default:
		return nil, after, fmt.Errorf("不读取符号链接或特殊文件：%q", name)
	}
	fd, err := unix.Openat(parent, name, flags, 0)
	if err != nil {
		return nil, after, err
	}
	if err := unix.Fstat(fd, &after); err != nil || !same(before, after) {
		_ = unix.Close(fd)
		return nil, after, fmt.Errorf("文件在导入期间被替换：%q", name)
	}
	return os.NewFile(uintptr(fd), name), after, nil
}

func same(a, b unix.Stat_t) bool {
	return a.Ino == b.Ino && a.Dev == b.Dev && a.Mode == b.Mode && a.Nlink == b.Nlink && a.Size == b.Size && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}

func createNew(fd int, name string, data []byte) error {
	fresh, err := unix.Openat(fd, name, unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_EXCL|unix.O_CREAT, 0o600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fresh), name)
	_, err = f.Write(data)
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		_ = unix.Unlinkat(fd, name, 0)
	}
	return err
}
