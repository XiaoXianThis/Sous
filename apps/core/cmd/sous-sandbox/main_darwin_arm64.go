//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/localrun"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:])
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "identify" {
		if len(args) != 2 {
			return errors.New("用法：sous-sandbox identify ABSOLUTE_SOURCE")
		}
		id, err := localrun.IdentifySource(args[1])
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(id)
	}
	if len(args) > 0 && args[0] == "download-image" {
		if len(args) != 2 {
			return errors.New("用法：sous-sandbox download-image ABSOLUTE_OCI_ARCHIVE_PATH")
		}
		if err := localrun.DownloadImageArchive(ctx, args[1]); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "固定镜像已校验并保存。运行时用 --image-archive 指定此归档即可离线启动。")
		return nil
	}

	if len(args) > 0 && args[0] == "review" {
		if len(args) != 2 {
			return errors.New("用法：sous-sandbox review RESULT_DIRECTORY")
		}
		review, err := localrun.InspectReview(args[1])
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(review)
	}
	if len(args) > 0 && args[0] == "logs" {
		if len(args) != 3 {
			return errors.New("用法：sous-sandbox logs RESULT_DIRECTORY REVIEW_REVISION")
		}
		log, err := localrun.PreviewLog(args[1], args[2])
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(log)
	}
	if len(args) > 0 && args[0] == "preview" {
		if len(args) != 4 {
			return errors.New("用法：sous-sandbox preview RESULT_DIRECTORY REVIEW_REVISION RELATIVE_FILE_PATH")
		}
		preview, err := localrun.PreviewFile(args[1], args[2], args[3])
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(preview)
	}
	if len(args) > 0 && args[0] == "apply" {
		if len(args) != 4 {
			return errors.New("用法：sous-sandbox apply RESULT_DIRECTORY REVIEW_REVISION RELATIVE_FILE_PATH")
		}
		applied, err := localrun.ApplyReview(ctx, args[1], args[2], args[3])
		if applied.Path != "" {
			err = errors.Join(err, json.NewEncoder(os.Stdout).Encode(applied))
		}
		return err
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	flagSet := flag.NewFlagSet("sous-sandbox", flag.ContinueOnError)
	o := localrun.Options{GuestOptions: localrun.GuestOptions{Progress: func(phase string) { fmt.Fprintln(os.Stderr, phase) }}}
	parentManaged := flagSet.Bool("parent-managed", false, "cancel the operation when the launcher's stdin pipe closes")
	flagSet.StringVar(&o.SourceIdentity, "source-identity", "", "verify the native picker's one-use source identity")
	recoverOnly := flagSet.Bool("recover", false, "reconcile and remove a guest left by an interrupted run without reading any project")
	flagSet.StringVar(&o.ImageArchive, "image-archive", "", "verified Alpine OCI archive; seed local image cache and disable pulling")
	flagSet.BoolVar(&o.Offline, "offline", false, "use the cached guest image; do not contact the image registry")
	flagSet.StringVar(&o.Source, "source", "", "explicitly authorized file or directory (absolute, no symlinks)")
	flagSet.StringVar(&o.Home, "runtime-home", filepath.Join(userHome, ".sous-sandbox"), "dedicated private VM state directory")
	flagSet.StringVar(&o.Output, "output", "", "existing local parent directory for review artifacts; never the source")
	flagSet.StringVar(&o.RuntimePath, "msb", "", "absolute path to official pinned msb executable")
	flagSet.StringVar(&o.FirmwarePath, "firmware", "", "absolute path to official pinned libkrunfw dylib")
	flagSet.DurationVar(&o.Timeout, "timeout", 90*time.Second, "execution timeout (1s to 5m)")
	flagSet.Usage = func() {
		fmt.Fprintln(flagSet.Output(), "Usage: sous-sandbox --source PATH --output DIR --msb FILE --firmware FILE [--runtime-home DIR] -- GUEST_COMMAND [ARGS...]")
		fmt.Fprintln(flagSet.Output(), "Review: sous-sandbox review RESULT_DIRECTORY; apply one reviewed change: sous-sandbox apply RESULT_DIRECTORY REVISION RELATIVE_FILE_PATH")
		fmt.Fprintln(flagSet.Output(), "Runs once in a network-disabled Microsandbox guest. Source files are copied, never mounted or modified.")
		flagSet.PrintDefaults()
	}
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	o.Command = flagSet.Args()
	if *parentManaged {
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() { _, _ = io.Copy(io.Discard, os.Stdin); cancel() }()
		ctx = runCtx
	}
	if *recoverOnly {
		if o.RuntimePath == "" || o.FirmwarePath == "" || o.Source != "" || o.Output != "" || o.ImageArchive != "" || len(o.Command) != 0 {
			return errors.New("--recover 需要运行时和固件路径，不能接收项目或命令")
		}
		if err := localrun.Recover(ctx, o.GuestOptions); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "已对账：没有等待销毁的 Guest。")
		return nil
	}
	if o.Source == "" || o.Output == "" || o.RuntimePath == "" || o.FirmwarePath == "" || len(o.Command) == 0 {
		flagSet.Usage()
		return errors.New("需要明确指定授权文件、结果目录、固定的运行时和 Guest 命令")
	}
	result, err := localrun.Run(ctx, o)
	if err != nil {
		if *parentManaged && ctx.Err() != nil && !errors.Is(err, localrun.ErrCleanupUnconfirmed) {
			if writeErr := json.NewEncoder(os.Stdout).Encode(map[string]bool{"cancelled": true, "cleanup_confirmed": true}); writeErr != nil {
				return errors.Join(err, writeErr)
			}
		}
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("Guest 命令退出码 %d；报告已保留，未写回原项目", result.ExitCode)
	}
	return nil
}
