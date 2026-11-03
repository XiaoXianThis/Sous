package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// MemoryKinds are the kinds a memory can have.
var MemoryKinds = []string{"profile", "project", "lesson", "note"}

const maxMemoryRunes = 2000

func validKind(k string) bool {
	for _, x := range MemoryKinds {
		if x == k {
			return true
		}
	}
	return false
}

// FormatMemory renders a memory for the model: "[id] (kind) content".
func FormatMemory(m store.Memory) string {
	return fmt.Sprintf("[%s] (%s) %s", m.ID, m.Kind, m.Content)
}

// MemorySave stores a new memory or replaces an existing one.
type MemorySave struct{ Log *eventlog.Log }

func (MemorySave) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "memory_save",
		Description: "记住一条长期有用的信息。profile：用户的偏好和习惯；project：项目的事实和约定；lesson：做事得到的经验教训；note：其他值得记住的事。" +
			"一条记忆只写一件事，用完整的一句话。要修改已有记忆，传入它的 id。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"content":{"type":"string","description":"要记住的内容"},
			"kind":{"type":"string","enum":["profile","project","lesson","note"]},
			"id":{"type":"string","description":"要修改的记忆 id；新建时不填"}
		},"required":["content","kind"]}`),
	}
}

// ValidateMemory checks a memory's kind and content and returns the trimmed content.
func ValidateMemory(kind, content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", errors.New("content is empty")
	}
	if utf8.RuneCountInString(content) > maxMemoryRunes {
		return "", fmt.Errorf("content is longer than %d characters; save one fact per memory", maxMemoryRunes)
	}
	if !validKind(kind) {
		return "", fmt.Errorf("kind must be one of %s", strings.Join(MemoryKinds, ", "))
	}
	return content, nil
}

func (t MemorySave) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct{ Content, Kind, ID string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	var err error
	if a.Content, err = ValidateMemory(a.Kind, a.Content); err != nil {
		return "", err
	}
	verb := "已记住"
	if a.ID != "" {
		if _, err := t.Log.Store.Memory(ctx, a.ID); err != nil {
			return "", fmt.Errorf("memory %s not found", a.ID)
		}
		verb = "已更新"
	} else {
		a.ID = ids.New()
	}
	ev := event.New(event.MemoryPut, event.ActorSupervisor, TurnFrom(ctx).ThreadID,
		event.MemoryPayload{MemoryID: a.ID, Kind: a.Kind, Content: a.Content})
	if _, err := t.Log.Append(ctx, ev); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s [%s]", verb, a.ID), nil
}

// MemorySearch looks up memories.
type MemorySearch struct{ Log *eventlog.Log }

func (MemorySearch) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "memory_search",
		Description: "在长期记忆里查找相关内容。系统已经自动附上了和当前对话最相关的几条记忆；只有需要更多时才调用。",
		Schema:      json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
	}
}

func (t MemorySearch) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct{ Query string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	ms, err := t.Log.Store.SearchMemories(ctx, a.Query, 10)
	if err != nil {
		return "", err
	}
	if len(ms) == 0 {
		return "没有找到相关记忆。", nil
	}
	lines := make([]string, len(ms))
	for i, m := range ms {
		lines[i] = FormatMemory(m)
	}
	return strings.Join(lines, "\n"), nil
}

// MemoryForget deletes a memory.
type MemoryForget struct{ Log *eventlog.Log }

func (MemoryForget) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "memory_forget",
		Description: "删除一条已经过时或错误的记忆。",
		Schema:      json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
	}
}

func (t MemoryForget) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct{ ID string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if _, err := t.Log.Store.Memory(ctx, a.ID); err != nil {
		return "", fmt.Errorf("memory %s not found", a.ID)
	}
	ev := event.New(event.MemoryDeleted, event.ActorSupervisor, TurnFrom(ctx).ThreadID, event.MemoryPayload{MemoryID: a.ID})
	if _, err := t.Log.Append(ctx, ev); err != nil {
		return "", err
	}
	return "已删除 [" + a.ID + "]", nil
}
