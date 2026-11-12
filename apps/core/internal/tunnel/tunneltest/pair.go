package tunneltest

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
)

// PairPage acts as the page's "add a machine": it returns the pairing
// string, and wait() completes the exchange (adding the machine to the
// roster, as a brain when brain >= 0) and returns the machine's id.
func (w *World) PairPage(brain int) (code string, wait func() (string, error)) {
	secret := trust.RandomBytes(16)
	pc := trust.PairCode{RelayHash: trust.RelayHash(w.URL), IdentityFP: trust.Fingerprint(w.ID.Public()), Secret: secret,
		Exp: time.Now().Add(trust.PairTTL)}
	room := trust.PairRoom(secret)
	ctx := context.Background()
	s := w.Session()
	if err := s.OpenRoom(ctx, room); err != nil {
		w.T.Fatal(err)
	}
	return pc.Encode(), func() (string, error) {
		wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		m, err := s.RoomReceive(wctx, room)
		if err != nil {
			return "", err
		}
		body, _ := trust.B64.DecodeString(m.Body)
		if !trust.CheckPairMAC(secret, "node", body, m.MAC) {
			return "", errors.New("bad MAC from the machine")
		}
		var h trust.PairHello
		if err := json.Unmarshal(body, &h); err != nil {
			return "", err
		}
		pub, _ := trust.B64.DecodeString(h.SignPub)
		if trust.NodeIDOf(pub) != h.Node {
			return "", errors.New("machine id does not match its key")
		}
		w.Update(func(r *trust.Roster) {
			n := trust.RosterNode{ID: h.Node, Name: h.Name, SignPub: h.SignPub, KEMPub: h.KEMPub, KeyStorage: h.KeyStorage, OS: h.OS, Role: h.Role, Added: time.Now().Unix()}
			if brain >= 0 {
				n.Brain = &trust.Brain{Priority: brain}
			}
			r.Nodes = append(r.Nodes, n)
		})
		w.mu.Lock()
		doc := w.doc
		w.mu.Unlock()
		// The welcome carries the newest roster; the machine checks the
		// identity against the pairing string, not the relay.
		raw, _ := json.Marshal(trust.PairWelcome{T: "pair.welcome", Roster: trust.RosterUpdate{Doc: doc}})
		return h.Node, s.RoomSend(ctx, room, trust.PairMsg{Body: trust.B64.EncodeToString(raw), MAC: trust.PairMAC(secret, "app", raw)})
	}
}
