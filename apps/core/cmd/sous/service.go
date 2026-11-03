package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/kardianos/service"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

type agentsInfo = proto.AgentInfo

// program adapts the daemon to the service manager; `sous daemon` runs the
// same code in the foreground, so the service only needs to start it.
type program struct{}

func (program) Start(service.Service) error { return nil }
func (program) Stop(service.Service) error  { return nil }

// newService describes the daemon to the platform's service manager:
// launchd on macOS (a per-user LaunchAgent), systemd on Linux (a user
// service), the Service Control Manager on Windows.
func newService(root string) (service.Service, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cfg := &service.Config{
		Name:        "sous",
		DisplayName: "sous daemon",
		Description: "Runs coding agents for sous on this machine.",
		Executable:  exe,
		Arguments:   []string{"daemon", "--home", root},
		Option: service.KeyValue{
			"KeepAlive":              true, // launchd
			"RunAtLoad":              true, // launchd
			"Restart":                "always",
			"OnFailure":              "restart", // windows
			"OnFailureDelayDuration": "5s",
		},
	}
	if runtime.GOOS != "windows" {
		// Coding agents keep their logins in the user's home and keychain,
		// so the daemon runs as the user, not as root.
		cfg.Option["UserService"] = true
	}
	return service.New(program{}, cfg)
}

func serviceCommand(root, action string) error {
	svc, err := newService(root)
	if err != nil {
		return err
	}
	switch action {
	case "status":
		st, err := svc.Status()
		if err != nil {
			return err
		}
		fmt.Println(map[service.Status]string{service.StatusRunning: "运行中", service.StatusStopped: "已停止", service.StatusUnknown: "未知"}[st])
		return nil
	case "install", "uninstall", "start", "stop", "restart":
		if err := service.Control(svc, action); err != nil {
			return err
		}
		fmt.Printf("sous 服务：%s 完成\n", action)
		if action == "install" {
			fmt.Println("下一步：sous service start")
		}
		return nil
	}
	return fmt.Errorf("unknown service action %q", action)
}
