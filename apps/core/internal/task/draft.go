package task

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

// FileSupervisorDraft permits one durable draft per human turn. Its stable ID
// prevents duplicate drafts on lost tool responses or process restart. Unlike
// Create, this never dispatches, assigns a machine, or reopens a project.
func (e *Engine) FileSupervisorDraft(ctx context.Context, projectID, title, detail string) (store.Task, error) {
	turn := tool.TurnFrom(ctx)
	title, detail = strings.TrimSpace(title), strings.TrimSpace(detail)
	if turn.ThreadID == "" || turn.FirstSeq <= 0 || title == "" || len(title) > 500 || len(detail) > 12000 {
		return store.Task{}, fmt.Errorf("%w: 需要有效的回合和任务标题（说明最多 12000 字节）", ErrInvalid)
	}
	if turn.ProjectID != "" {
		if projectID != "" && projectID != turn.ProjectID {
			return store.Task{}, fmt.Errorf("%w: 只能在当前项目创建草稿", ErrInvalid)
		}
		projectID = turn.ProjectID
	}
	if projectID == "" {
		projectID = "misc"
	}
	hash := sha256.Sum256(fmt.Appendf(nil, "sous:supervisor-draft:%s:%d", turn.ThreadID, turn.FirstSeq))
	var key ulid.ULID
	copy(key[:], hash[:])
	id := key.String()
	// Tool calls for the same turn go through one serial capability broker.
	// This query is also used when an unanswered turn is resumed after restart.
	if t, err := e.store().Task(ctx, id); err == nil {
		if t.OriginThreadID != turn.ThreadID || t.ProjectID != projectID || t.Title != title || t.Detail != detail {
			return store.Task{}, fmt.Errorf("本轮已登记一张任务草稿（%s），不会再重复创建", id)
		}
		return t, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.Task{}, err
	}
	p, err := e.store().Project(ctx, projectID)
	if err != nil || p.Archived() {
		return store.Task{}, fmt.Errorf("%w: 请选择现有的未归档项目", ErrInvalid)
	}
	ev := event.New(event.TaskCreated, event.ActorSupervisor, turn.ThreadID, event.TaskCreatedPayload{
		Kind: event.TaskKindGeneral, Title: title, Detail: detail, ProjectID: projectID,
		Status: event.TaskInbox, Review: event.ReviewNone, TopicID: turn.TopicID,
	})
	ev.TaskID = id
	if _, err := e.Log.Append(ctx, ev); err != nil {
		return store.Task{}, err
	}
	return e.store().Task(ctx, id)
}

// FileExternalDraft is a bounded planning action for one authorized external
// connection. The grant gate runs inside the same write transaction as TaskCreated,
// so revocation cannot complete and then be followed by an in-flight draft.
func (e *Engine) FileExternalDraft(ctx context.Context, grant, project, requestID, title, detail string, authorize func(*sql.Tx) error) (store.Task, error) {
	title, detail = strings.TrimSpace(title), strings.TrimSpace(detail)
	if authorize == nil || grant == "" || project == "" || requestID == "" || len(requestID) > 64 || title == "" || len(title) > 500 || len(detail) > 12000 || strings.ContainsRune(title+detail, 0) {
		return store.Task{}, fmt.Errorf("%w: invalid external task draft", ErrInvalid)
	}
	hash := sha256.Sum256(fmt.Appendf(nil, "sous:external-mcp-draft:%s:%s", grant, requestID))
	var key ulid.ULID
	copy(key[:], hash[:])
	id := key.String()
	_, err := e.Log.Write(ctx, func(w *store.Writer) error {
		tx := w.Tx()
		if err := authorize(tx); err != nil {
			return err
		}
		var previousProject, previousTitle, previousDetail string
		err := tx.QueryRowContext(ctx, `SELECT project_id,title,detail FROM tasks WHERE id=?`, id).Scan(&previousProject, &previousTitle, &previousDetail)
		if err == nil {
			if previousProject != project || previousTitle != title || previousDetail != detail {
				return fmt.Errorf("%w: external request_id already used", ErrInvalid)
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		ev := event.New(event.TaskCreated, "external-mcp", "", event.TaskCreatedPayload{
			Kind: event.TaskKindGeneral, Title: title, Detail: detail, ProjectID: project, Status: event.TaskInbox, Review: event.ReviewNone,
		})
		ev.TaskID = id
		_, err = w.Append(ev)
		return err
	})
	if err != nil {
		return store.Task{}, err
	}
	return e.store().Task(ctx, id)
}
