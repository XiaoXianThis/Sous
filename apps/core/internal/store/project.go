package store

import (
	"encoding/json"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
)

// project updates the views for one stored event, inside the write transaction.
func project(w *Writer, ev event.Event) error {
	ctx, tx := w.ctx, w.tx
	if err := enqueueEffects(w, ev); err != nil {
		return err
	}
	switch ev.Type {
	case event.ThreadCreated:
		var p event.ThreadCreatedPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO threads(id, title, kind, created_at, updated_at) VALUES (?,?,?,?,?)`,
			ev.ThreadID, p.Title, p.Kind, ev.TS, ev.TS)
		return err

	case event.MessageUser, event.MessageAssistant:
		var p event.MessagePayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		role := "user"
		if ev.Type == event.MessageAssistant {
			role = "assistant"
		}
		mctx := ""
		if p.Context != nil && (p.Context.ProjectID != "" || p.Context.TaskID != "") {
			raw, _ := json.Marshal(p.Context)
			mctx = string(raw)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO messages(id, thread_id, role, content, error, reply_to, kind, event_seq, created_at, context) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			p.MessageID, ev.ThreadID, role, p.Content, p.Error, p.ReplyTo, p.Kind, ev.Seq, ev.TS, mctx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE threads SET updated_at = ? WHERE id = ?`, ev.TS, ev.ThreadID)
		return err

	case event.MemoryPut:
		var p event.MemoryPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO memories(id, kind, content, source_event, created_at, updated_at) VALUES (?,?,?,?,?,?)
			 ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, content = excluded.content,
			   source_event = excluded.source_event, updated_at = excluded.updated_at`,
			p.MemoryID, p.Kind, p.Content, ev.Seq, ev.TS, ev.TS)
		return err

	case event.MemoryDeleted:
		var p event.MemoryPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, p.MemoryID)
		return err

	case event.NodePaired, event.NodeUpdated, event.NodeRevoked, event.NodeOnline, event.NodeOffline, event.NodeInventory,
		event.RunQueued, event.RunStarted, event.RunUpdate, event.RunEnded,
		event.RunPaused, event.RunResumed, event.RunMessage, event.RunMessageDelivered, event.RunArchived,
		event.ApprovalRequested, event.ApprovalDecided, event.ApprovalEscalated:
		return projectRun(w, ev)

	case event.SchedulePut, event.ScheduleDeleted, event.TriggerFired, event.InspectorFinding:
		return projectAuto(w, ev)

	case event.TaskCreated, event.TaskUpdated, event.TaskStatus, event.TaskReviewed,
		event.ProjectPut, event.ProjectDeleted:
		return projectTask(w, ev)

	case event.PersonaPut, event.PersonaDeleted, event.NodeRepos, event.NodeRole:
		return projectWorkbench(w, ev)

	case event.UsageRecorded:
		var p event.UsagePayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO usage(event_seq, ts, provider, model, role, input_tokens, output_tokens, cached_tokens, thread_id, task_id, run_id)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			ev.Seq, ev.TS, p.Provider, p.Model, p.Role, p.InputTokens, p.OutputTokens, p.CachedTokens,
			null(ev.ThreadID), null(ev.TaskID), null(ev.RunID))
		return err
	}
	return nil
}
