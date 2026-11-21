//go:build cgo

package localrun

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Options belongs to the local owner's file authorization and review flow.
// Hosted workers do not accept this type or open project paths.
type Options struct {
	GuestOptions
	Source, Output string
	SourceIdentity string
	Offline        bool
}

type Result struct {
	Directory string `json:"directory"`
	ExitCode  int    `json:"exit_code"`
	Imported  int    `json:"imported_files"`
	Skipped   int    `json:"excluded_entries"`
}

// Run is the single invocation of a private host broker. It must not run
// inside Hub or accept paths or command arguments supplied over the network.
func Run(ctx context.Context, o Options) (Result, error) {
	var result Result
	if !processLock.TryLock() {
		return result, errors.New("另一个本机沙盒运行正在进行")
	}
	defer processLock.Unlock()
	if !filepath.IsAbs(o.Source) || filepath.Clean(o.Source) != o.Source || o.Source == "/" {
		return result, errors.New("请明确选择一个绝对路径，不接受磁盘根目录")
	}
	if err := o.validateCommand(); err != nil {
		return result, err
	}
	out, err := filepath.Abs(o.Output)
	if err != nil || o.Output == "" {
		return result, errors.New("请指定结果目录的上级路径")
	}
	out, err = filepath.EvalSymlinks(out)
	if err != nil {
		return result, fmt.Errorf("无法打开结果目录：%w", err)
	}
	if samePathOrUnder(out, o.Source) || samePathOrUnder(out, o.Home) || samePathOrUnder(o.Home, o.Source) || samePathOrUnder(o.Source, o.Home) {
		return result, errors.New("输入、运行时数据和结果目录必须隔离，结果不能写入原项目")
	}
	inspect, err := os.Lstat(o.Source)
	if err != nil {
		return result, fmt.Errorf("无法定位选中的项目：%w", err)
	}
	if o.SourceIdentity != "" {
		actual, err := IdentifySource(o.Source)
		if err != nil || actual != o.SourceIdentity {
			return result, errors.New("选中的文件或目录已经变化，请在本机重新选择")
		}
	}
	if !inspect.Mode().IsRegular() && !inspect.IsDir() {
		return result, errors.New("授权入口不是普通文件或目录，不会跟随链接")
	}
	if err := ensureSeparate(inspect, out); err != nil {
		return result, err
	}
	if err := ensureSeparate(inspect, o.Home); err != nil {
		return result, err
	}
	outRoot, err := os.OpenRoot(out)
	if err != nil {
		return result, err
	}
	defer outRoot.Close()
	unlock, err := prepare(ctx, o.GuestOptions, true)
	if err != nil {
		return result, err
	}
	defer unlock()
	if o.ImageArchive != "" {
		o.progress("核对固定离线镜像")
		if err := importImage(ctx, o.Home, o.ImageArchive); err != nil {
			return result, err
		}
		o.Offline = true
	}
	o.progress("检查授权目录")
	before, err := capture(ctx, o.Source, inspect)
	if err != nil {
		return result, fmt.Errorf("用户选择的项目未导入：%w", err)
	}
	if len(before.files) == 0 {
		return result, errors.New("选中范围内没有可导入的文件，请检查筛选规则")
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute+o.Timeout)
	defer cancel()
	id := namePrefix + strings.ToLower(rand.Text()[:10])
	after, exit, logs, err := guestRun(runCtx, id, before, o.GuestOptions, o.Offline, nil)
	if err != nil {
		return result, errors.Join(err, ctx.Err())
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	o.progress("Guest 已销毁，正在保存审阅材料")
	where, err := writeReportUnder(outRoot, id, before, after, exit, logs)
	if err != nil {
		return result, fmt.Errorf("已销毁 Guest，但无法保存结果：%w", err)
	}
	return Result{Directory: where, ExitCode: exit, Imported: len(before.files), Skipped: before.skipped}, nil
}

func samePathOrUnder(candidate, root string) bool {
	// Darwin's common filesystems compare names without case and with
	// normalization. Also compare folded paths even on case-sensitive volumes.
	fold := cases.Fold()
	a, b := fold.String(norm.NFC.String(filepath.Clean(candidate))), fold.String(norm.NFC.String(filepath.Clean(root)))
	rel, err := filepath.Rel(b, a)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// Distinguish aliases to the same directory inode (e.g. APFS Data volume
// paths), not just different textual paths on common filesystems.
func ensureSeparate(source os.FileInfo, parent string) error {
	if !source.IsDir() {
		return nil
	}
	if !filepath.IsAbs(parent) {
		return errors.New("输出和运行时目录必须是绝对路径")
	}
	for {
		fi, err := os.Stat(parent)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && os.SameFile(source, fi) {
			return errors.New("输出和运行时目录不可指向源项目内部")
		}
		up := filepath.Dir(parent)
		if up == parent {
			return nil
		}
		parent = up
	}
}
