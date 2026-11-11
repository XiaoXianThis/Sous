package httpapi_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/coder/websocket"
	"net/http"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func TestNotesRESTAndSend(t *testing.T) {
	e := newEnv(t, true)
	if code := e.do(t, "GET", "/api/v1/notes", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", code)
	}
	e.login(t)
	var n store.Note
	if code := e.do(t, "POST", "/api/v1/notes", map[string]any{"text": "  中文记事  "}, &n); code != 201 || n.Text != "中文记事" {
		t.Fatalf("create: %d %+v", code, n)
	}
	var list struct {
		Notes []store.Note `json:"notes"`
	}
	if code := e.do(t, "GET", "/api/v1/notes?q=记事", nil, &list); code != 200 || len(list.Notes) != 1 {
		t.Fatalf("search: %d %+v", code, list)
	}
	if code := e.do(t, "PATCH", "/api/v1/notes/"+n.ID, map[string]any{"text": "更新后的记事"}, &n); code != 200 || n.Text != "更新后的记事" {
		t.Fatalf("patch: %d %+v", code, n)
	}
	var sent struct {
		MessageID string `json:"message_id"`
		TopicID   string `json:"topic_id"`
	}
	body := map[string]any{"content": "请处理", "context": map[string]any{"note_ids": []string{n.ID, n.ID}}}
	if code := e.do(t, "POST", "/api/v1/threads/"+e.main+"/messages", body, &sent); code != 202 {
		t.Fatalf("send: %d", code)
	}
	n, err := e.log.Store.Note(context.Background(), n.ID)
	if err != nil || n.Status != "sent" || len(n.Sends) != 1 || n.Sends[0].MessageID != sent.MessageID || n.Sends[0].TopicID != sent.TopicID {
		t.Fatalf("sent note: %+v %v", n, err)
	}
	if code := e.do(t, "POST", "/api/v1/threads/"+e.main+"/messages", map[string]any{"content": "x", "context": map[string]any{"note_ids": []string{"missing"}}}, nil); code != 404 {
		t.Fatalf("missing note: %d", code)
	}
	if code := e.do(t, "DELETE", "/api/v1/notes/"+n.ID, nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code := e.do(t, "DELETE", "/api/v1/notes/"+n.ID, nil, nil); code != 404 {
		t.Fatalf("delete missing: %d", code)
	}
}

func TestNoteEventToLoggedInWebSocket(t *testing.T) {
	e := newEnv(t, true)
	e.login(t)
	u, _ := url.Parse(e.srv.URL)
	h := http.Header{}
	for _, c := range e.client.Jar.Cookies(u) {
		h.Add("Cookie", c.Name+"="+c.Value)
	}
	h.Set("Origin", "http://localhost:8787")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/ws?since=0", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if code := e.do(t, "POST", "/api/v1/notes", map[string]string{"text": "记一下"}, nil); code != 201 {
		t.Fatal(code)
	}
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var ev event.Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Type == event.NotePut {
			break
		}
	}
}

func TestMeetingMessageUsesMeetingTopicAndRejectsClosed(t *testing.T) {
	e := newEnv(t, true)
	e.login(t)
	id := "meeting-test"
	opening := "meeting-opening"
	if _, err := e.log.Append(context.Background(), event.New(event.ThreadCreated, event.ActorUser, id, event.ThreadCreatedPayload{Title: "会", Kind: event.KindMeeting, MeetingTopic: opening}), event.New(event.MessageUser, event.ActorUser, id, event.MessagePayload{MessageID: opening, Content: "议题"})); err != nil {
		t.Fatal(err)
	}
	var sent struct {
		TopicID string `json:"topic_id"`
	}
	if code := e.do(t, "POST", "/api/v1/threads/"+id+"/messages", map[string]string{"content": "继续"}, &sent); code != 202 || sent.TopicID != opening {
		t.Fatalf("send: %d %+v", code, sent)
	}
	if _, err := e.log.Append(context.Background(), event.New(event.MeetingClosed, event.ActorUser, id, event.MeetingPayload{MeetingID: id})); err != nil {
		t.Fatal(err)
	}
	if code := e.do(t, "POST", "/api/v1/threads/"+id+"/messages", map[string]string{"content": "继续"}, nil); code != 409 {
		t.Fatalf("closed: %d", code)
	}
}
