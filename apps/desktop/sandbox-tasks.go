package main

import (
	"context"
	"errors"
	"time"
)

type sandboxIntent struct {
	ID     string `json:"id"`
	TaskID string `json:"task_id"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

func (s *sandboxHost) refreshTasks(ctx context.Context) error {
	var tasks []sandboxIntent
	if s.hub == nil {
		return errNoSandboxPipe
	}
	err := s.hub.dispatch(ctx, sandboxPipeRequest{Action: "list"}, &tasks)
	s.tasksOnline = err == nil
	if err != nil && !errors.Is(err, errNoSandboxPipe) {
		return err
	}
	s.tasks = tasks
	if s.task.ID != "" {
		found := false
		for _, x := range tasks {
			if x.ID == s.task.ID {
				found = true
				break
			}
		}
		if !found {
			s.task = sandboxIntent{}
			s.grant = sandboxGrant{}
		}
	}
	return nil
}

func (s *sandboxHost) selectTask(ctx context.Context, id string) error {
	if err := s.refreshTasks(ctx); err != nil {
		return err
	}
	if id == s.task.ID {
		return nil
	}
	if id == "" {
		s.task = sandboxIntent{}
		s.grant = sandboxGrant{}
		return nil
	}
	for _, x := range s.tasks {
		if x.ID == id {
			s.task = x
			s.grant = sandboxGrant{}
			return nil
		}
	}
	return errors.New("工作台请求已撤销、过期或变更，请重新选择")
}

// monitorTask is only active while actually running. A lost or revoked Hub
// claim cancels this managed Guest; it never renews host path authorization.
func (s *sandboxHost) monitorTask(ctx context.Context, id, lease string, cancel context.CancelFunc) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			var stop bool
			err := s.hub.dispatch(ctx, sandboxPipeRequest{Action: "heartbeat", Dispatch: id, Lease: lease}, &stop)
			if err != nil || stop {
				cancel()
				return
			}
		}
	}()
	return done
}

func (s *sandboxHost) settleTask(id, lease, outcome string, changes int) error {
	if id == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var record struct {
		Status string `json:"status"`
	}
	err := s.hub.dispatch(ctx, sandboxPipeRequest{Action: "complete", Dispatch: id, Lease: lease, Outcome: outcome, Changes: changes}, &record)
	_ = s.refreshTasks(ctx)
	if err != nil || record.Status != outcome {
		return errors.New("未能确认工作台运行状态；如在本机已生成报告，可继续审阅，不会自动重跑")
	}
	return nil
}
