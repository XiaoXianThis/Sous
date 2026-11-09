package httpapi_test

import (
	"context"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

type topicList struct {
	Topics []struct {
		store.Topic
		Count int
		Last  *store.Message
		Tasks []store.Task
	}
	Earlier struct{ Count int }
}

func waitReplies(t *testing.T, e *env, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		msgs, _ := e.log.Store.MessagesAfter(context.Background(), e.main, 0)
		got := 0
		for _, m := range msgs {
			if m.Role == "assistant" {
				got++
			}
		}
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %d replies: %+v", n, msgs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A message without a topic starts one; a message with one continues it;
// the list and the topic's messages show it.
func TestTopicsOverREST(t *testing.T) {
	e := newEnv(t, true)
	e.login(t)
	path := "/api/v1/threads/" + e.main + "/messages"
	var first struct {
		MessageID string `json:"message_id"`
		TopicID   string `json:"topic_id"`
	}
	if code := e.do(t, "POST", path, map[string]any{"content": "修一下登录页\n细节如下"}, &first); code != 202 || first.TopicID != first.MessageID {
		t.Fatalf("post: %d %+v", code, first)
	}
	waitReplies(t, e, 1)
	if code := e.do(t, "POST", path, map[string]any{"content": "还有注册页", "topic_id": first.TopicID}, nil); code != 202 {
		t.Fatalf("post in topic: %d", code)
	}
	if code := e.do(t, "POST", path, map[string]any{"content": "x", "topic_id": "nope"}, nil); code != 404 {
		t.Fatalf("unknown topic: %d", code)
	}
	waitReplies(t, e, 2)
	if code := e.do(t, "POST", path, map[string]any{"content": "另一件事"}, nil); code != 202 {
		t.Fatalf("post: %d", code)
	}
	waitReplies(t, e, 3)

	var list topicList
	if code := e.do(t, "GET", "/api/v1/threads/"+e.main+"/topics", nil, &list); code != 200 || len(list.Topics) != 2 {
		t.Fatalf("topics: %d %+v", code, list)
	}
	var login = list.Topics[1]
	if login.ID != first.TopicID || login.Title != "修一下登录页" || login.Count != 4 || login.Last == nil || login.Last.Role != "assistant" {
		t.Fatalf("login topic: %+v", login)
	}
	var msgs struct{ Messages []store.Message }
	e.do(t, "GET", path+"?topic="+first.TopicID, nil, &msgs)
	if len(msgs.Messages) != 4 {
		t.Fatalf("topic messages: %+v", msgs.Messages)
	}
	for _, m := range msgs.Messages {
		if m.TopicID != first.TopicID {
			t.Fatalf("message in another topic: %+v", m)
		}
	}

	// Rename, close and reopen.
	var topic store.Topic
	if code := e.do(t, "PATCH", "/api/v1/topics/"+first.TopicID, map[string]any{"title": "登录和注册", "status": "closed"}, &topic); code != 200 ||
		topic.Title != "登录和注册" || topic.Status != event.TopicClosed {
		t.Fatalf("patch: %d %+v", code, topic)
	}
	if code := e.do(t, "PATCH", "/api/v1/topics/"+first.TopicID, map[string]any{"status": "done"}, nil); code != 400 {
		t.Fatalf("bad status: %d", code)
	}
	e.do(t, "POST", path, map[string]any{"content": "又坏了", "topic_id": first.TopicID}, nil)
	topic, _ = e.log.Store.Topic(context.Background(), first.TopicID)
	if topic.Status != event.TopicOpen {
		t.Fatalf("a new message reopens the topic: %+v", topic)
	}
}

// A message the supervisor attached can be taken back into its own topic,
// and is then answered on its own.
func TestMoveMessageBack(t *testing.T) {
	e := newEnv(t, true)
	e.login(t)
	ctx := context.Background()
	path := "/api/v1/threads/" + e.main + "/messages"
	var a struct {
		MessageID string `json:"message_id"`
	}
	e.do(t, "POST", path, map[string]any{"content": "a"}, &a)
	waitReplies(t, e, 1)
	// b arrives and is attached to a at once, as the supervisor's
	// topic_attach does; the next turn answers it in a.
	b := struct{ MessageID string }{"b"}
	e.log.Append(ctx,
		event.New(event.MessageUser, event.ActorUser, e.main, event.MessagePayload{MessageID: "b", Content: "b"}),
		event.New(event.MessageMoved, event.ActorSupervisor, e.main, event.MessageMovedPayload{MessageID: "b", TopicID: a.MessageID, From: "b"}))
	waitReplies(t, e, 2)

	if code := e.do(t, "POST", "/api/v1/messages/"+b.MessageID+"/move", map[string]any{"topic_id": a.MessageID}, nil); code != 400 {
		t.Fatalf("moving into another topic: %d", code)
	}
	var m store.Message
	if code := e.do(t, "POST", "/api/v1/messages/"+b.MessageID+"/move", map[string]any{"topic_id": b.MessageID}, &m); code != 200 || m.TopicID != b.MessageID {
		t.Fatalf("move back: %d %+v", code, m)
	}
	waitReplies(t, e, 3)
	msgs, _ := e.log.Store.TopicMessages(ctx, e.main, b.MessageID, 0, 10)
	if len(msgs) != 2 || msgs[1].Role != "assistant" {
		t.Fatalf("b's topic after the move: %+v", msgs)
	}
}
