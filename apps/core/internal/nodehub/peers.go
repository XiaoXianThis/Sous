package nodehub

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// remoteIP is where a machine's connection comes from, as the Hub sees it.
// Behind a proxy it is the first X-Forwarded-For address; it is only a hint
// for peerNote, never used to decide anything.
func remoteIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		return strings.TrimSpace(strings.Split(f, ",")[0])
	}
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	return host
}

var remoteCmd = regexp.MustCompile(`(?i)(^|[\s;&|(])(ssh|scp|sftp|rsync)\s`)

// peerNote recognizes an approval that reaches another connected machine
// over ssh, scp or rsync, and says that machine could do the work itself
// (decision 0025: a hint, not a rule; ssh still goes to the user).
func (h *Hub) peerNote(ctx context.Context, fromNode, text string) string {
	if !remoteCmd.MatchString(text) {
		return ""
	}
	lower := strings.ToLower(text)
	nodes, err := h.log.Store.Nodes(ctx)
	if err != nil {
		return ""
	}
	for _, n := range nodes {
		if n.ID == fromNode || n.Revoked {
			continue
		}
		keys := []string{n.Name, n.Hostname}
		if i := strings.IndexByte(n.Hostname, '.'); i > 0 {
			keys = append(keys, n.Hostname[:i])
		}
		h.mu.Lock()
		if c := h.conns[n.ID]; c != nil {
			keys = append(keys, c.addrs...)
		}
		h.mu.Unlock()
		for _, k := range keys {
			if mentionsHost(lower, strings.ToLower(k)) {
				return "提示：" + k + " 是已接入 sous 的机器「" + n.Name + "」。也可以让主管把这件事直接派到那台机器上做（比如在服务器上就地部署），不经过 ssh。"
			}
		}
	}
	return ""
}

// mentionsHost reports whether host appears in text as a whole host name:
// after start, space, @, quote or /, and before end, space, :, /, quote.
func mentionsHost(text, host string) bool {
	if len(host) < 3 || host == "localhost" || host == "127.0.0.1" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(text[i:], host)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(host)
		before := start == 0 || strings.ContainsRune(" \t@'\"/=", rune(text[start-1]))
		after := end == len(text) || strings.ContainsRune(" \t:/'\"\n;", rune(text[end]))
		if before && after {
			return true
		}
		i = start + 1
	}
}

// AskWaitMinutes is what the supervisor is told about how long an agent
// waits for an answer (node.AskWait).
const AskWaitMinutes = 20

// askTrigger wakes the supervisor of the run's thread with an agent's
// question (decision 0025); the answer goes back with run_message.
func (h *Hub) askTrigger(ctx context.Context, ref runRef, p proto.RunAsk) event.Event {
	thread := ref.thread
	if thread == "" {
		thread, _ = h.log.Store.MainThreadID(ctx)
	}
	who, subject, target := "agent", "一个 Run", p.RunID
	if r, err := h.log.Store.Run(ctx, p.RunID); err == nil {
		who = r.Agent
		if n, err := h.log.Store.Node(ctx, r.NodeID); err == nil && n.Name != "" {
			who += "（" + n.Name + "）"
		}
	}
	if ref.task != "" {
		target = ref.task
		subject = "任务 " + ref.task
		if t, err := h.log.Store.Task(ctx, ref.task); err == nil {
			subject = fmt.Sprintf("任务 %s「%s」", t.ID, t.Title)
		}
	}
	content := fmt.Sprintf("【agent 的问题】%s的 %s 在执行中问：\n%s\n\n它停在这里等回答：用 run_message（task 填 %s）回答它；"+
		"自己能判断的直接答，要用户拿主意时用 notify 提醒用户（用户也可以在任务页的“追加指令”里直接回答）。%d 分钟内没有回答，它会按自己的判断继续。"+
		"（这是 sous 的自动通知，不是用户发的消息。）", subject, who, p.Question, target, AskWaitMinutes)
	return event.New(event.TriggerFired, event.ActorTrigger, thread, event.TriggerPayload{
		Kind: event.TriggerTask, Key: fmt.Sprintf("ask:%s:%d", p.RunID, p.Seq), MessageID: ids.New(),
		Title: "agent 在问问题", Content: content, At: time.Now().UnixMilli(),
	})
}
