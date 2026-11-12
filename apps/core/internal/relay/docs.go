package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
)

// put stores a document after the same checks a receiver makes, so an
// honest relay never hands out something a receiver would reject.
func (s *Server) put(c *conn, name string, body json.RawMessage) error {
	s.mu.Lock()
	id, cur := s.identity, s.roster
	s.mu.Unlock()
	isSession := strings.HasPrefix(c.addr, "sess:")
	now := s.Now()
	used := func(ch string) bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		for k, t := range s.usedAsrt {
			if now.Sub(t) > 2*trust.AssertionTTL {
				delete(s.usedAsrt, k)
			}
		}
		_, seen := s.usedAsrt[ch]
		s.usedAsrt[ch] = now
		return seen
	}
	switch {
	case name == "roster":
		if !isSession {
			return errors.New("只有用户的会话能改名册")
		}
		var u trust.RosterUpdate
		if err := json.Unmarshal(body, &u); err != nil {
			return err
		}
		next, err := trust.AcceptRoster(id, cur, u, now, used)
		if err != nil {
			return err
		}
		// Every version stays readable as roster@<v>: a machine that missed
		// one checks each step against the passkeys of the version before.
		if err := s.store(fmt.Sprintf("roster@%d", next.V), body); err != nil {
			return err
		}
		if err := s.store(name, body); err != nil {
			return err
		}
		s.mu.Lock()
		s.roster = &next
		s.mu.Unlock()
		s.evict(next)
	case name == "scopes":
		if !isSession {
			return errors.New("只有用户的会话能改自主范围")
		}
		var u trust.ScopesUpdate
		if err := json.Unmarshal(body, &u); err != nil {
			return err
		}
		var prev *trust.Scopes
		if old, err := s.doc(name); err == nil {
			var o trust.ScopesUpdate
			if json.Unmarshal(old, &o) == nil {
				if sc, err := o.Open(id); err == nil {
					prev = &sc
				}
			}
		}
		if _, err := trust.AcceptScopes(cur, prev, u, now, used); err != nil {
			return err
		}
		return s.store(name, body)
	case name == "pause" || name == "domain":
		if !isSession {
			return errors.New("只有用户的会话能写这个文档")
		}
		var sd trust.Signed
		if err := json.Unmarshal(body, &sd); err != nil {
			return err
		}
		var v struct {
			V int64 `json:"v"`
		}
		if _, err := sd.Open(id, name, &v); err != nil {
			return err
		}
		if old, err := s.doc(name); err == nil {
			var o trust.Signed
			var ov struct {
				V int64 `json:"v"`
			}
			if json.Unmarshal(old, &o) == nil {
				raw, _ := o.Payload()
				json.Unmarshal(raw, &ov)
			}
			if v.V <= ov.V {
				return fmt.Errorf("版本 %d 不比现有的 %d 新", v.V, ov.V)
			}
		}
		return s.store(name, body)
	case name == "identity":
		if !isSession {
			return errors.New("只有用户的会话能改身份包")
		}
		var b struct {
			SignPub string `json:"sign_pub"`
		}
		if err := json.Unmarshal(body, &b); err != nil || b.SignPub != trust.B64.EncodeToString(id) {
			return errors.New("身份包不属于这个中继的主人")
		}
		return s.store(name, body)
	case name == "lease":
		var sd trust.Signed
		if err := json.Unmarshal(body, &sd); err != nil {
			return err
		}
		l, err := trust.OpenLease(cur, sd)
		if err != nil {
			return err
		}
		if c.addr != trust.NodeAddr(l.Holder) {
			return errors.New("只能为自己声明租约")
		}
		if old, err := s.doc(name); err == nil {
			var o trust.Signed
			if json.Unmarshal(old, &o) == nil {
				if ol, err := trust.OpenLease(cur, o); err == nil && ol.Live(now) && !l.Newer(ol) {
					return fmt.Errorf("租约冲突：第 %d 任由 %s 持有", ol.Term, ol.Holder)
				}
				if ol, err := trust.OpenLease(cur, o); err == nil && l.Term < ol.Term {
					return fmt.Errorf("任期 %d 比现有的 %d 旧", l.Term, ol.Term)
				}
			}
		}
		return s.store(name, body)
	case name == "snapshot":
		if !s.isHolder(c.addr) {
			return errors.New("只有持有租约的大脑能写快照")
		}
		return s.store(name, body)
	case strings.HasPrefix(name, "alert/"):
		if c.addr != "node:"+strings.TrimPrefix(name, "alert/") {
			return errors.New("机器只能写自己的警报")
		}
		return s.store(name, body)
	default:
		return fmt.Errorf("未知的文档 %q", name)
	}
	return nil
}

func (s *Server) store(name string, body []byte) error {
	if _, err := s.db.Exec(`INSERT INTO docs(name, body, updated) VALUES (?,?,?) ON CONFLICT(name) DO UPDATE SET body = excluded.body, updated = excluded.updated`,
		name, []byte(body), s.Now().Unix()); err != nil {
		return err
	}
	s.broadcast(frame{T: "doc", Name: name, Body: body})
	return nil
}

// evict disconnects machines the roster no longer lists.
func (s *Server) evict(r trust.Roster) {
	s.mu.Lock()
	var gone []*conn
	for addr, cs := range s.conns {
		if id, ok := strings.CutPrefix(addr, "node:"); ok {
			if _, in := r.Node(id); !in {
				gone = append(gone, cs...)
			}
		}
	}
	s.mu.Unlock()
	for _, c := range gone {
		go c.stopAfterFlush() // it gets the roster that removed it first
	}
}
