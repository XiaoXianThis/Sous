package notify

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
)

// Routes serves push subscriptions and the settings page's data. Roles maps
// each model role to its "provider/model", shown read-only.
func (n *Notifier) Routes(roles map[string]string) func(*http.ServeMux, func(string, http.HandlerFunc)) {
	return func(mux *http.ServeMux, protect func(string, http.HandlerFunc)) {
		protect("GET /api/v1/push/vapid-public-key", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"key": n.Sender.VAPID.Public})
		})
		protect("POST /api/v1/push/subscriptions", n.subscribe)
		protect("DELETE /api/v1/push/subscriptions", n.unsubscribe)
		protect("POST /api/v1/push/test", n.test)
		protect("GET /api/v1/settings", func(w http.ResponseWriter, r *http.Request) { n.getSettings(w, r, roles) })
		protect("PATCH /api/v1/settings", func(w http.ResponseWriter, r *http.Request) { n.patchSettings(w, r, roles) })
	}
}

func (n *Notifier) subscribe(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256DH string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil || !strings.HasPrefix(b.Endpoint, "https://") || b.Keys.P256DH == "" || b.Keys.Auth == "" {
		writeError(w, http.StatusBadRequest, "invalid", "无效的推送订阅")
		return
	}
	if _, err := encrypt(Subscription{Endpoint: b.Endpoint, P256DH: b.Keys.P256DH, Auth: b.Keys.Auth}, []byte("{}")); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "推送订阅的密钥无效："+err.Error())
		return
	}
	err := n.Log.Store.PutPushSubscription(r.Context(), store.PushSubscription{
		Endpoint: b.Endpoint, P256DH: b.Keys.P256DH, Auth: b.Keys.Auth, UserAgent: r.UserAgent(), CreatedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (n *Notifier) unsubscribe(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Endpoint string `json:"endpoint"`
	}
	json.NewDecoder(r.Body).Decode(&b)
	if err := n.Log.Store.DeletePushSubscription(r.Context(), b.Endpoint); err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// test sends a notification to every subscribed device.
func (n *Notifier) test(w http.ResponseWriter, r *http.Request) {
	subs, err := n.Log.Store.PushSubscriptions(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	sent := 0
	var errs []string
	for _, s := range subs {
		err := n.Sender.Send(r.Context(), Subscription{Endpoint: s.Endpoint, P256DH: s.P256DH, Auth: s.Auth},
			Message{Title: "sous", Body: "推送通知已经开启。", URL: "/", Tag: "test"}, true)
		if err == nil {
			sent++
		} else {
			if err == ErrGone {
				n.Log.Store.DeletePushSubscription(r.Context(), s.Endpoint)
			}
			errs = append(errs, err.Error())
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": len(subs), "sent": sent, "errors": errs})
}

type settingsView struct {
	DefaultMode  string            `json:"default_mode"`
	DefaultAgent string            `json:"default_agent"`
	Notify       map[string]bool   `json:"notify"`
	Quiet        Quiet             `json:"quiet"`
	Roles        map[string]string `json:"roles"`
	Devices      int               `json:"push_devices"`
}

func (n *Notifier) view(r *http.Request, roles map[string]string) settingsView {
	ctx := r.Context()
	st := n.Log.Store
	mode, _ := st.Setting(ctx, task.SettingDefaultMode)
	if mode == "" {
		mode = "default"
	}
	agent, _ := st.Setting(ctx, task.SettingDefaultAgent)
	subs, _ := st.PushSubscriptions(ctx)
	return settingsView{DefaultMode: mode, DefaultAgent: agent, Notify: Kinds(ctx, st), Quiet: QuietHours(ctx, st), Roles: roles, Devices: len(subs)}
}

func (n *Notifier) getSettings(w http.ResponseWriter, r *http.Request, roles map[string]string) {
	writeJSON(w, http.StatusOK, n.view(r, roles))
}

// patchSettings changes the fields present in the body. API keys are never
// settings: they stay in the Hub's environment (requirements R9.2).
func (n *Notifier) patchSettings(w http.ResponseWriter, r *http.Request, roles map[string]string) {
	var b struct {
		DefaultMode  *string         `json:"default_mode"`
		DefaultAgent *string         `json:"default_agent"`
		Notify       map[string]bool `json:"notify"`
		Quiet        *Quiet          `json:"quiet"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	ctx := r.Context()
	st := n.Log.Store
	if b.DefaultMode != nil {
		if !slices.Contains(task.Modes, *b.DefaultMode) {
			writeError(w, http.StatusBadRequest, "invalid", "权限模式只能是 default、auto 或 open")
			return
		}
		st.SetSetting(ctx, task.SettingDefaultMode, *b.DefaultMode)
	}
	if b.DefaultAgent != nil {
		st.SetSetting(ctx, task.SettingDefaultAgent, strings.TrimSpace(*b.DefaultAgent))
	}
	if b.Notify != nil {
		kinds := Kinds(ctx, st)
		for k, v := range b.Notify {
			if _, ok := kinds[k]; !ok {
				writeError(w, http.StatusBadRequest, "invalid", "未知的通知类型 "+k)
				return
			}
			kinds[k] = v
		}
		raw, _ := json.Marshal(kinds)
		st.SetSetting(ctx, SettingKinds, string(raw))
	}
	if b.Quiet != nil {
		q := *b.Quiet
		_, ok1 := minutes(q.Start)
		_, ok2 := minutes(q.End)
		if !ok1 || !ok2 {
			writeError(w, http.StatusBadRequest, "invalid", "免打扰时间要写成 HH:MM")
			return
		}
		if q.TZ != "" {
			if _, err := time.LoadLocation(q.TZ); err != nil {
				writeError(w, http.StatusBadRequest, "invalid", "未知的时区 "+q.TZ)
				return
			}
		}
		raw, _ := json.Marshal(q)
		st.SetSetting(ctx, SettingQuiet, string(raw))
	}
	writeJSON(w, http.StatusOK, n.view(r, roles))
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
	slog.Error("notify: internal error", "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
}
