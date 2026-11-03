// Command sous is the client on each of the user's machines.
//
//	sous pair <hub-url> <code> [--name NAME]   pair this machine with a Hub
//	sous daemon                               run the daemon in the foreground
//	sous status                               show pairing and agents
//	sous agents                               check which coding agents are usable
//	sous service install|uninstall|start|stop|restart|status
//	sous unpair                               forget the pairing on this machine
//	sous version
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
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/kardianos/service"

	"github.com/XiaoXianThis/Sous/apps/core/internal/agents"
	"github.com/XiaoXianThis/Sous/apps/core/internal/node"
	"github.com/XiaoXianThis/Sous/apps/core/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sous:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  sous pair <hub-url> <code> [--name NAME]
  sous daemon
  sous status
  sous agents
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
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	home := fs.String("home", "", "directory for sous files")
	name := fs.String("name", "", "machine name shown in sous (pair)")
	debug := fs.Bool("debug", false, "log debug messages")
	if err := fs.Parse(reorder(rest)); err != nil {
		return err
	}
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
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
			return errors.New("usage: sous pair <hub-url> <code> [--name NAME]")
		}
		cfg, err := node.Pair(context.Background(), root, fs.Arg(0), fs.Arg(1), *name)
		if err != nil {
			return err
		}
		fmt.Printf("已配对：%s（id %s）连接到 %s\n", cfg.Name, cfg.NodeID, cfg.HubURL)
		fmt.Println("下一步：运行 `sous service install` 让它开机自动运行，或者先用 `sous daemon` 在前台试一下。")
		return nil
	case "daemon":
		return daemon(root)
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

// reorder moves flags in front of positional arguments, so
// `sous pair url code --name x` works like `sous pair --name x url code`.
func reorder(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && a != "--debug" && a != "-debug" && i+1 < len(args) {
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
	specs := agents.Builtin()
	if cfg, err := node.LoadConfig(root); err == nil {
		for i, s := range specs {
			if cmd, ok := cfg.Agents[s.Name]; ok && len(cmd) > 0 {
				specs[i].Launch = cmd
			}
		}
	}
	return specs
}

func daemon(root string) error {
	cfg, err := node.LoadConfig(root)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("sous daemon starting", "hub", cfg.HubURL, "node", cfg.NodeID, "home", root)
	return node.NewDaemon(root, cfg, agents.Builtin()).Run(ctx)
}

func status(root string) error {
	cfg, err := node.LoadConfig(root)
	if err != nil {
		return err
	}
	fmt.Printf("机器：%s（id %s）\nHub：%s\n文件：%s\n\n", cfg.Name, cfg.NodeID, cfg.HubURL, root)
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
