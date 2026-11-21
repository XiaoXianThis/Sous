package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acpagent"
	"github.com/XiaoXianThis/Sous/apps/core/internal/brain"
	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/hub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/legacyagent"
	"github.com/XiaoXianThis/Sous/apps/core/internal/localdispatch"
	"github.com/XiaoXianThis/Sous/apps/core/internal/node"
)

func runHub(args []string) error {
	flags := flag.NewFlagSet("sous hub", flag.ContinueOnError)
	home := flags.String("home", "", "Sous home (default $SOUS_HOME or ~/.sous)")
	cfgFile := flags.String("config", "", "optional local workspace YAML")
	web := flags.String("web-dir", "", "built WebUI directory")
	listen := flags.String("listen", "", "loopback listen address (default 127.0.0.1:8787)")
	publicURL := flags.String("public-url", "", "matching local browser origin")
	desktopAuth := flags.Bool("desktop-native-auth", false, "allow desktop system authentication over private inherited pipes")
	managed := flags.Bool("app-managed", false, "emit one startup record and shut down when stdin closes")
	agentSwitch := flags.Bool("desktop-agent-switch", false, "enable selection of local supervisor modules from the built-in catalog")
	sandboxDispatch := flags.Bool("desktop-sandbox-dispatch", false, "enable workspace requests over the desktop private inherited pipes")
	agent := flags.String("agent", "none", "none, acp (first-party preview), or legacy")
	agentConfig := flags.String("agent-config", "", "model YAML for the ACP preview")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *agentSwitch && (!*managed || (*agent != "legacy" && *agent != "none") || *agentConfig != "") {
		return errors.New("desktop agent switch requires --app-managed, a legacy/none default, and no --agent-config")
	}
	if *desktopAuth && (!*managed || *cfgFile != "") {
		return errors.New("desktop native auth requires --app-managed and the default local Hub assembly")
	}
	if *sandboxDispatch && !*managed {
		return errors.New("desktop sandbox dispatch requires --app-managed")
	}
	if flags.NArg() != 0 {
		return errors.New("sous hub accepts flags, not positional arguments")
	}
	var factory hub.AgentFactory
	switch *agent {
	case "none":
	case "acp":
		if *agentConfig == "" {
			return errors.New("--agent acp requires --agent-config")
		}
		var err error
		factory, err = acpagent.Factory(*agentConfig)
		if err != nil {
			return fmt.Errorf("ACP supervisor: %w", err)
		}
	case "legacy":
		factory = legacyagent.New
	default:
		return fmt.Errorf("unknown agent %q (expected none, acp or legacy)", *agent)
	}
	if *agent != "acp" && *agentConfig != "" {
		return errors.New("--agent-config requires --agent acp")
	}
	root := *home
	if root == "" {
		var err error
		root, err = node.Root()
		if err != nil {
			return err
		}
	}
	cfg := config.ForLocalWorkspace(filepath.Join(root, "workspace"))
	if *cfgFile != "" {
		var err error
		cfg, err = config.Load(*cfgFile)
		if err != nil {
			return err
		}
		cfg.LocalWorkspace = true
		// In the local entry point network tools and external MCP remain off;
		// explicitly chosen legacy models can still use configured providers.
		cfg.Web.Disabled, cfg.MCP.Disabled = true, true
	}
	if *web != "" {
		cfg.WebDir = *web
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *publicURL != "" {
		cfg.PublicURL = strings.TrimRight(*publicURL, "/")
	} else if *listen != "" {
		_, port, err := net.SplitHostPort(*listen)
		if err != nil {
			return fmt.Errorf("invalid --listen: %w", err)
		}
		cfg.PublicURL = "http://localhost:" + port
	}
	cfg.LocalDesktopAuth = *desktopAuth
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	data, err := filepath.EvalSymlinks(cfg.DataDir)
	if err != nil {
		return err
	}
	data, err = filepath.Abs(data)
	if err != nil {
		return err
	}
	brainPath, err := filepath.Abs(brain.DataDir(root))
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(brainPath); err == nil {
		brainPath = resolved
	}
	if strings.EqualFold(data, brainPath) {
		return errors.New("local workspace cannot open the relay brain's database")
	}
	if cfg.WebDir != "" {
		if st, err := os.Stat(filepath.Join(cfg.WebDir, "index.html")); err != nil || !st.Mode().IsRegular() {
			return errors.New("built WebUI not found: run wok build web and pass --web-dir")
		}
	}
	cfg.DataDir = data
	cfg.LocalSandboxDispatch = *sandboxDispatch
	var h *hub.Hub
	if *agentSwitch {
		// The app's local profile is the only configuration source. Neither
		// the browser nor the database can name a file or launch program.
		profile := filepath.Join(root, "supervisor.yaml")
		h, err = hub.OpenLocalWithAgents(cfg, hub.LocalAgents{Default: *agent, Choices: map[string]hub.AgentFactory{
			"legacy": legacyagent.New,
			"acp": func(deps hub.AgentDeps) (hub.AgentModule, error) {
				acpFactory, err := acpagent.Factory(profile)
				if err != nil {
					return hub.AgentModule{}, err
				}
				return acpFactory(deps)
			},
		}})
	} else {
		h, err = hub.Open(cfg, factory)
	}
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *managed {
		pipe := h.SandboxDispatch != nil || *desktopAuth
		h.LocalReady = func(origin, setupURL string) error {
			record := map[string]any{"origin": origin, "setup_url": setupURL}
			if h.SandboxDispatch != nil {
				record["sandbox_dispatch"] = localdispatch.PipeVersion
			}
			if *desktopAuth {
				record["desktop_auth"] = localdispatch.DesktopAuthVersion
			}
			if err := json.NewEncoder(os.Stdout).Encode(record); err != nil {
				return err
			}
			if pipe {
				var grant func(bool) (string, error)
				var lock func()
				if *desktopAuth {
					grant, lock = h.Auth.DesktopGrant, h.Auth.LockDesktop
				}
				go func() { _ = h.SandboxDispatch.ServeDesktop(ctx, os.Stdin, os.Stdout, grant, lock); stop() }()
			}
			return nil
		}
		if !pipe {
			go func() { _, _ = io.Copy(io.Discard, os.Stdin); stop() }()
		}
	}
	return h.Run(ctx)
}

func runACPAgent(args []string) error {
	flags := flag.NewFlagSet("sous agent", flag.ContinueOnError)
	configPath := flags.String("config", "", "first-party ACP supervisor model YAML")
	managed := flags.Bool("managed", false, "use the pinned model snapshot supplied by the parent")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *managed && flags.NArg() == 0 && *configPath == "" {
		return acpagent.ServeManaged(os.Stdin, os.Stdout)
	}
	if *configPath == "" || *managed || flags.NArg() != 0 {
		return errors.New("sous agent requires --config FILE")
	}
	return acpagent.Serve(*configPath, os.Stdin, os.Stdout)
}

func runACPBridge(args []string) error {
	if len(args) != 0 {
		return errors.New("agent-mcp does not accept arguments")
	}
	return acpagent.ServeBridge(os.Stdin, os.Stdout)
}
