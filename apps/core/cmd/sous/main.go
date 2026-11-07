// Command sous is the client on each of the user's machines.
//
//	sous pair <hub-url> <code> [--name NAME] [--role server] [--workspace DIR]   pair this machine with a Hub
//	sous daemon                               run the daemon in the foreground
//	sous status                               show pairing and agents
//	sous agents                               check which coding agents are usable
//	sous chat [--thread ID]                   chat with sous in the terminal, through the daemon
//	sous update [--check] [--version vX.Y.Z]  install the latest release (through the daemon when it runs)
//	sous service install|uninstall|start|stop|restart|status
//	sous unpair                               forget the pairing on this machine
//	sous version
//	sous mcp --home DIR --run ID --token T    sous's tools for a sub-agent (started by the agent, not by hand)
//
// --home DIR (or SOUS_HOME) changes where sous keeps its files (default ~/.sous).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/kardianos/service"

	"github.com/XiaoXianThis/Sous/apps/core/internal/agents"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ipc"
	"github.com/XiaoXianThis/Sous/apps/core/internal/node"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sousmcp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/update"
	"github.com/XiaoXianThis/Sous/apps/core/internal/version"
	"github.com/XiaoXianThis/Sous/apps/core/internal/watchdog"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sous:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  sous pair <hub-url> <code> [--name NAME] [--role dev|server] [--workspace DIR]
  sous daemon
  sous status
  sous agents
  sous chat [--thread ID]
  sous update [--check] [--version vX.Y.Z]
  sous service install|uninstall|start|stop|restart|status
  sous unpair
  sous version

Every command accepts --home DIR (default ~/.sous, or $SOUS_HOME).
`)
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("missing command")
	}
	cmd, rest := args[0], args[1:]
	if cmd == "mcp" {
		// Its stdout is the MCP stream: nothing else may write to it.
		if code := sousmcp.Main(rest); code != 0 {
			os.Exit(code)
		}
		return nil
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	home := fs.String("home", "", "directory for sous files")
	name := fs.String("name", "", "machine name shown in sous (pair)")
	role := fs.String("role", "", "dev (default) or server (pair)")
	wsFlag := fs.String("workspace", "", "directory searched for git repositories, comma-separated (pair)")
	debug := fs.Bool("debug", false, "log debug messages")
	thread := fs.String("thread", "", "thread id (chat; default: the main thread)")
	check := fs.Bool("check", false, "only check for a newer release (update)")
	ver := fs.String("version", "", "release tag to install (update; default: the latest)")
	if err := fs.Parse(reorder(rest)); err != nil {
		return err
	}
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	logs := node.NewLogRing(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(slog.New(logs))
	if *home != "" {
		os.Setenv("SOUS_HOME", *home)
	}
	root, err := node.Root()
	if err != nil {
		return err
	}

	switch cmd {
	case "version":
		fmt.Println(version.Version)
		return nil
	case "pair":
		if fs.NArg() != 2 {
			return errors.New("usage: sous pair <hub-url> <code> [--name NAME] [--role dev|server] [--workspace DIR]")
		}
		if *role != "" && *role != "dev" && *role != "server" {
			return errors.New("--role must be dev or server")
		}
		var wss []string
		for _, w := range strings.Split(*wsFlag, ",") {
			if w = strings.TrimSpace(w); w != "" {
				wss = append(wss, w)
			}
		}
		cfg, err := node.PairWith(context.Background(), root, fs.Arg(0), fs.Arg(1), *name, node.PairOptions{Role: *role, Workspaces: wss})
		if err != nil {
			return err
		}
		fmt.Printf("已配对：%s（id %s）连接到 %s\n", cfg.Name, cfg.NodeID, cfg.HubURL)
		fmt.Println("下一步：运行 `sous service install` 让它开机自动运行，或者先用 `sous daemon` 在前台试一下。")
		return nil
	case "daemon":
		return daemon(root, logs)
	case "chat":
		return chat(root, *thread)
	case "update":
		return updateCommand(root, *check, *ver)
	case "status":
		return status(root)
	case "agents":
		printAgents(agents.Detect(context.Background(), agentSpecs(root)))
		return nil
	case "unpair":
		if err := os.Remove(root + string(os.PathSeparator) + "node.json"); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Println("已在这台电脑上取消配对。记得也在 sous 的“机器”页面里删除它。")
		return nil
	case "service":
		if fs.NArg() != 1 {
			return errors.New("usage: sous service install|uninstall|start|stop|restart|status")
		}
		return serviceCommand(root, fs.Arg(0))
	case "help", "-h", "--help":
		usage()
		return nil
	}
	usage()
	return fmt.Errorf("unknown command %q", cmd)
}

var boolFlag = map[string]bool{"debug": true, "check": true}

// reorder moves flags in front of positional arguments, so
// `sous pair url code --name x` works like `sous pair --name x url code`.
func reorder(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && !boolFlag[strings.TrimLeft(a, "-")] && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func agentSpecs(root string) []agents.Spec {
	agents.AugmentPath()
	specs := agents.Builtin()
	if cfg, err := node.LoadConfig(root); err == nil {
		specs = node.EffectiveSpecs(specs, cfg)
	}
	return specs
}

func daemon(root string, logs *node.LogRing) error {
	cfg, err := node.LoadConfig(root)
	if err != nil {
		return err
	}
	update.CleanOld(exePath())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	slog.Info("sous daemon starting", "hub", cfg.HubURL, "node", cfg.NodeID, "home", root, "version", version.Version)
	agents.AugmentPath() // a service manager's PATH often lacks where agents live
	d := node.NewDaemon(root, cfg, agents.Builtin())
	// SOUS_UPDATE_API points updates at another GitHub API (for testing).
	d.UpdateAPI = os.Getenv("SOUS_UPDATE_API")
	restart := false
	d.OnUpdated = func(v string) {
		slog.Info("sous daemon: updated; restarting", "version", v)
		restart = true
		cancel()
	}
	if err := d.ServeIPC(ctx, logs); err != nil {
		return err
	}
	// A stuck daemon exits so its service manager starts it again; runs
	// in progress are saved and resumed by the next one.
	wd := watchdog.New()
	wd.Probe("daemon", 2*time.Minute, d.Probe)
	go wd.Run(ctx)
	err = d.Run(ctx)
	if restart {
		return restartSelf()
	}
	return err
}

func exePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		return r
	}
	return exe
}

// restartSelf starts the new executable after an update. Under a service
// manager the process exits with a failure code, which every service
// manager answers with a restart; in a terminal it replaces itself.
func restartSelf() error {
	if !service.Interactive() {
		slog.Info("sous daemon: exiting so the service manager starts the new version")
		os.Exit(3)
	}
	return execSelf()
}

func updateCommand(root string, checkOnly bool, tag string) error {
	ctx := context.Background()
	if c, err := ipc.Dial(ipc.Path(root)); err == nil {
		defer c.Close()
		var info struct {
			Current, Latest string
			Newer           bool
			NotesURL        string `json:"notes_url"`
		}
		if err := c.Call(ctx, "update.check", nil, &info); err != nil {
			return err
		}
		fmt.Printf("当前版本：%s，最新版本：%s\n", info.Current, info.Latest)
		if checkOnly {
			return nil
		}
		if !info.Newer && tag == "" && info.Current != "dev" {
			fmt.Println("已经是最新版本。")
			return nil
		}
		var res struct{ Version string }
		if err := c.Call(ctx, "update.apply", map[string]string{"version": tag}, &res); err != nil {
			return err
		}
		fmt.Printf("已安装 %s，daemon 正在重启。\n", res.Version)
		return nil
	}
	cl := update.Client{}
	rel, err := cl.Release(ctx, tag)
	if err != nil {
		return err
	}
	fmt.Printf("当前版本：%s，最新版本：%s\n", version.Version, rel.Tag)
	if checkOnly {
		return nil
	}
	if tag == "" && !update.Newer(rel.Tag, version.Version) && version.Version != "dev" {
		fmt.Println("已经是最新版本。")
		return nil
	}
	installed, err := cl.Install(ctx, rel.Tag, exePath())
	if err != nil {
		return err
	}
	fmt.Printf("已安装 %s。如果 sous 作为系统服务在运行，执行 `sous service restart` 让它生效。\n", installed)
	return nil
}

func status(root string) error {
	cfg, err := node.LoadConfig(root)
	if err != nil {
		return err
	}
	fmt.Printf("机器：%s（id %s）\nHub：%s\n文件：%s\n\n", cfg.Name, cfg.NodeID, cfg.HubURL, root)
	if c, err := ipc.Dial(ipc.Path(root)); err == nil {
		var st node.Status
		if c.Call(context.Background(), "status", nil, &st) == nil {
			conn := map[bool]string{true: "已连接 Hub", false: "未连接 Hub"}[st.HubConnected]
			fmt.Printf("daemon：运行中（%s，%s，正在执行 %d 个 Run，待发送 %d 帧）\n\n", st.Version, conn, len(st.Runs), st.Pending)
		}
		c.Close()
	} else {
		fmt.Printf("daemon：没有在运行\n\n")
	}
	if svc, err := newService(root); err == nil {
		if st, err := svc.Status(); err == nil {
			fmt.Printf("系统服务：%s\n\n", map[service.Status]string{service.StatusRunning: "运行中", service.StatusStopped: "已停止"}[st])
		} else {
			fmt.Printf("系统服务：未安装（%v）\n\n", err)
		}
	}
	printAgents(agents.Detect(context.Background(), agentSpecs(root)))
	return nil
}

func printAgents(infos []agentsInfo) {
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "agent\t已安装\t版本\t登录\t说明")
	for _, a := range infos {
		installed, login := "否", "-"
		if a.Installed {
			installed = "是"
			login = "未知"
			if a.LoggedIn != nil {
				login = map[bool]string{true: "已登录", false: "未登录"}[*a.LoggedIn]
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", a.Name, installed, a.Version, login, a.Note)
	}
	w.Flush()
}
