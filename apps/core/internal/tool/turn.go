package tool

import "context"

// Turn describes the supervisor turn a tool runs in.
type Turn struct {
	ThreadID     string
	MainThreadID string
	// ProjectID is set in a project chat: the turn may only touch this project.
	ProjectID string
	// ReplyTo is the seq of the user message the turn answers: messages a
	// tool adds to the conversation (a consultant's answer) belong to it.
	ReplyTo int64
	// TopicID is the topic the turn answers (chat-topics): tasks it creates
	// and messages tools add belong to it.
	TopicID string
}

type turnKey struct{}

// WithTurn attaches the turn to ctx for the tools it calls.
func WithTurn(ctx context.Context, t Turn) context.Context {
	return context.WithValue(ctx, turnKey{}, t)
}

// TurnFrom returns the turn attached to ctx, if any.
func TurnFrom(ctx context.Context) Turn {
	t, _ := ctx.Value(turnKey{}).(Turn)
	return t
}
