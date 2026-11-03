package tool

import (
	"context"
	"encoding/json"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
)

// CurrentTime reports the Hub's local time.
type CurrentTime struct {
	Now func() time.Time
}

func (CurrentTime) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "current_time",
		Description: "获取当前日期和时间（服务器时区）。",
		Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

func (t CurrentTime) Call(ctx context.Context, _ json.RawMessage) (string, error) {
	now := time.Now
	if t.Now != nil {
		now = t.Now
	}
	return now().Format("2006-01-02 15:04:05 Mon MST"), nil
}
