// Command sous-hub is the sous server: the supervisor, the event store and
// the client API.
//
//	sous-hub [-config hub.yaml]                       serve
//	sous-hub [-config hub.yaml] admin passkey-reset   print a new passkey setup link
//	sous-hub [-config hub.yaml] admin backup <file>   write a consistent database snapshot
//	sous-hub version
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/hub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sous-hub:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "hub.yaml", "path to the config file")
	debug := flag.Bool("debug", false, "log debug messages")
	flag.Parse()
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	args := flag.Args()
	if len(args) > 0 && args[0] == "version" {
		fmt.Println(version.Version)
		return nil
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	h, err := hub.Open(cfg)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(args) == 0 {
		return h.Run(ctx)
	}
	defer h.Store.Close()
	switch {
	case len(args) >= 2 && args[0] == "admin" && args[1] == "passkey-reset":
		fs := flag.NewFlagSet("passkey-reset", flag.ExitOnError)
		revokeAll := fs.Bool("revoke-all", false, "also remove every passkey and log out every session")
		fs.Parse(args[2:])
		tok, err := h.Auth.ResetPasskeys(ctx, *revokeAll)
		if err != nil {
			return err
		}
		fmt.Println("Open this link within 24 hours to register a passkey:")
		fmt.Println(h.Auth.SetupURL(tok))
		return nil
	case len(args) == 3 && args[0] == "admin" && args[1] == "backup":
		if err := h.Store.Backup(ctx, args[2]); err != nil {
			return err
		}
		fmt.Println("backup written to", args[2])
		return nil
	}
	return fmt.Errorf("unknown command %q", args)
}
