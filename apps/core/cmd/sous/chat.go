package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/ipc"
)

type chatThread struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
}

type chatMessage struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Kind      string `json:"kind"`
	Error     bool   `json:"error"`
	CreatedAt int64  `json:"created_at"`
}

type chatApproval struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Detail  string `json:"detail"`
	Review  string `json:"review"`
	Options []struct {
		OptionID string `json:"option_id"`
		Name     string `json:"name"`
		Kind     string `json:"kind"`
	} `json:"options"`
}

// chatUI is `sous chat`: the conversation in a terminal, through the
// daemon's IPC, so the terminal needs no passkey (requirements R1.4).
type chatUI struct {
	path   string
	c      *ipc.Client
	sub    *ipc.Client
	seq    int64          // newest event seen, to resubscribe after a reconnect
	sent   map[string]int // contents sent but not yet echoed back
	out    io.Writer
	mu     sync.Mutex
	thread chatThread
	// streaming is the id of the reply being streamed, if any.
	streaming string
	mine      map[string]bool // message ids this terminal sent
	shown     map[string]bool // message ids already printed
	approvals []chatApproval
}

func chat(root, threadID string) error {
	path := ipc.Path(root)
	c, err := ipc.Dial(path)
	if err != nil {
		return err
	}
	defer c.Close()
	sub, err := ipc.Dial(path)
	if err != nil {
		return err
	}
	defer sub.Close()
	ui := &chatUI{path: path, c: c, sub: sub, out: os.Stdout, mine: map[string]bool{}, shown: map[string]bool{}, sent: map[string]int{}}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	threads, err := ui.threads(ctx)
	if err != nil {
		return err
	}
	for _, t := range threads {
		if (threadID == "" && t.Kind == "main") || t.ID == threadID {
			ui.thread = t
		}
	}
	if ui.thread.ID == "" {
		return fmt.Errorf("找不到对话 %s", threadID)
	}
	last, err := ui.history(ctx)
	if err != nil {
		return err
	}
	ui.seq = last
	if err := sub.Call(ctx, "events.subscribe", map[string]int64{"since": last}, nil); err != nil {
		return err
	}
	go ui.events(ctx, sub)
	fmt.Fprintln(ui.out, "\n输入消息后回车发送；/help 查看命令，/quit 退出。")

	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(ui.out)
			return nil
		case line, ok := <-lines:
			if !ok {
				return nil
			}
			quit, err := ui.input(ctx, strings.TrimSpace(line))
			if err != nil {
				fmt.Fprintln(ui.out, "出错了："+err.Error())
			}
			if quit {
				return nil
			}
		}
	}
}

func (ui *chatUI) threads(ctx context.Context) ([]chatThread, error) {
	var res struct{ Threads []chatThread }
	err := ui.client().Call(ctx, "threads.list", nil, &res)
	return res.Threads, err
}

// history prints the last messages of the thread and returns the newest seq.
func (ui *chatUI) history(ctx context.Context) (int64, error) {
	var res struct {
		Messages []chatMessage `json:"messages"`
		LastSeq  int64         `json:"last_seq"`
	}
	if err := ui.client().Call(ctx, "messages.list", map[string]any{"thread_id": ui.thread.ID, "limit": 20}, &res); err != nil {
		return 0, err
	}
	fmt.Fprintf(ui.out, "── %s ──\n", ui.thread.Title)
	for _, m := range res.Messages {
		ui.print(m)
	}
	return res.LastSeq, nil
}

func (ui *chatUI) print(m chatMessage) {
	if ui.shown[m.ID] {
		return
	}
	ui.shown[m.ID] = true
	at := time.UnixMilli(m.CreatedAt).Format("15:04")
	switch {
	case m.Kind == "silent":
		fmt.Fprintf(ui.out, "  （%s 已读，不需要回复）\n", at)
	case m.Role == "user" && m.Kind == "trigger":
		fmt.Fprintf(ui.out, "[%s 自动] %s\n", at, firstLineOf(m.Content))
	case m.Role == "user":
		fmt.Fprintf(ui.out, "[%s 你] %s\n", at, m.Content)
	default:
		label := map[string]string{"notice": "提醒", "report": "任务汇报", "inspector": "巡检"}[m.Kind]
		if label == "" {
			label = "sous"
		}
		fmt.Fprintf(ui.out, "[%s %s] %s\n", at, label, m.Content)
	}
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// events prints what happens in the thread as it happens. When the daemon
// goes away (it restarts after an update or a crash) it reconnects.
func (ui *chatUI) events(ctx context.Context, sub *ipc.Client) {
	for {
		for n := range sub.Notifications() {
			switch n.Method {
			case "hub":
				var p struct{ Connected bool }
				json.Unmarshal(n.Params, &p)
				if !p.Connected {
					fmt.Fprintln(ui.out, "（和 Hub 的连接断了，正在重连…）")
				}
			case "event":
				ui.event(n.Params)
			}
		}
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintln(ui.out, "（和 daemon 的连接断了，正在重连…）")
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			if next, err := ui.reconnect(ctx); err == nil {
				sub = next
				fmt.Fprintln(ui.out, "（已重新连上 daemon）")
				break
			}
		}
	}
}

// reconnect dials the daemon again and resubscribes after the last event seen.
func (ui *chatUI) reconnect(ctx context.Context) (*ipc.Client, error) {
	c, err := ipc.Dial(ui.path)
	if err != nil {
		return nil, err
	}
	sub, err := ipc.Dial(ui.path)
	if err != nil {
		c.Close()
		return nil, err
	}
	ui.mu.Lock()
	since := ui.seq
	ui.mu.Unlock()
	if err := sub.Call(ctx, "events.subscribe", map[string]int64{"since": since}, nil); err != nil {
		c.Close()
		sub.Close()
		return nil, err
	}
	ui.mu.Lock()
	old, oldSub := ui.c, ui.sub
	ui.c, ui.sub = c, sub
	ui.mu.Unlock()
	old.Close()
	oldSub.Close()
	return sub, nil
}

func (ui *chatUI) client() *ipc.Client {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	return ui.c
}

func (ui *chatUI) event(raw json.RawMessage) {
	var ev struct {
		Seq      int64           `json:"seq"`
		Type     string          `json:"type"`
		ThreadID string          `json:"thread_id"`
		TS       int64           `json:"ts"`
		Payload  json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return
	}
	ui.mu.Lock()
	defer ui.mu.Unlock()
	if ev.Seq > ui.seq {
		ui.seq = ev.Seq
	}
	if ev.ThreadID != ui.thread.ID && ev.Type != "approval.requested" {
		return
	}
	var p struct {
		MessageID string `json:"message_id"`
		Content   string `json:"content"`
		Delta     string `json:"delta"`
		Thinking  bool   `json:"thinking"`
		Kind      string `json:"kind"`
		Error     bool   `json:"error"`
		Title     string `json:"title"`
		Review    string `json:"review"`
		Status    string `json:"status"`
		Agent     string `json:"agent"`
		Summary   string `json:"summary"`
	}
	json.Unmarshal(ev.Payload, &p)
	switch ev.Type {
	case "message.assistant.delta":
		if p.Thinking {
			return
		}
		if ui.streaming != p.MessageID {
			ui.streaming = p.MessageID
			fmt.Fprintf(ui.out, "[%s sous] ", time.Now().Format("15:04"))
		}
		fmt.Fprint(ui.out, p.Delta)
	case "message.assistant":
		if ui.streaming == p.MessageID {
			ui.streaming = ""
			ui.shown[p.MessageID] = true
			fmt.Fprintln(ui.out)
			return
		}
		ui.print(chatMessage{ID: p.MessageID, Role: "assistant", Content: p.Content, Kind: p.Kind, Error: p.Error, CreatedAt: ev.TS})
	case "message.user":
		if ui.sent[p.Content] > 0 {
			ui.sent[p.Content]--
			ui.mine[p.MessageID] = true
		}
		if ui.mine[p.MessageID] {
			ui.shown[p.MessageID] = true
			return
		}
		ui.print(chatMessage{ID: p.MessageID, Role: "user", Content: p.Content, CreatedAt: ev.TS})
	case "trigger.fired":
		ui.print(chatMessage{ID: p.MessageID, Role: "user", Kind: "trigger", Content: p.Content, CreatedAt: ev.TS})
	case "run.queued":
		fmt.Fprintf(ui.out, "  · 已派给 %s\n", p.Agent)
	case "run.ended":
		fmt.Fprintf(ui.out, "  · Run 结束：%s\n", p.Status)
	case "approval.requested":
		if p.Review != "ai" {
			fmt.Fprintf(ui.out, "  ! 需要你审批：%s（/approvals 查看）\n", p.Title)
		}
	}
}

const chatHelp = `命令：
  /threads            列出对话
  /use <序号>          切换到某个对话
  /new [标题]          新建对话并切换过去
  /approvals          列出待审批的操作
  /approve <序号> [附言] 批准
  /reject <序号> [附言]  拒绝
  /status             daemon 和 Hub 连接的状态
  /quit               退出`

// input handles one line; it reports whether to quit.
func (ui *chatUI) input(ctx context.Context, line string) (bool, error) {
	if line == "" {
		return false, nil
	}
	if !strings.HasPrefix(line, "/") {
		var res struct {
			MessageID string `json:"message_id"`
		}
		// The echo can arrive before the answer: remember the text first.
		ui.mu.Lock()
		ui.sent[line]++
		ui.mu.Unlock()
		if err := ui.client().Call(ctx, "chat.send", map[string]string{"thread_id": ui.thread.ID, "content": line}, &res); err != nil {
			ui.mu.Lock()
			ui.sent[line]--
			ui.mu.Unlock()
			return false, fmt.Errorf("%w（daemon 重连后再发一次）", err)
		}
		ui.mu.Lock()
		ui.mine[res.MessageID] = true
		ui.mu.Unlock()
		return false, nil
	}
	cmd, arg, _ := strings.Cut(line[1:], " ")
	arg = strings.TrimSpace(arg)
	switch cmd {
	case "quit", "exit", "q":
		return true, nil
	case "help", "h":
		fmt.Fprintln(ui.out, chatHelp)
	case "threads":
		ts, err := ui.threads(ctx)
		if err != nil {
			return false, err
		}
		for i, t := range ts {
			mark := " "
			if t.ID == ui.thread.ID {
				mark = "*"
			}
			fmt.Fprintf(ui.out, "%s %d. %s\n", mark, i+1, t.Title)
		}
	case "use", "new":
		var t chatThread
		if cmd == "new" {
			if arg == "" {
				arg = "新对话"
			}
			if err := ui.client().Call(ctx, "threads.create", map[string]string{"title": arg}, &t); err != nil {
				return false, err
			}
		} else {
			ts, err := ui.threads(ctx)
			if err != nil {
				return false, err
			}
			n, err := strconv.Atoi(arg)
			if err != nil || n < 1 || n > len(ts) {
				return false, errors.New("用 /threads 看序号")
			}
			t = ts[n-1]
		}
		ui.mu.Lock()
		ui.thread = t
		ui.mu.Unlock()
		_, err := ui.history(ctx)
		return false, err
	case "approvals":
		var res struct{ Approvals []chatApproval }
		if err := ui.client().Call(ctx, "approvals.list", nil, &res); err != nil {
			return false, err
		}
		ui.approvals = nil
		for _, a := range res.Approvals {
			if a.Review == "user" {
				ui.approvals = append(ui.approvals, a)
			}
		}
		if len(ui.approvals) == 0 {
			fmt.Fprintln(ui.out, "没有等你审批的操作。")
		}
		for i, a := range ui.approvals {
			fmt.Fprintf(ui.out, "%d. %s\n%s\n\n", i+1, a.Title, indent(a.Detail))
		}
	case "approve", "reject":
		idx, note, _ := strings.Cut(arg, " ")
		n, err := strconv.Atoi(idx)
		if err != nil || n < 1 || n > len(ui.approvals) {
			return false, errors.New("先用 /approvals 列出，再用序号")
		}
		a := ui.approvals[n-1]
		want := map[string][]string{"approve": {"allow_once", "allow_always"}, "reject": {"reject_once", "reject_always"}}[cmd]
		opt := ""
		for _, k := range want {
			for _, o := range a.Options {
				if o.Kind == k && opt == "" {
					opt = o.OptionID
				}
			}
		}
		if opt == "" {
			return false, errors.New("这个审批没有对应的选项")
		}
		if err := ui.client().Call(ctx, "approvals.decide", map[string]string{"id": a.ID, "option_id": opt, "note": strings.TrimSpace(note)}, nil); err != nil {
			return false, err
		}
		fmt.Fprintln(ui.out, "已提交。")
	case "status":
		var st struct {
			Version      string `json:"version"`
			Name         string `json:"name"`
			HubURL       string `json:"hub_url"`
			HubConnected bool   `json:"hub_connected"`
			Runs         []struct {
				Agent  string `json:"agent"`
				Paused bool   `json:"paused"`
			} `json:"runs"`
		}
		if err := ui.client().Call(ctx, "status", nil, &st); err != nil {
			return false, err
		}
		conn := "已连接"
		if !st.HubConnected {
			conn = "未连接"
		}
		fmt.Fprintf(ui.out, "%s（sous %s），Hub %s %s，正在执行 %d 个 Run\n", st.Name, st.Version, st.HubURL, conn, len(st.Runs))
	default:
		return false, fmt.Errorf("不认识的命令 /%s；/help 查看", cmd)
	}
	return false, nil
}

func indent(s string) string {
	if s == "" {
		return ""
	}
	return "   " + strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n   ")
}
