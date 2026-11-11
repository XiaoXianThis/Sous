package sysmap

import "context"

type Refs struct {
	ThreadID string `json:"thread_id,omitempty"`
	TaskID   string `json:"task_id,omitempty"`
	RunID    string `json:"run_id,omitempty"`
}
type refsKey struct{}

func WithRefs(ctx context.Context, r Refs) context.Context {
	return context.WithValue(ctx, refsKey{}, r)
}
func RefsFrom(ctx context.Context) Refs { r, _ := ctx.Value(refsKey{}).(Refs); return r }
