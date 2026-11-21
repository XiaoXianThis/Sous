package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acpagent"
	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/hub"
)

func runHostedHub(args []string) error {
	flags := flag.NewFlagSet("sous hosted-hub", flag.ContinueOnError)
	cfgFile := flags.String("config", "", "required hosted workspace YAML")
	setup := flags.String("setup-link-file", "", "issue first enrollment into a new 0600 file instead of starting the service")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *cfgFile == "" || flags.NArg() != 0 {
		return errors.New("sous hosted-hub requires --config FILE")
	}
	cfg, model, err := config.LoadHosted(*cfgFile)
	if err != nil {
		return err
	}
	if info, err := os.Stat(filepath.Join(cfg.WebDir, "index.html")); err != nil || !info.Mode().IsRegular() {
		return errors.New("hosted web_dir must contain a built index.html")
	}
	var factory hub.AgentFactory
	if model != "" && *setup == "" {
		factory, err = acpagent.Factory(model)
		if err != nil {
			return err
		}
	}
	h, err := hub.Open(cfg, factory)
	if err != nil {
		return err
	}
	if *setup != "" {
		defer h.Close()
		f, err := os.OpenFile(*setup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		link, err := h.CreateHostedSetup(context.Background())
		if err == nil {
			_, err = f.WriteString(link + "\n")
		}
		if err == nil {
			err = f.Sync()
		}
		err = errors.Join(err, f.Close())
		if err != nil {
			_ = os.Remove(*setup)
		}
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	h.WatchdogOn = true
	return h.Run(ctx)
}
