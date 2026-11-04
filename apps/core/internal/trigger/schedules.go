package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

var ErrInvalid = errors.New("invalid schedule")

// ScheduleInput creates or changes a schedule; nil fields keep their value.
type ScheduleInput struct {
	Title    *string
	Cron     *string
	TZ       *string
	Prompt   *string
	ThreadID *string
	Enabled  *bool
}

// Put creates (id empty) or changes a schedule.
func (s *Scheduler) Put(ctx context.Context, id string, in ScheduleInput, actor string) (store.Schedule, error) {
	x := store.Schedule{Enabled: true}
	if id != "" {
		var err error
		if x, err = s.store().Schedule(ctx, id); err != nil {
			return store.Schedule{}, err
		}
	} else {
		id = ids.New()
		x.TZ = DefaultTZ(ctx, s.store())
	}
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = strings.TrimSpace(*src)
		}
	}
	set(&x.Title, in.Title)
	set(&x.Cron, in.Cron)
	set(&x.TZ, in.TZ)
	set(&x.Prompt, in.Prompt)
	set(&x.ThreadID, in.ThreadID)
	if in.Enabled != nil {
		x.Enabled = *in.Enabled
	}
	if x.Prompt == "" {
		return store.Schedule{}, fmt.Errorf("%w: 需要写清楚到时间要做什么（prompt）", ErrInvalid)
	}
	if utf8.RuneCountInString(x.Prompt) > 4000 {
		return store.Schedule{}, fmt.Errorf("%w: prompt 太长", ErrInvalid)
	}
	if x.Title == "" {
		x.Title = truncate(x.Prompt, 20)
	}
	if _, err := ParseCron(x.Cron); err != nil {
		return store.Schedule{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if x.TZ != "" {
		if _, err := time.LoadLocation(x.TZ); err != nil {
			return store.Schedule{}, fmt.Errorf("%w: 未知的时区 %s", ErrInvalid, x.TZ)
		}
	}
	if x.ThreadID != "" {
		if _, err := s.store().Thread(ctx, x.ThreadID); err != nil {
			return store.Schedule{}, fmt.Errorf("%w: 对话 %s 不存在", ErrInvalid, x.ThreadID)
		}
	}
	ev := event.New(event.SchedulePut, actor, x.ThreadID, event.SchedulePayload{
		ScheduleID: id, Title: x.Title, Cron: x.Cron, TZ: x.TZ, Prompt: x.Prompt, ThreadID: x.ThreadID, Enabled: x.Enabled,
	})
	if _, err := s.Log.Append(ctx, ev); err != nil {
		return store.Schedule{}, err
	}
	return s.get(ctx, id)
}

func (s *Scheduler) get(ctx context.Context, id string) (store.Schedule, error) {
	x, err := s.store().Schedule(ctx, id)
	if err != nil {
		return x, err
	}
	if n := NextRun(x); !n.IsZero() && x.Enabled {
		x.NextRun = n.UnixMilli()
	}
	return x, nil
}

// List returns every schedule with its next run.
func (s *Scheduler) List(ctx context.Context) ([]store.Schedule, error) {
	all, err := s.store().Schedules(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if n := NextRun(all[i]); !n.IsZero() && all[i].Enabled {
			all[i].NextRun = n.UnixMilli()
		}
	}
	return all, nil
}

// Delete removes a schedule.
func (s *Scheduler) Delete(ctx context.Context, id, actor string) error {
	if _, err := s.store().Schedule(ctx, id); err != nil {
		return err
	}
	_, err := s.Log.Append(ctx, event.New(event.ScheduleDeleted, actor, "", event.SchedulePayload{ScheduleID: id}))
	return err
}

func describe(x store.Schedule) string {
	s := fmt.Sprintf("定时任务 %s「%s」，cron `%s`", x.ID, x.Title, x.Cron)
	if x.TZ != "" {
		s += "（" + x.TZ + "）"
	}
	if !x.Enabled {
		s += "，已停用"
	} else if x.NextRun > 0 {
		s += "，下次 " + time.UnixMilli(x.NextRun).In(location(x.TZ)).Format("2006-01-02 15:04")
	}
	return s
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// Tools returns the supervisor's schedule tools.
func (s *Scheduler) Tools() []tool.Tool {
	return []tool.Tool{scheduleCreate{s}, scheduleList{s}, scheduleDelete{s}}
}

type scheduleCreate struct{ s *Scheduler }

func (scheduleCreate) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "schedule_create",
		Description: "创建或修改定时任务：到时间时，prompt 会作为一条消息交给你（主管）处理，比如“汇总昨天的任务进展发给我”。" +
			"cron 是 5 个字段：分 时 日 月 周，例如每天早上 9 点是 \"0 9 * * *\"，工作日 18:30 是 \"30 18 * * 1-5\"。" +
			"要修改已有的定时任务，传入它的 id，只填要改的字段。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"id":{"type":"string","description":"要修改的定时任务 id；新建时不填"},
			"title":{"type":"string","description":"简短的名字"},
			"cron":{"type":"string"},
			"prompt":{"type":"string","description":"到时间时你要做的事，写成给自己的指令"},
			"tz":{"type":"string","description":"IANA 时区，比如 Asia/Shanghai；不填用用户设备的时区"},
			"enabled":{"type":"boolean"},
			"here":{"type":"boolean","description":"为 true 时结果发在当前对话；默认发在主线"}
		}}`),
	}
}

func (t scheduleCreate) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		ID                      string
		Title, Cron, Prompt, TZ *string
		Enabled                 *bool
		Here                    bool
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	in := ScheduleInput{Title: a.Title, Cron: a.Cron, Prompt: a.Prompt, TZ: a.TZ, Enabled: a.Enabled}
	if a.Here {
		th := tool.TurnFrom(ctx).ThreadID
		in.ThreadID = &th
	}
	if a.ID == "" && a.Cron == nil {
		return "", fmt.Errorf("%w: 需要 cron", ErrInvalid)
	}
	x, err := t.s.Put(ctx, a.ID, in, event.ActorSupervisor)
	if err != nil {
		return "", err
	}
	return "已保存：" + describe(x), nil
}

type scheduleList struct{ s *Scheduler }

func (scheduleList) Spec() provider.ToolSpec {
	return provider.ToolSpec{Name: "schedule_list", Description: "列出所有定时任务。", Schema: json.RawMessage(`{"type":"object","properties":{}}`)}
}

func (t scheduleList) Call(ctx context.Context, _ json.RawMessage) (string, error) {
	all, err := t.s.List(ctx)
	if err != nil {
		return "", err
	}
	if len(all) == 0 {
		return "还没有定时任务", nil
	}
	var b strings.Builder
	for _, x := range all {
		b.WriteString("- " + describe(x) + "：" + truncate(x.Prompt, 80) + "\n")
	}
	return b.String(), nil
}

type scheduleDelete struct{ s *Scheduler }

func (scheduleDelete) Spec() provider.ToolSpec {
	return provider.ToolSpec{Name: "schedule_delete", Description: "删除一个定时任务。",
		Schema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`)}
}

func (t scheduleDelete) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct{ ID string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if err := t.s.Delete(ctx, a.ID, event.ActorSupervisor); err != nil {
		return "", err
	}
	return "已删除", nil
}

// Routes serves /api/v1/schedules.
func (s *Scheduler) Routes(mux *http.ServeMux, protect func(string, http.HandlerFunc)) {
	protect("GET /api/v1/schedules", func(w http.ResponseWriter, r *http.Request) {
		all, err := s.List(r.Context())
		if err != nil {
			internal(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"schedules": all})
	})
	put := func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Title    *string `json:"title"`
			Cron     *string `json:"cron"`
			TZ       *string `json:"tz"`
			Prompt   *string `json:"prompt"`
			ThreadID *string `json:"thread_id"`
			Enabled  *bool   `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			writeError(w, http.StatusBadRequest, "bad_json", err.Error())
			return
		}
		id := r.PathValue("id")
		if id == "" && b.Cron == nil {
			writeError(w, http.StatusBadRequest, "invalid", "需要 cron")
			return
		}
		x, err := s.Put(r.Context(), id, ScheduleInput{Title: b.Title, Cron: b.Cron, TZ: b.TZ, Prompt: b.Prompt, ThreadID: b.ThreadID, Enabled: b.Enabled}, event.ActorUser)
		if err != nil {
			scheduleError(w, err)
			return
		}
		status := http.StatusOK
		if id == "" {
			status = http.StatusCreated
		}
		writeJSON(w, status, x)
	}
	protect("POST /api/v1/schedules", put)
	protect("PATCH /api/v1/schedules/{id}", put)
	protect("DELETE /api/v1/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Delete(r.Context(), r.PathValue("id"), event.ActorUser); err != nil {
			scheduleError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	protect("POST /api/v1/schedules/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		x, err := s.store().Schedule(r.Context(), r.PathValue("id"))
		if err != nil {
			scheduleError(w, err)
			return
		}
		content := fmt.Sprintf("【定时任务「%s」（手动触发）】\n%s", x.Title, x.Prompt)
		if err := s.fire(r.Context(), event.TriggerCron, "schedule:"+x.ID, x.ID, x.ThreadID, x.Title, content); err != nil {
			internal(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
	})
}

func scheduleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "定时任务不存在")
	case errors.Is(err, ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
	default:
		internal(w, err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func internal(w http.ResponseWriter, err error) {
	slog.Error("trigger: internal error", "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
}
