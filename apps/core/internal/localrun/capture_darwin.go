package localrun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// capture binds the user's selection to open descriptors. It never re-opens a
// path returned by a guest, follows symlinks, or reads through a device change.
func capture(ctx context.Context, source string, expected os.FileInfo) (snapshot, error) {
	s := newSnapshot()
	if !filepath.IsAbs(source) || filepath.Clean(source) != source || source == "/" {
		return s, errors.New("请明确选择一个绝对路径，不接受磁盘根目录")
	}
	if home, err := os.UserHomeDir(); err == nil && (source == home || strings.HasPrefix(home, source+"/")) {
		return s, errors.New("请只选择一个项目目录或文件，不能导入整个用户目录")
	}
	parent, name, err := openParent(source)
	if err != nil {
		return s, err
	}
	defer parent.Close()
	dir := int(parent.Fd())
	if !safeName(name) || excluded(name) {
		return s, errors.New("不能把系统、隐藏、依赖或常见凭据文件作为共享范围")
	}
	f, st, err := openChecked(dir, name)
	if err != nil {
		return s, err
	}
	defer f.Close()
	if expected != nil {
		actual, infoErr := f.Stat()
		if infoErr != nil || !os.SameFile(actual, expected) {
			return s, errors.New("选中的文件或目录已经被替换，请重新选择")
		}
	}
	s.binding = &binding{Source: source, Root: fileIdentity(st), Directory: st.Mode&unix.S_IFMT == unix.S_IFDIR,
		Directories: make(map[string]identity), Files: make(map[string]identity)}
	if s.binding.Directory {
		err = s.walk(ctx, f, st.Dev, "", 0)
	} else {
		err = s.addFile(ctx, f, st, name)
	}
	if err == nil {
		err = verifyEntry(dir, name, st)
	}
	return s, err
}

// IdentifySource snapshots the native selection without importing its files.
// The launcher's later Run must check this exact identity again before import.
func IdentifySource(source string) (string, error) {
	parent, name, err := openParent(source)
	if err != nil {
		return "", err
	}
	defer parent.Close()
	f, st, err := openChecked(int(parent.Fd()), name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	id := fileIdentity(st)
	return fmt.Sprintf("%d:%d:%d:%d", id.Device, id.Inode, id.BornSec, id.BornNsec), nil
}

func fileIdentity(st unix.Stat_t) identity {
	return identity{Device: st.Dev, Inode: st.Ino, BornSec: st.Btim.Sec, BornNsec: st.Btim.Nsec}
}

func verifyEntry(parent int, name string, st unix.Stat_t) error {
	var now unix.Stat_t
	if err := unix.Fstatat(parent, name, &now, unix.AT_SYMLINK_NOFOLLOW); err != nil || !same(st, now) {
		return fmt.Errorf("导入期间文件或目录发生变化，请重试：%q", name)
	}
	return nil
}

func (s *snapshot) addFile(ctx context.Context, f *os.File, st unix.Stat_t, name string) error {
	if st.Size > int64(min(MaxFile, maxBytes-s.total)) {
		return fmt.Errorf("文件或目录总大小超过限制：%q", name)
	}
	if err := s.read(ctx, name, f); err != nil {
		return err
	}
	var after unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &after); err != nil || !same(st, after) {
		return fmt.Errorf("导入期间文件发生变化：%q", name)
	}
	s.binding.Files[name] = fileIdentity(st)
	return nil
}

func (s *snapshot) walk(ctx context.Context, dir *os.File, device int32, prefix string, depth int) error {
	if depth > maxDepth {
		return errors.New("目录嵌套超过预览上限")
	}
	children, err := dir.ReadDir(maxEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(children) > maxEntries {
		return errors.New("目录条目超过预览上限")
	}
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.entry(); err != nil {
			return err
		}
		name := child.Name()
		if excluded(name) {
			s.skipped++
			continue
		}
		rel := name
		if prefix != "" {
			rel = prefix + "/" + name
		}
		if !relative(rel) {
			return fmt.Errorf("不支持的目录条目：%q", rel)
		}
		fd := int(dir.Fd())
		f, st, err := openChecked(fd, name)
		if err != nil {
			return err
		}
		func() {
			defer f.Close()
			if st.Dev != device {
				err = fmt.Errorf("不读取子挂载：%q", rel)
				return
			}
			if st.Mode&unix.S_IFMT == unix.S_IFDIR {
				s.dirs = append(s.dirs, rel)
				s.binding.Directories[rel] = fileIdentity(st)
				err = s.walk(ctx, f, device, rel, depth+1)
			} else {
				err = s.addFile(ctx, f, st, rel)
			}
			if err == nil {
				err = verifyEntry(fd, name, st)
			}
		}()
		if err != nil {
			return err
		}
	}
	return nil
}
