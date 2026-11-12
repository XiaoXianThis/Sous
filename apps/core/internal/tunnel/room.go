package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/coder/websocket"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
)

func (c *Client) roomMsg(f rframe) {
	var m trust.PairMsg
	if json.Unmarshal(f.Data, &m) != nil {
		return
	}
	c.mu.Lock()
	ch := c.rooms[f.Room]
	c.mu.Unlock()
	if ch != nil {
		select {
		case ch <- m:
		default:
		}
	}
}

// OpenRoom opens a pairing room as the page does (tests use this side).
func (c *Client) OpenRoom(ctx context.Context, room string) error {
	c.mu.Lock()
	if c.rooms == nil {
		c.rooms = map[string]chan trust.PairMsg{}
	}
	c.rooms[room] = make(chan trust.PairMsg, 4)
	c.mu.Unlock()
	_, err := c.request(ctx, rframe{T: "room.open", Room: room})
	return err
}

// RoomSend sends to the machine in the room.
func (c *Client) RoomSend(ctx context.Context, room string, m trust.PairMsg) error {
	raw, _ := json.Marshal(m)
	return c.write(ctx, rframe{T: "room.send", Room: room, Data: raw})
}

// RoomReceive waits for the machine's message.
func (c *Client) RoomReceive(ctx context.Context, room string) (trust.PairMsg, error) {
	c.mu.Lock()
	ch := c.rooms[room]
	c.mu.Unlock()
	if ch == nil {
		return trust.PairMsg{}, errors.New("room not open")
	}
	select {
	case m := <-ch:
		return m, nil
	case <-ctx.Done():
		return trust.PairMsg{}, ctx.Err()
	}
}

// Room is an unauthenticated connection to a pairing room: a machine that
// is not in the roster yet meets the page there. Both sides authenticate
// each other with the pairing string's secret, not with the relay.
type Room struct {
	ws *websocket.Conn
}

// JoinRoom connects anonymously and joins a pairing room.
func JoinRoom(ctx context.Context, relayURL, room string) (*Room, error) {
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(dctx, WSURL(relayURL), nil)
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(1 << 20)
	r := &Room{ws: ws}
	if f, err := r.read(dctx); err != nil || f.T != "challenge" {
		ws.CloseNow()
		return nil, errors.New("中继没有按预期应答")
	}
	if err := writeJSON(dctx, ws, rframe{T: "anon"}); err != nil {
		ws.CloseNow()
		return nil, err
	}
	if f, err := r.read(dctx); err != nil || f.T != "welcome" {
		ws.CloseNow()
		return nil, errors.New("中继拒绝了连接")
	}
	if err := writeJSON(dctx, ws, rframe{T: "room.join", Room: room, RID: "1"}); err != nil {
		ws.CloseNow()
		return nil, err
	}
	f, err := r.read(dctx)
	if err != nil {
		ws.CloseNow()
		return nil, err
	}
	if f.T == "err" {
		ws.CloseNow()
		return nil, errors.New(f.Error)
	}
	return r, nil
}

func (r *Room) read(ctx context.Context) (rframe, error) {
	var f rframe
	_, raw, err := r.ws.Read(ctx)
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal(raw, &f)
}

// Send passes a message to the other side of the room.
func (r *Room) Send(ctx context.Context, room string, msg trust.PairMsg) error {
	raw, _ := json.Marshal(msg)
	return writeJSON(ctx, r.ws, rframe{T: "room.send", Room: room, Data: raw})
}

// Receive waits for the other side's message.
func (r *Room) Receive(ctx context.Context) (trust.PairMsg, error) {
	for {
		f, err := r.read(ctx)
		if err != nil {
			return trust.PairMsg{}, err
		}
		switch f.T {
		case "room":
			var m trust.PairMsg
			return m, json.Unmarshal(f.Data, &m)
		case "err":
			return trust.PairMsg{}, errors.New(f.Error)
		}
	}
}

// Close leaves the room.
func (r *Room) Close() { r.ws.CloseNow() }

// Claim makes the identity the relay's owner with the token `sous-relay
// init` printed (the page does this at first setup; tests use it too).
func Claim(ctx context.Context, relayURL, token string, roster trust.RosterUpdate, bundle json.RawMessage) error {
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(dctx, WSURL(relayURL), nil)
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	r := &Room{ws: ws}
	if _, err := r.read(dctx); err != nil {
		return err
	}
	if err := writeJSON(dctx, ws, rframe{T: "anon"}); err != nil {
		return err
	}
	if _, err := r.read(dctx); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"roster": roster, "bundle": bundle})
	if err := writeJSON(dctx, ws, rframe{T: "claim", RID: "1", Token: token, Body: body}); err != nil {
		return err
	}
	f, err := r.read(dctx)
	if err != nil {
		return err
	}
	if f.T == "err" {
		return errors.New(f.Error)
	}
	return nil
}
