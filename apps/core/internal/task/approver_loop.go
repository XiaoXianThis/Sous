package task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/prompts"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/redact"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

var errInvalidVerdict = errors.New("invalid_verdict")
var approvalTools = []provider.ToolSpec{
	{Name: "inspect", Description: "在本次审批所属机器和 Run 目录执行受限只读观察，不能写入或外发。", Schema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)},
	{Name: "read_file", Description: "读取 worktree、命令点名的临时脚本，或精确配置的脱敏投影。node.json 只返回 hub 地址/本地库存在性。", Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":0},"max_bytes":{"type":"integer","minimum":1,"maximum":16384}},"required":["path"],"additionalProperties":false}`)},
}

func (e *Engine) approvalStillPending(ctx context.Context, id string) bool {
	a, err := e.store().Approval(ctx, id)
	if err != nil || a.Status != "pending" {
		return false
	}
	r, err := e.store().Run(ctx, a.RunID)
	return err == nil && !r.Paused && (r.Status == event.RunStatusQueued || r.Status == event.RunStatusRunning)
}
func (e *Engine) askApproval(ctx context.Context, t store.Task, a store.Approval, input string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	req := provider.Request{Model: e.Approver.Name, System: prompts.Approver, Messages: []provider.Message{provider.Text(provider.RoleUser, redact.Mask(input))}}
	total := 0
	for step := 0; step < 6; step++ {
		if !e.approvalStillPending(ctx, a.ID) {
			return store.ErrApprovalClosed
		}
		callsSoFar, err := e.store().ApprovalModelCalls(ctx, a.ID)
		if err != nil {
			return err
		}
		if callsSoFar >= 6 {
			return fmt.Errorf("approval model call budget exhausted")
		}
		called := event.New(event.Type("approval.model_called"), event.ActorApprover, a.ThreadID, map[string]string{"approval_id": a.ID})
		called.RunID = a.RunID
		called.TaskID = a.TaskID
		if _, err := e.Log.Write(ctx, func(w *store.Writer) error {
			if err := w.ReserveApprovalModel(a.ID); err != nil {
				return err
			}
			_, err := w.Append(called)
			return err
		}); err != nil {
			return err
		}
		req.Tools = nil
		if callsSoFar < 5 && a.Facts != nil && a.Facts.Version == 1 {
			req.Tools = approvalTools
		}
		resp, err := e.Approver.Provider.Stream(ctx, req, nil)
		if err != nil {
			return err
		}
		usage := event.New(event.UsageRecorded, event.ActorApprover, t.OriginThreadID, event.UsagePayload{Provider: e.Approver.Provider.Name(), Model: provider.ModelOf(resp, e.Approver.Name), Role: "approver", InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens, CachedTokens: resp.Usage.CachedTokens})
		usage.TaskID = t.ID
		usage.RunID = a.RunID
		if _, err := e.Log.Append(ctx, usage); err != nil {
			return err
		}
		calls := resp.Message.ToolCalls()
		if len(calls) == 0 {
			// Exactly one final JSON object, without prose, fences or second objects.
			text := strings.TrimSpace(resp.Message.PlainText())
			if !strings.HasPrefix(text, "{") || json.Unmarshal([]byte(text), out) != nil {
				return errInvalidVerdict
			}
			return nil
		}
		if step == 5 || len(req.Tools) == 0 {
			return errInvalidVerdict
		}
		if strings.Contains(resp.Message.PlainText(), `"decision"`) {
			return errInvalidVerdict
		}
		req.Messages = append(req.Messages, resp.Message)
		result := provider.Message{Role: provider.RoleTool}
		for _, call := range calls {
			content, err := e.approvalTool(ctx, a, call)
			if err != nil {
				content = redact.Mask(err.Error())
			}
			if len(content) > 16<<10 {
				content = content[:(16<<10)-len(" [truncated]")] + " [truncated]"
			}
			total += len(content)
			if total > 64<<10 {
				return fmt.Errorf("inspection result budget exhausted")
			}
			tr := provider.ToolResult{CallID: call.ID, Content: content, IsError: err != nil}
			result.Parts = append(result.Parts, provider.Part{ToolResult: &tr})
		}
		req.Messages = append(req.Messages, result)
	}
	return errInvalidVerdict
}
func (e *Engine) approvalTool(ctx context.Context, a store.Approval, c provider.ToolCall) (string, error) {
	if c.Name != "inspect" && c.Name != "read_file" {
		return "", fmt.Errorf("unknown approval tool")
	}
	if len(c.Args) > 8192 {
		return "", fmt.Errorf("tool argument budget")
	}
	var args map[string]any
	if json.Unmarshal(c.Args, &args) != nil {
		return "", fmt.Errorf("invalid tool arguments")
	}
	// Canonical JSON; model cannot vary key order to defeat once-only execution.
	raw, _ := json.Marshal(args)
	sum := sha256.Sum256(append([]byte(a.ID+"\x00"+c.Name+"\x00"), raw...))
	key := hex.EncodeToString(sum[:])
	if old, err := e.store().ApprovalCheck(ctx, key); err == nil {
		if old.Result == "" {
			return "", fmt.Errorf("previous inspection incomplete; will not repeat")
		}
		if strings.HasPrefix(old.Result, "inspection failed:") {
			return old.Result, fmt.Errorf("previous inspection failed")
		}
		return old.Result, nil
	}
	payload := event.ApprovalCheckPayload{CheckID: key, ApprovalID: a.ID, Tool: c.Name, ArgsDigest: key, Summary: "查过：" + c.Name + "（" + redact.Mask(string(raw)) + "）"}
	ev := event.New(event.ApprovalChecked, event.ActorApprover, a.ThreadID, payload)
	ev.RunID = a.RunID
	ev.TaskID = a.TaskID
	if _, err := e.Log.Write(ctx, func(w *store.Writer) error {
		if err := w.ReserveApprovalCheck(key, a.ID, a.RunID); err != nil {
			return err
		}
		_, err := w.Append(ev)
		return err
	}); err != nil {
		return "", err
	}
	toolCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	started := time.Now()
	res, err := e.Nodes.ApprovalInspect(toolCtx, a.ID, c.Name, raw)
	payload.DurationMS = time.Since(started).Milliseconds()
	payload.Failed = err != nil
	text := ""
	if err != nil {
		text = "inspection failed: " + redact.Mask(err.Error())
	} else {
		b, _ := json.Marshal(res)
		text = redact.Mask(string(b))
	}
	if len(text) > 16<<10 {
		payload.Truncated = true
		text = text[:(16<<10)-len(" [truncated]")] + " [truncated]"
	}
	payload.Result = text
	payload.Bytes = len(text)
	resultSum := sha256.Sum256([]byte(text))
	payload.ResultDigest = hex.EncodeToString(resultSum[:])
	done := event.New(event.ApprovalChecked, event.ActorApprover, a.ThreadID, payload)
	done.RunID = a.RunID
	done.TaskID = a.TaskID
	if _, e2 := e.Log.Append(ctx, done); e2 != nil {
		return "", e2
	}
	return text, err
}

func (e *Engine) decideApproval(ctx context.Context, id, option, note, by string) error {
	if !e.approvalStillPending(ctx, id) {
		return nil
	}
	note, err := e.inspectionNote(ctx, id, note)
	if err != nil {
		return err
	}
	err = e.Nodes.DecideAs(ctx, id, option, note, by)
	if errors.Is(err, store.ErrApprovalBudget) {
		return e.escalateApproval(ctx, id, "这次执行里审批 agent 的批准额度已用完，请你决定")
	}
	if errors.Is(err, store.ErrApprovalClosed) {
		return nil
	}
	return err
}

func (e *Engine) inspectionNote(ctx context.Context, id, note string) (string, error) {
	summary, err := e.store().ApprovalCheckSummary(ctx, id)
	if err != nil {
		return "", err
	}
	if summary != "" {
		note += "\n" + summary
	}
	return redact.Mask(note), nil
}
func (e *Engine) escalateApproval(ctx context.Context, id, reason string) error {
	if !e.approvalStillPending(ctx, id) {
		return nil
	}
	note, err := e.inspectionNote(ctx, id, reason)
	if err != nil {
		return err
	}
	return e.Nodes.Escalate(ctx, id, note)
}
