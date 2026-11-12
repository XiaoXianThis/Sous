package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/prompts"
)

// The summary's guarded sections only leave through SummaryDone, so on a
// long-lived thread they would grow without end. After each compaction the
// code tidies the summary:
//   - a commitment whose newest source is older than StaleTodoAge is dropped
//     (moved to SummaryDone, which the next compaction discards);
//   - once the summary passes SummaryBudgetRatio of the window, the oldest
//     decisions and preferences move into long-term memory, down to
//     SummaryTargetRatio, and one line in 其他背景 says so.
const (
	SummaryBudgetRatio = 0.08
	SummaryTargetRatio = 0.05
	StaleTodoAge       = 14 * 24 * time.Hour
)

const (
	secDecisions   = "已做的决定"
	secTodos       = "待办和承诺"
	secPreferences = "用户的偏好和纠正"
	secBackground  = "其他背景"
	archivedNote   = "较早的决定和偏好已归档到长期记忆"
)

var archivedRe = regexp.MustCompile(archivedNote + `（共 (\d+) 条）`)

// sumDoc is a summary split into its sections, so items can be moved.
type sumDoc struct {
	head []string
	secs []sumSec
}

type sumSec struct {
	name  string
	lines []string
}

func parseDoc(s string) *sumDoc {
	d := &sumDoc{}
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			d.secs = append(d.secs, sumSec{name: strings.TrimSpace(strings.TrimLeft(t, "#"))})
			continue
		}
		if len(d.secs) == 0 {
			d.head = append(d.head, line)
			continue
		}
		d.secs[len(d.secs)-1].lines = append(d.secs[len(d.secs)-1].lines, line)
	}
	return d
}

func (d *sumDoc) String() string {
	var out []string
	if h := strings.TrimSpace(strings.Join(d.head, "\n")); h != "" {
		out = append(out, h, "")
	}
	for _, s := range d.secs {
		out = append(out, "## "+s.name)
		var body []string
		for _, l := range s.lines {
			if strings.TrimSpace(l) != "" {
				body = append(body, l)
			}
		}
		if len(body) == 0 {
			body = []string{"- 无"}
		}
		out = append(out, body...)
		out = append(out, "")
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// sec returns the section, adding it in the standard order when missing.
func (d *sumDoc) sec(name string) *sumSec {
	for i := range d.secs {
		if d.secs[i].name == name {
			return &d.secs[i]
		}
	}
	order := func(n string) int {
		for i, x := range prompts.SummarySections {
			if x == n {
				return i
			}
		}
		return len(prompts.SummarySections)
	}
	at := len(d.secs)
	for i, s := range d.secs {
		if order(s.name) > order(name) {
			at = i
			break
		}
	}
	d.secs = append(d.secs[:at], append([]sumSec{{name: name}}, d.secs[at:]...)...)
	return &d.secs[at]
}

// items lists the items of a section, with their line index.
func (d *sumDoc) items(name string) (out []item, at []int) {
	for i := range d.secs {
		if d.secs[i].name != name {
			continue
		}
		for j, line := range d.secs[i].lines {
			for _, it := range parseSummary("## " + name + "\n" + line) {
				out = append(out, it)
				at = append(at, j)
			}
		}
	}
	return out, at
}

// remove drops the lines of a section by index.
func (d *sumDoc) remove(name string, idx []int) {
	drop := map[int]bool{}
	for _, i := range idx {
		drop[i] = true
	}
	s := d.sec(name)
	var keep []string
	for i, l := range s.lines {
		if !drop[i] {
			keep = append(keep, l)
		}
	}
	s.lines = keep
}

// add appends an item to a section, replacing its "- 无".
func (d *sumDoc) add(name, text string) {
	s := d.sec(name)
	var keep []string
	for _, l := range s.lines {
		if t := strings.TrimSpace(l); t != "- 无" && t != "- （无）" {
			keep = append(keep, l)
		}
	}
	s.lines = append(keep, "- "+text)
}

// prose is a summary without its code-owned receipt archive.
func prose(summary string) string {
	p, _, _ := strings.Cut(summary, receiptArchive)
	return strings.TrimSpace(p)
}

// tidy applies the stale-commitment and archive rules to a new summary.
// old is the previous summary, which may hold the archive note the
// summarizer dropped.
func (s *Supervisor) tidy(ctx context.Context, threadID, old, summary string, archive bool) string {
	text, receipts, hasReceipts := strings.Cut(summary, receiptArchive)
	d := parseDoc(text)

	// Stale commitments.
	now := s.opt.Now()
	todos, at := d.items(secTodos)
	var stale []int
	for i, it := range todos {
		if t, ok := s.newestSource(ctx, it); ok && now.Sub(t) > StaleTodoAge {
			stale = append(stale, at[i])
			d.add(prompts.SummaryDone, "过期丢弃（"+t.Format("2006-01-02")+" 后没有进展）："+it.text)
		}
	}
	if len(stale) > 0 {
		d.remove(secTodos, stale)
		slog.Info("supervisor: dropped stale commitments", "thread", threadID, "items", len(stale))
	}

	// The archive note: carry the count over if the summarizer lost it.
	count := archivedCount(prose(old))
	if n := archivedCount(d.String()); n > count {
		count = n
	}
	bg, bgAt := d.items(secBackground)
	var notes []int
	for i, it := range bg {
		if strings.Contains(it.text, archivedNote) {
			notes = append(notes, bgAt[i])
		}
	}
	d.remove(secBackground, notes)

	if archive {
		count += s.archiveOld(ctx, threadID, d, count)
	}
	if count > 0 {
		d.add(secBackground, fmt.Sprintf("%s（共 %d 条），需要时用 memory_search 查，原文用 thread_search。", archivedNote, count))
	}
	out := d.String()
	if hasReceipts {
		out += receiptArchive + receipts
	}
	return out
}

func archivedCount(s string) int {
	m := archivedRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// archiveOld moves the oldest decisions and preferences into long-term
// memory while the summary is over its budget. It returns how many moved.
func (s *Supervisor) archiveOld(ctx context.Context, threadID string, d *sumDoc, count int) int {
	w := float64(s.window(ctx))
	if float64(estimateText(d.String())) <= SummaryBudgetRatio*w {
		return 0
	}
	type cand struct {
		section string
		it      item
		line    int
		when    time.Time
		dated   bool
	}
	var cands []cand
	for _, sec := range []string{secDecisions, secPreferences} {
		its, at := d.items(sec)
		for i, it := range its {
			t, ok := s.newestSource(ctx, it)
			cands = append(cands, cand{sec, it, at[i], t, ok})
		}
	}
	// Oldest first; items without a dated source last.
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.dated != b.dated {
			return a.dated
		}
		return a.when.Before(b.when)
	})
	n := 0
	for _, c := range cands {
		if float64(estimateText(d.String())) <= SummaryTargetRatio*w {
			break
		}
		if err := s.remember(ctx, threadID, c.section, c.it.text); err != nil {
			slog.Warn("supervisor: archive summary item", "thread", threadID, "err", err)
			continue // keep it in the summary
		}
		d.remove(c.section, []int{c.line})
		// Later indexes of the section shifted by one.
		for k := range cands {
			if cands[k].section == c.section && cands[k].line > c.line {
				cands[k].line--
			}
		}
		n++
	}
	if n > 0 {
		slog.Info("supervisor: archived summary items to memory", "thread", threadID, "items", n)
	}
	return n
}

// remember saves a summary item as a memory unless one already holds it.
func (s *Supervisor) remember(ctx context.Context, threadID, section, text string) error {
	key := normalize(text)
	if r := []rune(key); len(r) > 12 {
		key = string(r[:12])
	}
	if ms, err := s.opt.Log.Store.SearchMemories(ctx, refRe.ReplaceAllString(text, ""), 5); err == nil {
		for _, m := range ms {
			if key != "" && strings.Contains(normalize(m.Content), key) {
				return nil
			}
		}
	}
	kind, content := "note", "对话里做过的决定："+text
	if section == secPreferences {
		kind, content = "profile", text
	}
	_, err := s.opt.Log.Append(ctx, event.New(event.MemoryPut, event.ActorSupervisor, threadID,
		event.MemoryPayload{MemoryID: ids.New(), Kind: kind, Content: content}))
	return err
}

// newestSource is when the newest message an item cites was written.
func (s *Supervisor) newestSource(ctx context.Context, it item) (time.Time, bool) {
	var newest int64
	for _, r := range it.refs {
		seq, err := strconv.ParseInt(strings.Trim(r, "[#]"), 10, 64)
		if err != nil {
			continue
		}
		m, err := s.opt.Log.Store.MessageBySeq(ctx, seq)
		if err != nil {
			continue
		}
		if m.CreatedAt > newest {
			newest = m.CreatedAt
		}
	}
	if newest == 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(newest), true
}
