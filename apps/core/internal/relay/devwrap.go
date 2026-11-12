package relay

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
)

// Device wraps (decision 0033): a device whose passkey has no PRF (Windows
// Hello on most builds) keeps the identity encrypted under a key that never
// leaves that browser. The relay holds that ciphertext and hands it out only
// for a fresh passkey assertion of the same device, so neither the relay
// alone nor a copy of the browser profile alone opens the identity.

const devNonceTTL = 2 * trust.AssertionTTL

func (s *Server) devwrapChallenge(c *conn, f frame) error {
	n := trust.B64.EncodeToString(trust.RandomBytes(32))
	now := s.Now()
	s.mu.Lock()
	for k, t := range s.devNonce {
		if now.Sub(t) > devNonceTTL {
			delete(s.devNonce, k)
		}
	}
	if len(s.devNonce) > 1000 {
		s.mu.Unlock()
		return errors.New("请稍后再试")
	}
	s.devNonce[n] = now
	s.mu.Unlock()
	c.send(frame{T: "devwrap.challenge", RID: f.RID, Nonce: n})
	return nil
}

func (s *Server) devwrapGet(c *conn, f frame) error {
	s.mu.Lock()
	t, ok := s.devNonce[f.Nonce]
	delete(s.devNonce, f.Nonce)
	r := s.roster
	s.mu.Unlock()
	if !ok || s.Now().Sub(t) > devNonceTTL {
		return errors.New("验证已过期，请重试")
	}
	if r == nil {
		return errors.New("这个中继还没有完成首次设置")
	}
	var a trust.Assertion
	if err := json.Unmarshal(f.Data, &a); err != nil {
		return err
	}
	if a.CredID != f.Name {
		return errors.New("断言不属于这台设备的 Passkey")
	}
	challenge, err := trust.B64.DecodeString(f.Nonce)
	if err != nil {
		return err
	}
	if err := trust.VerifyAssertion(r, a, challenge, s.Now(), nil); err != nil {
		return err
	}
	var body []byte
	err = s.db.QueryRow(`SELECT body FROM devwraps WHERE cred = ?`, f.Name).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("这台设备的密钥包不在中继上，请在已解锁的设备上重新添加它")
	}
	if err != nil {
		return err
	}
	c.send(frame{T: "devwrap", RID: f.RID, Name: f.Name, Body: body})
	return nil
}

// devwrapPut stores a device wrap from an unlocked session, for a passkey
// the roster lists.
func (s *Server) devwrapPut(c *conn, f frame) error {
	if !isSessionAddr(c.addr) {
		return errors.New("只有用户的会话能保存设备密钥包")
	}
	s.mu.Lock()
	r := s.roster
	s.mu.Unlock()
	if r == nil {
		return errors.New("这个中继还没有完成首次设置")
	}
	if _, ok := r.Passkey(f.Name); !ok {
		return errors.New("名册里没有这个 Passkey")
	}
	if len(f.Body) == 0 || len(f.Body) > 64<<10 {
		return errors.New("设备密钥包大小不对")
	}
	if _, err := s.db.Exec(`INSERT INTO devwraps(cred, body, updated) VALUES (?,?,?) ON CONFLICT(cred) DO UPDATE SET body = excluded.body, updated = excluded.updated`,
		f.Name, []byte(f.Body), s.Now().Unix()); err != nil {
		return err
	}
	c.send(frame{T: "ok", RID: f.RID})
	return nil
}

func isSessionAddr(addr string) bool { return len(addr) > 5 && addr[:5] == "sess:" }
