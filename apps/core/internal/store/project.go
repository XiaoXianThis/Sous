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
	case event.ApprovalChecked:
		return projectApprovalCheck(w, ev)
	case event.ThreadCreated:
		var p event.ThreadCreatedPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO threads(id, title, kind, project_id, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
			ev.ThreadID, p.Title, p.Kind, p.ProjectID, ev.TS, ev.TS)
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
		if !p.Context.Empty() {
			raw, _ := json.Marshal(p.Context)
			mctx = string(raw)
		}
		atts := ""
		if len(p.Attachments) > 0 {
			raw, _ := json.Marshal(p.Attachments)
			atts = string(raw)
		}
		source := ""
		if p.Source != nil {
			raw, _ := json.Marshal(p.Source)
			source = string(raw)
		}
		tools := ""
		if len(p.Tools) > 0 {
			raw, _ := json.Marshal(p.Tools)
			tools = string(raw)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO messages(id, thread_id, role, content, error, reply_to, kind, event_seq, created_at, context, attachments, source, tools) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			p.MessageID, ev.ThreadID, role, p.Content, p.Error, p.ReplyTo, p.Kind, ev.Seq, ev.TS, mctx, atts, source, tools); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE threads SET updated_at = ? WHERE id = ?`, ev.TS, ev.ThreadID)
		return err

	case event.MessageTools:
		var p event.MessageToolsPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		tools := ""
		if len(p.Tools) > 0 {
			raw, _ := json.Marshal(p.Tools)
			tools = string(raw)
		}
		_, err := tx.ExecContext(ctx, `UPDATE messages SET tools = ? WHERE id = ? AND thread_id = ?`, tools, p.MessageID, ev.ThreadID)
		return err

	case event.ThreadCompacted:
		var p event.CompactedPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		profile, _ := json.Marshal(nonNilSlice(p.Profile))
		_, err := tx.ExecContext(ctx,
			`INSERT INTO thread_segments(thread_id, segment, covers_up_to, summary, profile, reason, model, input_tokens, event_seq, created_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?)
			 ON CONFLICT(thread_id, segment) DO UPDATE SET covers_up_to = excluded.covers_up_to, summary = excluded.summary,
			   profile = excluded.profile, reason = excluded.reason, model = excluded.model,
			   input_tokens = excluded.input_tokens, event_seq = excluded.event_seq, created_at = excluded.created_at`,
			ev.ThreadID, p.Segment, p.CoversUpTo, p.Summary, string(profile), p.Reason, p.Model, p.InputTokens, ev.Seq, ev.TS)
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
		event.RunTurn, event.ConsultClosed,
		event.ApprovalRequested, event.ApprovalDecided, event.ApprovalEscalated:
		return projectRun(w, ev)

	case event.SchedulePut, event.ScheduleDeleted, event.TriggerFired, event.InspectorFinding:
		return projectAuto(w, ev)

	case event.TaskCreated, event.TaskUpdated, event.TaskStatus, event.TaskReviewed, event.TaskGit,
		event.ProjectPut, event.ProjectDeleted, event.ProjectArchived, event.ProjectDeployed:
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
