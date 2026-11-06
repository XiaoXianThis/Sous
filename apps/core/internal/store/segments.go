package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
)

// Segment is one context segment of a thread (long-main-thread): the model
// sees Summary in place of the messages up to CoversUpTo, plus every message
// after it.
type Segment struct {
	ThreadID    string   `json:"thread_id"`
	Segment     int      `json:"segment"`
	CoversUpTo  int64    `json:"covers_up_to"`
	Summary     string   `json:"summary"`
	Profile     []string `json:"profile"`
	Reason      string   `json:"reason"`
	Model       string   `json:"model,omitempty"`
	InputTokens int      `json:"input_tokens,omitempty"`
	Seq         int64    `json:"seq"`
	CreatedAt   int64    `json:"created_at"`
}

// LatestSegment returns the thread's current segment, or ErrNotFound when
// the thread has none yet.
func (s *Store) LatestSegment(ctx context.Context, threadID string) (Segment, error) {
	var g Segment
	var profile string
	err := s.db.QueryRowContext(ctx,
		`SELECT thread_id, segment, covers_up_to, summary, profile, reason, model, input_tokens, event_seq, created_at
		 FROM thread_segments WHERE thread_id = ? ORDER BY segment DESC LIMIT 1`, threadID).
		Scan(&g.ThreadID, &g.Segment, &g.CoversUpTo, &g.Summary, &profile, &g.Reason, &g.Model, &g.InputTokens, &g.Seq, &g.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Segment{}, ErrNotFound
	}
	if err != nil {
		return Segment{}, err
	}
	json.Unmarshal([]byte(profile), &g.Profile)
	if g.Profile == nil {
		g.Profile = []string{}
	}
	return g, nil
}

// MessagesAfter returns every message of a thread with seq > after, oldest
// first.
func (s *Store) MessagesAfter(ctx context.Context, threadID string, after int64) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, thread_id, role, content, error, reply_to, kind, event_seq, created_at, context, attachments, source
		 FROM messages WHERE thread_id = ? AND event_seq > ? ORDER BY event_seq`, threadID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessageRows(rows)
}

// MessagesAround returns the message with seq in a thread and up to before
// and after messages next to it, oldest first. ErrNotFound when seq is not
// a message of the thread.
func (s *Store) MessagesAround(ctx context.Context, threadID string, seq int64, before, after int) ([]Message, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE thread_id = ? AND event_seq = ?`, threadID, seq).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrNotFound
	}
	older, err := s.Messages(ctx, threadID, seq+1, before+1) // includes seq itself
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, thread_id, role, content, error, reply_to, kind, event_seq, created_at, context, attachments, source
		 FROM messages WHERE thread_id = ? AND event_seq > ? ORDER BY event_seq LIMIT ?`, threadID, seq, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	newer, err := scanMessageRows(rows)
	if err != nil {
		return nil, err
	}
	return append(older, newer...), nil
}

// MessageHit is a message found by SearchMessages.
type MessageHit struct {
	Message
	Score float64 `json:"score"`
}

// SearchMessages finds messages related to query in one thread, or in every
// thread when threadID is empty, best match first. Like SearchMemories it
// matches substrings (Chinese by two-character runs), which is fast enough
// for one user's messages.
func (s *Store) SearchMessages(ctx context.Context, threadID, query string, limit int) ([]MessageHit, error) {
	terms := searchTerms(query)
	if len(terms) == 0 {
		return []MessageHit{}, nil
	}
	conds := make([]string, len(terms))
	args := []any{}
	where := ""
	if threadID != "" {
		where = "thread_id = ? AND "
		args = append(args, threadID)
	}
	for i, t := range terms {
		conds[i] = "instr(lower(content), ?) > 0"
		args = append(args, t)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, thread_id, role, content, error, reply_to, kind, event_seq, created_at, context, attachments, source
		 FROM messages WHERE `+where+`kind != 'silent' AND (`+strings.Join(conds, " OR ")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	msgs, err := scanMessageRows(rows)
	if err != nil {
		return nil, err
	}
	hits := make([]MessageHit, len(msgs))
	for i, m := range msgs {
		hits[i].Message = m
		content := strings.ToLower(m.Content)
		for _, t := range terms {
			if strings.Contains(content, t) {
				hits[i].Score += float64(utf8.RuneCountInString(t))
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Seq > hits[j].Seq // newer first among equals
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// ThreadUsage is the token usage of a thread's recent model calls.
type ThreadUsage struct {
	Calls        int `json:"calls"`
	InputTokens  int `json:"input_tokens"`
	CachedTokens int `json:"cached_tokens"`
	// LastInput is the input tokens of the newest call.
	LastInput int `json:"last_input"`
}

// RecentThreadUsage sums the last n calls of role in a thread.
func (s *Store) RecentThreadUsage(ctx context.Context, threadID, role string, n int) (ThreadUsage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT input_tokens, cached_tokens FROM usage WHERE thread_id = ? AND role = ? ORDER BY event_seq DESC LIMIT ?`,
		threadID, role, n)
	if err != nil {
		return ThreadUsage{}, err
	}
	defer rows.Close()
	var u ThreadUsage
	for rows.Next() {
		var in, cached int
		if err := rows.Scan(&in, &cached); err != nil {
			return ThreadUsage{}, err
		}
		if u.Calls == 0 {
			u.LastInput = in
		}
		u.Calls++
		u.InputTokens += in
		u.CachedTokens += cached
	}
	return u, rows.Err()
}

// MessageBySeq returns the message stored by the event seq.
func (s *Store) MessageBySeq(ctx context.Context, seq int64) (Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, thread_id, role, content, error, reply_to, kind, event_seq, created_at, context, attachments, source
		 FROM messages WHERE event_seq = ?`, seq)
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
