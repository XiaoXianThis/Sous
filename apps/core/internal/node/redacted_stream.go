package node

import (
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// Hold complete lines so secrets split between ACP chunks are filtered once.
// Oversized lines are discarded until newline rather than leaking fragments.
type redactedLine struct {
	pending string
	dropped bool
	private bool
}

func (s *redactedLine) push(chunk string, final bool) string {
	var out strings.Builder
	for len(chunk) > 0 {
		i := strings.IndexByte(chunk, '\n')
		part := chunk
		if i >= 0 {
			part = chunk[:i+1]
		}
		if !s.dropped {
			if len(s.pending)+len(part) > 16<<10 {
				s.pending = ""
				s.dropped = true
			} else {
				s.pending += part
			}
		}
		if i < 0 {
			break
		}
		if s.dropped {
			out.WriteString("[oversized line redacted]\n")
		} else {
			if s.private {
				if strings.Contains(s.pending, "-----END") {
					s.private = false
				}
			} else if strings.Contains(s.pending, "-----BEGIN") {
				s.private = true
				out.WriteString("***\n")
			} else {
				out.WriteString(mask(s.pending))
			}
		}
		s.pending = ""
		s.dropped = false
		chunk = chunk[i+1:]
	}
	// A pending run of non-ASCII text cannot be a fragment of any supported
	// credential syntax. Keep Chinese progress messages responsive without
	// flushing partial ASCII tokens or key blocks.
	unicodeOnly := s.pending != "" && !s.private && !s.dropped
	for _, r := range s.pending {
		if r < 128 && r != ' ' && r != '\t' {
			unicodeOnly = false
			break
		}
	}
	if unicodeOnly {
		out.WriteString(s.pending)
		s.pending = ""
	}
	if final {
		if s.dropped {
			out.WriteString("[oversized line redacted]")
		} else {
			if s.private {
				if strings.Contains(s.pending, "-----END") {
					s.private = false
				}
			} else if strings.Contains(s.pending, "-----BEGIN") {
				s.private = true
				out.WriteString("***\n")
			} else {
				out.WriteString(mask(s.pending))
			}
		}
		s.pending = ""
		s.dropped = false
	}
	return out.String()
}
func (r *run) streamText(kind, chunk string, final bool) {
	r.mu.Lock()
	if r.streams == nil {
		r.streams = map[string]*redactedLine{}
	}
	s := r.streams[kind]
	if s == nil {
		s = &redactedLine{}
		r.streams[kind] = s
	}
	text := s.push(chunk, final)
	r.mu.Unlock()
	if text != "" {
		r.d.ob.ephemeral(proto.New(proto.TRunOutput, proto.RunUpdate{RunID: r.spec.RunID, Kind: kind, Text: text}))
	}
}
func (r *run) flushStreams() {
	r.streamText(proto.UpdateText, "", true)
	r.streamText(proto.UpdateThought, "", true)
}
