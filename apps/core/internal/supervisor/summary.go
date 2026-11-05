package supervisor

import (
	"regexp"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/prompts"
)

// A summary is Markdown: "## <section>" headings (prompts.SummarySections)
// with "- " items, each ending in the seqs of its sources ([#1203]).

// guarded are the sections whose items may only leave through SummaryDone.
var guarded = []string{"已做的决定", "待办和承诺"}

type item struct {
	section string
	text    string
	refs    []string
}

var refRe = regexp.MustCompile(`\[#\d+\]`)

// parseSummary returns the items of a summary by section.
func parseSummary(s string) []item {
	var out []item
	section := ""
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "#"):
			section = strings.TrimSpace(strings.TrimLeft(t, "#"))
		case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* "):
			text := strings.TrimSpace(t[2:])
			if text == "" || text == "无" || text == "（无）" {
				continue
			}
			out = append(out, item{section: section, text: text, refs: refRe.FindAllString(text, -1)})
		}
	}
	return out
}

// normalize keeps the letters, digits and CJK characters of s, without refs.
func normalize(s string) string {
	s = refRe.ReplaceAllString(s, "")
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 0x2E80 || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// kept reports whether the new items still hold it: an item of the same
// section or of SummaryDone shares one of its sources, or (without sources)
// starts with the same words.
func kept(it item, next []item) bool {
	key := normalize(it.text)
	if r := []rune(key); len(r) > 12 {
		key = string(r[:12])
	}
	for _, n := range next {
		if n.section != it.section && n.section != prompts.SummaryDone {
			continue
		}
		for _, r := range it.refs {
			for _, x := range n.refs {
				if r == x {
					return true
				}
			}
		}
		if key != "" && strings.Contains(normalize(n.text), key) {
			return true
		}
	}
	return false
}

// missingItems lists the decisions and commitments of old that next drops
// without moving them to SummaryDone.
func missingItems(old, next string) []item {
	ni := parseSummary(next)
	var out []item
	for _, it := range parseSummary(old) {
		for _, g := range guarded {
			if it.section == g && !kept(it, ni) {
				out = append(out, it)
			}
		}
	}
	return out
}

func lines(items []item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = "- （" + it.section + "）" + it.text
	}
	return out
}

// restore puts dropped items back into their sections of summary, adding a
// section when it is missing.
func restore(summary string, missing []item) string {
	ls := strings.Split(summary, "\n")
	for _, it := range missing {
		at := -1
		for i, l := range ls {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "#") && strings.TrimSpace(strings.TrimLeft(t, "#")) == it.section {
				at = i + 1
				for at < len(ls) && !strings.HasPrefix(strings.TrimSpace(ls[at]), "#") {
					at++
				}
				for at > i+1 && strings.TrimSpace(ls[at-1]) == "" {
					at--
				}
				break
			}
		}
		line := "- " + it.text
		if at < 0 {
			ls = append([]string{"## " + it.section, line, ""}, ls...)
			continue
		}
		ls = append(ls[:at], append([]string{line}, ls[at:]...)...)
	}
	return strings.Join(ls, "\n")
}
