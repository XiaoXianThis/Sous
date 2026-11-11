package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
)

// Topics (work/specs/chat-topics) group a thread's messages. A topic has no
// event of its own to start it: the first message without a topic starts
// one, with the message's id, so the projection stays deterministic.

// Topic is one topic of a thread.
type Topic struct {
	ID       string `json:"id"`
	ThreadID string `json:"thread_id"`
	Title    string `json:"title"`
	// Summary is the current conclusion the supervisor keeps, or the
	// closing note of a closed topic.
	Summary   string `json:"summary"`
	Status    string `json:"status"` // open / closed
	FirstSeq  int64  `json:"first_seq"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// TopicTitleRunes bounds a title taken from a message.
const TopicTitleRunes = 30

// startsTopic reports whether a message without a topic starts one: what
// the user (or a trigger) wrote, and news the user is to see (a task's
// report, a notice, an inspector's note). The supervisor's own replies do
// not.
func startsTopic(role, kind string) bool {
	// A direct message to an agent is not addressed to the supervisor and
	// stays where the user is looking; it starts no topic (decision 0028).
	if kind == event.KindDM {
		return false
	}
	if role == "user" {
		return true
	}
	switch kind {
	case event.KindReport, event.KindNotice, event.KindInspector, event.KindMinutes:
		return true
	}
	return false
}

// reopensTopic reports whether a message brings a closed topic back: the
// same messages that start one. A reply of the supervisor after it closed
// the topic does not.
func reopensTopic(role, kind string) bool { return startsTopic(role, kind) }

// TopicTitle is the title of a topic a message starts: the title given, or
// the first line of the content, cut to TopicTitleRunes.
func TopicTitle(title, content string) string {
	t := strings.TrimSpace(title)
	if t == "" {
		for _, line := range strings.Split(content, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				t = line
				break
			}
		}
	}
	t = strings.Join(strings.Fields(t), " ")
	if r := []rune(t); len(r) > TopicTitleRunes {
		t = string(r[:TopicTitleRunes]) + "…"
	}
	if t == "" {
		t = "新话题"
	}
	return t
}

// topicOfMessage returns the topic a new message goes into, starting or
// reopening it as the message asks.
func topicOfMessage(w *Writer, ev event.Event, msgID, role, kind, topicID, title, content string) (string, error) {
	ctx, tx := w.ctx, w.tx
	if topicID == "" {
		var kind, meetingTopic string
		if err := tx.QueryRowContext(ctx, `SELECT kind, meeting_topic FROM threads WHERE id=?`, ev.ThreadID).Scan(&kind, &meetingTopic); err != nil {
			return "", err
		}
		if kind == event.KindMeeting && msgID != meetingTopic {
			topicID = meetingTopic
		}
	}
	if topicID == "" {
		if !startsTopic(role, kind) {
			return "", nil
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO topics(id, thread_id, title, status, first_seq, created_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
			msgID, ev.ThreadID, TopicTitle(title, content), event.TopicOpen, ev.Seq, ev.TS, ev.TS)
		return msgID, err
	}
	reopen := 0
	if reopensTopic(role, kind) {
		reopen = 1
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE topics SET updated_at = ?, status = CASE WHEN ? = 1 THEN 'open' ELSE status END WHERE id = ? AND thread_id = ?`,
		ev.TS, reopen, topicID, ev.ThreadID)
	return topicID, err
}

func projectTopic(w *Writer, ev event.Event) error {
	ctx, tx := w.ctx, w.tx
	switch ev.Type {
	case event.TopicUpdated:
		var p event.TopicUpdatedPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		sets := []string{"updated_at = ?"}
		args := []any{ev.TS}
		if p.Title != nil {
			sets, args = append(sets, "title = ?"), append(args, TopicTitle(*p.Title, ""))
		}
		if p.Summary != nil {
			sets, args = append(sets, "summary = ?"), append(args, strings.TrimSpace(*p.Summary))
		}
		if p.Status != nil {
			sets, args = append(sets, "status = ?"), append(args, *p.Status)
		}
		args = append(args, p.TopicID)
		_, err := tx.ExecContext(ctx, `UPDATE topics SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
		return err
	case event.MessageMoved:
		var p event.MessageMovedPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET topic_id = ? WHERE id = ? AND thread_id = ?`, p.TopicID, p.MessageID, ev.ThreadID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE topics SET updated_at = ?, status = 'open' WHERE id = ? AND thread_id = ?`, ev.TS, p.TopicID, ev.ThreadID)
		return err
	}
	return nil
}

const topicCols = `id, thread_id, title, summary, status, first_seq, created_at, updated_at`

func scanTopics(rows *sql.Rows) ([]Topic, error) {
	defer rows.Close()
	out := []Topic{}
	for rows.Next() {
		var t Topic
		if err := rows.Scan(&t.ID, &t.ThreadID, &t.Title, &t.Summary, &t.Status, &t.FirstSeq, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Topics lists a thread's topics, most recently active first.
func (s *Store) Topics(ctx context.Context, threadID string, limit int) ([]Topic, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+topicCols+` FROM topics WHERE thread_id = ? ORDER BY updated_at DESC, first_seq DESC LIMIT ?`, threadID, limit)
	if err != nil {
		return nil, err
	}
	return scanTopics(rows)
}

// Topic returns one topic.
func (s *Store) Topic(ctx context.Context, id string) (Topic, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+topicCols+` FROM topics WHERE id = ?`, id)
	if err != nil {
		return Topic{}, err
	}
	ts, err := scanTopics(rows)
	if err != nil {
		return Topic{}, err
	}
	if len(ts) == 0 {
		return Topic{}, ErrNotFound
	}
	return ts[0], nil
}

// TopicsByID returns the topics with these ids, by id.
func (s *Store) TopicsByID(ctx context.Context, ids []string) (map[string]Topic, error) {
	out := map[string]Topic{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+topicCols+` FROM topics WHERE id IN (?`+repeat(",?", len(ids)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	ts, err := scanTopics(rows)
	for _, t := range ts {
		out[t.ID] = t
	}
	return out, err
}

// TopicMessages returns up to limit messages of a topic with seq < before
// (0 means newest), oldest first. An empty topic id is the thread's
// messages from before topics.
func (s *Store) TopicMessages(ctx context.Context, threadID, topicID string, before int64, limit int) ([]Message, error) {
	if before <= 0 {
		before = 1 << 62
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, thread_id, role, content, error, reply_to, kind, event_seq, created_at, context, attachments, source, tools, topic_id, task_id FROM (
		   SELECT * FROM messages WHERE thread_id = ? AND topic_id = ? AND event_seq < ? ORDER BY event_seq DESC LIMIT ?
		 ) ORDER BY event_seq`, threadID, topicID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessageRows(rows)
}

// TopicStats is what a topic card shows besides the topic: how many
// messages it has and the newest one that says something.
type TopicStats struct {
	Count int
	Last  *Message
}

// TopicStats counts the messages of a thread's topics and finds the newest
// visible one of each.
func (s *Store) TopicStats(ctx context.Context, threadID string) (map[string]TopicStats, error) {
	out := map[string]TopicStats{}
	rows, err := s.db.QueryContext(ctx, `SELECT topic_id, COUNT(*) FROM messages WHERE thread_id = ? GROUP BY topic_id`, threadID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, err
		}
		out[id] = TopicStats{Count: n}
	}
	rows.Close()
	last, err := s.db.QueryContext(ctx,
		`SELECT id, thread_id, role, content, error, reply_to, kind, event_seq, created_at, context, attachments, source, tools, topic_id, task_id
		 FROM messages m WHERE thread_id = ? AND kind != 'silent' AND content != ''
		   AND event_seq = (SELECT MAX(event_seq) FROM messages x WHERE x.thread_id = m.thread_id AND x.topic_id = m.topic_id AND x.kind != 'silent' AND x.content != '')`,
		threadID)
	if err != nil {
		return nil, err
	}
	defer last.Close()
	msgs, err := scanMessageRows(last)
	if err != nil {
		return nil, err
	}
	for i := range msgs {
		st := out[msgs[i].TopicID]
		st.Last = &msgs[i]
		out[msgs[i].TopicID] = st
	}
	return out, nil
}

// TopicTasks returns the tasks of a thread's topics, by topic.
func (s *Store) TopicTasks(ctx context.Context, topicIDs []string) (map[string][]Task, error) {
	out := map[string][]Task{}
	if len(topicIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(topicIDs))
	for i, id := range topicIDs {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+taskCols+` FROM tasks WHERE topic_id IN (?`+repeat(",?", len(args)-1)+`) ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	tasks, err := s.scanTasks(ctx, rows)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		out[t.TopicID] = append(out[t.TopicID], t)
	}
	return out, nil
}

// Message returns one message of a thread by id.
func (s *Store) Message(ctx context.Context, threadID, id string) (Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, thread_id, role, content, error, reply_to, kind, event_seq, created_at, context, attachments, source, tools, topic_id, task_id
		 FROM messages WHERE thread_id = ? AND id = ?`, threadID, id)
	if err != nil {
		return Message{}, err
	}
	defer rows.Close()
	msgs, err := scanMessageRows(rows)
	if err != nil {
		return Message{}, err
	}
	if len(msgs) == 0 {
		return Message{}, ErrNotFound
	}
	return msgs[0], nil
}

// MessageByID finds a message in any thread.
func (s *Store) MessageByID(ctx context.Context, id string) (Message, error) {
	var thread string
	err := s.db.QueryRowContext(ctx, `SELECT thread_id FROM messages WHERE id = ?`, id).Scan(&thread)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	} else if err != nil {
		return Message{}, err
	}
	return s.Message(ctx, thread, id)
}

// TaskTopic is the topic of a task in a thread, or "" when the task has
// none or its topic is in another thread.
func (s *Store) TaskTopic(ctx context.Context, threadID, taskID string) string {
	if taskID == "" {
		return ""
	}
	var topic string
	err := s.db.QueryRowContext(ctx,
		`SELECT t.topic_id FROM tasks t JOIN topics p ON p.id = t.topic_id WHERE t.id = ? AND p.thread_id = ?`, taskID, threadID).Scan(&topic)
	if err != nil {
		return ""
	}
	return topic
}

// TaskTalk returns the events of a task that carry what the supervisor
// and the task's agents said to each other (chat-topics §5), oldest first:
// runs queued (the prompts), follow-up messages, progress reports, the
// agents' questions, the runs' results, reviews and reports.
func (s *Store) TaskTalk(ctx context.Context, taskID string, limit int) ([]event.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, id, ts, type, actor, IFNULL(thread_id,''), IFNULL(task_id,''), IFNULL(run_id,''), payload FROM events
		 WHERE task_id = ? AND (type IN ('run.queued', 'run.message', 'run.ended', 'task.reviewed', 'trigger.fired', 'message.assistant')
		   OR (type = 'run.update' AND json_extract(payload, '$.kind') = 'status' AND json_extract(payload, '$.text') LIKE '进度：%'))
		 ORDER BY seq LIMIT ?`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []event.Event
	for rows.Next() {
		var ev event.Event
		var typ, payload string
		if err := rows.Scan(&ev.Seq, &ev.ID, &ev.TS, &typ, &ev.Actor, &ev.ThreadID, &ev.TaskID, &ev.RunID, &payload); err != nil {
			return nil, err
		}
		ev.Type = event.Type(typ)
		ev.Payload = []byte(payload)
		out = append(out, ev)
	}
	return out, rows.Err()
}
