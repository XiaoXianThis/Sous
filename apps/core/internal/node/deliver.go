package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/policy"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/workspace"
)

// deliver commits what is left in the task's worktree, pushes its branch and
// opens a pull request (docs/decisions/0009). The push goes through the same
// policy as an agent's actions: git-remote, so it is approved according to
// the run's permission mode. sous never merges the pull request.
func (r *run) deliver() {
	if r.spec.RepoPath == "" || r.spec.TaskID == "" {
		r.end("failed", "", "", "", errors.New("只有代码任务能交付"))
		return
	}
	ws, err := r.prepare()
	if err != nil {
		r.end("failed", "", "", "", fmt.Errorf("找不到任务的 worktree：%w", err))
		return
	}
	r.markStarted(ws)
	title := strings.TrimSpace(r.spec.Title)
	if title == "" {
		title = "sous: " + ws.Branch
	}

	r.status("正在提交剩下的改动")
	if _, err := workspace.Commit(r.ctx, ws, title); err != nil {
		r.end("failed", "", "", "", fmt.Errorf("提交失败：%w", err))
		return
	}
	commits := workspace.Commits(r.ctx, ws)
	if commits == "" {
		r.end("failed", "", "", "", errors.New("分支上没有任何改动，没有可以交付的东西"))
		return
	}
	_, stat, _ := workspace.Diff(r.ctx, ws)

	push := "git push -u origin " + ws.Branch
	if r.spec.Force {
		push = "git push --force-with-lease -u origin " + ws.Branch
	}
	raw, _ := json.Marshal(map[string]string{"command": push})
	cat, why := r.d.policy.Classify(policy.Request{ToolKind: "execute", RawInput: raw, Workdir: ws.Dir})
	ask := fmt.Sprintf("push 分支 %s 并创建 PR", ws.Branch)
	detail := fmt.Sprintf("将执行：\n  %s\n  gh pr create --head %s", push, ws.Branch)
	if ws.BaseBranch != "" {
		detail += " --base " + ws.BaseBranch
	}
	detail += "\n\n提交：\n" + commits
	if stat != "" {
		detail += "\n\n" + stat
	}
	options := []proto.Option{{OptionID: "allow", Name: "批准", Kind: "allow_once"}, {OptionID: "reject", Name: "拒绝", Kind: "reject_once"}}
	switch policy.Decide(r.spec.PermissionMode, cat) {
	case policy.Deny:
		r.recordPolicy(ask+"（"+why+"）", cat, "deny")
		r.end("failed", "", "", stat, errors.New("底线规则拒绝了 push："+why))
		return
	case policy.Allow:
		r.recordPolicy(ask, cat, "allow")
	default:
		review := "user"
		if policy.Decide(r.spec.PermissionMode, cat) == policy.AskAI {
			review = "ai"
		}
		if review == "ai" {
			r.status("等审批 agent 判断能否 push")
		} else {
			r.status("等你批准 push")
		}
		if opt := r.ask(ask, "execute", detail, cat, review, options); opt != "allow" {
			if r.ctx.Err() != nil {
				r.end("cancelled", "", "", stat, errors.New(r.stopReasonText()))
			} else {
				r.end("cancelled", "", "", stat, errors.New("没有获得 push 的许可"))
			}
			return
		}
	}

	r.status("正在 push " + ws.Branch)
	if err := workspace.Push(r.ctx, ws, r.spec.Force); err != nil {
		r.end(r.failureStatus(), "", "", stat, fmt.Errorf("push 失败：%w", err))
		return
	}
	r.status("正在创建 PR")
	url, conflict, err := workspace.PullRequest(r.ctx, ws, title, r.prBody(commits))
	if err != nil {
		r.end(r.failureStatus(), "", "", stat, err)
		return
	}
	summary := fmt.Sprintf("已 push %s，PR：%s", ws.Branch, url)
	if conflict {
		summary += "（和目标分支有冲突）"
	}
	r.d.ob.reliable(proto.New(proto.TRunEnded, proto.RunEnded{
		RunID: r.spec.RunID, Status: "succeeded", Summary: summary, DiffStat: stat, PRURL: url, Conflict: conflict,
	}))
	slog.Info("daemon: delivered", "run", r.spec.RunID, "pr", url, "conflict", conflict)
}

func (r *run) failureStatus() string {
	if r.ctx.Err() != nil {
		return "cancelled"
	}
	return "failed"
}

func (r *run) prBody(commits string) string {
	var b strings.Builder
	if p := strings.TrimSpace(r.spec.Prompt); p != "" {
		b.WriteString(p + "\n\n")
	}
	b.WriteString("## Commits\n\n```\n" + commits + "\n```\n\n")
	b.WriteString("_由 [sous](https://github.com/XiaoXianThis/Sous) 创建。sous 不会合并这个 PR，请评审后自行合并。_\n")
	return b.String()
}
