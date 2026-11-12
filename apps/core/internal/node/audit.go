package node

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
)

// auditLog is an append-only, hash-chained record of every command this
// machine received and what it decided (design §10.4). The page reads it
// over a session channel and checks the chain.
type auditLog struct {
	mu   sync.Mutex
	path string
	seq  int64
	prev string
	tail []AuditEntry // the last entries, for quick reads
}

// AuditEntry is one decision.
type AuditEntry struct {
	Seq    int64  `json:"seq"`
	At     int64  `json:"at"`
	From   string `json:"from"` // brain, sess:<id>, self
	Type   string `json:"type"`
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
	Digest string `json:"digest,omitempty"` // SHA-256 of the command body
	Prev   string `json:"prev"`
	Hash   string `json:"hash"`
}

const auditTail = 2000

func openAudit(path string) (*auditLog, error) {
	a := &auditLog{path: path}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e AuditEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			a.seq, a.prev = e.Seq, e.Hash
			a.tail = append(a.tail, e)
			if len(a.tail) > auditTail {
				a.tail = a.tail[len(a.tail)-auditTail:]
			}
		}
	}
	return a, nil
}

// AuditHash is the chain hash of an entry (Hash itself left out).
func AuditHash(e AuditEntry) string {
	e.Hash = ""
	raw, _ := json.Marshal(e)
	h := sha256.Sum256(raw)
	return trust.B64.EncodeToString(h[:])
}

func (a *auditLog) add(from, typ string, ok bool, reason string, body []byte) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	e := AuditEntry{Seq: a.seq + 1, At: time.Now().Unix(), From: from, Type: typ, OK: ok, Reason: reason, Prev: a.prev}
	if len(body) > 0 {
		h := sha256.Sum256(body)
		e.Digest = trust.B64.EncodeToString(h[:])
	}
	e.Hash = AuditHash(e)
	raw, _ := json.Marshal(e)
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		f.Write(append(raw, '\n'))
		f.Close()
	}
	a.seq, a.prev = e.Seq, e.Hash
	a.tail = append(a.tail, e)
	if len(a.tail) > auditTail {
		a.tail = a.tail[1:]
	}
}

func (a *auditLog) since(seq int64, limit int) []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []AuditEntry
	for _, e := range a.tail {
		if e.Seq > seq {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func (a *auditLog) head() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{"seq": a.seq, "hash": a.prev}
}
