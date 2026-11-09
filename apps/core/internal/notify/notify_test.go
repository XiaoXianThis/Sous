package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func TestHandleRespectsSwitchesAndQuietHours(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	log := eventlog.New(st, event.NewBus())
	ctx := context.Background()
	main, _ := log.EnsureMainThread(ctx)

	b := newBrowser(t)
	var mu sync.Mutex
	var got []Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m Message
		json.Unmarshal(b.decrypt(t, body), &m)
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
		w.WriteHeader(201)
	}))
	defer srv.Close()
	sub := b.sub(srv.URL + "/sub")
	st.PutPushSubscription(ctx, store.PushSubscription{Endpoint: sub.Endpoint, P256DH: sub.P256DH, Auth: sub.Auth})
	gone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer gone.Close()
	old := newBrowser(t).sub(gone.URL)
	st.PutPushSubscription(ctx, store.PushSubscription{Endpoint: old.Endpoint, P256DH: old.P256DH, Auth: old.Auth})

	vapid, _ := LoadVAPID(t.TempDir())
	now := time.Date(2026, 11, 3, 23, 30, 0, 0, time.UTC)
	n := &Notifier{Log: log, Sender: &Sender{VAPID: vapid, Subject: "mailto:a@b"}, Now: func() time.Time { return now }}

	notice := event.New(event.MessageAssistant, event.ActorSupervisor, main, event.MessagePayload{MessageID: "m1", Content: "早上好", Kind: event.KindNotice})
	push := func(ev event.Event) {
		raw, _ := json.Marshal(ev)
		if err := n.Handle(ctx, store.Effect{Payload: raw}); err != nil {
			t.Fatal(err)
		}
	}
	push(notice)
	if len(got) != 1 || got[0].Title != "sous" || got[0].Body != "早上好" || got[0].URL != "/#topic=m1" || got[0].Tag != "topic-m1" {
		t.Fatalf("notice: %+v", got)
	}
	if subs, _ := st.PushSubscriptions(ctx); len(subs) != 1 {
		t.Fatal("a gone subscription was kept")
	}

	// Quiet hours hold back everything but approvals.
	q, _ := json.Marshal(Quiet{Enabled: true, Start: "22:00", End: "08:00", TZ: "UTC"})
	st.SetSetting(ctx, SettingQuiet, string(q))
	push(notice)
	if len(got) != 1 {
		t.Fatal("pushed during quiet hours")
	}
	// A kind switched off stays silent at any hour.
	now = now.Add(10 * time.Hour)
	st.SetSetting(ctx, SettingKinds, `{"report":false}`)
	push(notice)
	if len(got) != 1 {
		t.Fatal("pushed a disabled kind")
	}
	st.SetSetting(ctx, SettingKinds, `{}`)
	push(notice)
	if len(got) != 2 {
		t.Fatal("re-enabled kind not pushed")
	}
	evs, _ := st.EventsSince(ctx, 0, 100)
	sent := 0
	for _, ev := range evs {
		if ev.Type == event.NotifySent {
			sent++
		}
	}
	if sent != 2 {
		t.Fatalf("notify.sent events: %d", sent)
	}
}
