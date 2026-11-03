package store

import (
	"context"
	"database/sql"
	"sort"

	"strings"
	"unicode"
	"unicode/utf8"
)

type Memory struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Content   string  `json:"content"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
	Score     float64 `json:"score,omitempty"` // search rank, higher is better
}

const memoryCols = `id, kind, content, created_at, updated_at`

func scanMemories(rows *sql.Rows, withScore bool) ([]Memory, error) {
	defer rows.Close()
	out := []Memory{}
	for rows.Next() {
		var m Memory
		dest := []any{&m.ID, &m.Kind, &m.Content, &m.CreatedAt, &m.UpdatedAt}
		if withScore {
			dest = append(dest, &m.Score)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) Memory(ctx context.Context, id string) (Memory, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+memoryCols+` FROM memories WHERE id = ?`, id)
	if err != nil {
		return Memory{}, err
	}
	ms, err := scanMemories(rows, false)
	if err != nil {
		return Memory{}, err
	}
	if len(ms) == 0 {
		return Memory{}, ErrNotFound
	}
	return ms[0], nil
}

// Memories lists memories, most recently updated first. kind may be empty.
func (s *Store) Memories(ctx context.Context, kind string, limit int) ([]Memory, error) {
	q := `SELECT ` + memoryCols + ` FROM memories`
	args := []any{}
	if kind != "" {
		q += ` WHERE kind = ?`
		args = append(args, kind)
	}
	q += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanMemories(rows, false)
}

// SearchMemories finds memories related to query, best match first.
//
// A personal assistant holds hundreds to a few thousand memories, so this
// scans with substring matches instead of keeping a full-text index. Chinese
// text is matched by two-character runs (most Chinese words are two
// characters), other words whole and case-insensitively. A memory scores the
// summed length of the terms it contains.
func (s *Store) SearchMemories(ctx context.Context, query string, limit int) ([]Memory, error) {
	terms := searchTerms(query)
	if len(terms) == 0 {
		return []Memory{}, nil
	}
	conds := make([]string, len(terms))
	args := make([]any, len(terms))
	for i, t := range terms {
		conds[i] = "instr(lower(content), ?) > 0"
		args[i] = t
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+memoryCols+` FROM memories WHERE `+strings.Join(conds, " OR "), args...)
	if err != nil {
		return nil, err
	}
	hits, err := scanMemories(rows, false)
	if err != nil {
		return nil, err
	}
	for i := range hits {
		content := strings.ToLower(hits[i].Content)
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
		return hits[i].UpdatedAt > hits[j].UpdatedAt
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// maxSearchTerms bounds the SQL a long query produces.
const maxSearchTerms = 48

// searchTerms splits a query into lowercase terms: two-character runs of
// Chinese (and other CJK) text, and whole words otherwise. Single
// characters are dropped as too common to mean anything.
func searchTerms(query string) []string {
	words := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		if !seen[t] && len(out) < maxSearchTerms {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, w := range words {
		var run, latin []rune
		flush := func() {
			if len(run) == 1 {
				// a lone character is too common to mean anything
			} else {
				for i := 0; i+2 <= len(run); i++ {
					add(string(run[i : i+2]))
				}
			}
			run = run[:0]
			if len(latin) >= 2 {
				add(string(latin))
			}
			latin = latin[:0]
		}
		for _, r := range w {
			if isCJK(r) {
				if len(latin) > 0 {
					flush()
				}
				run = append(run, r)
			} else {
				if len(run) > 0 {
					flush()
				}
				latin = append(latin, r)
			}
		}
		flush()
	}
	return out
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
}
