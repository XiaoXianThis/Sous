// Package relay is sous-relay (docs/decisions/0030): the server that used to
// be the Hub, now a blind post office. It forwards and stores ciphertext and
// signed documents and holds no key that matters. Its checks only keep an
// honest relay tidy; every receiver verifies everything again, so a relay
// that lies is caught there (work/specs/zero-trust, design §12).
package relay

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	_ "modernc.org/sqlite"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
	"github.com/XiaoXianThis/Sous/apps/core/internal/version"
)

// Limits.
const (
	MaxMessage   = 8 << 20
	MailboxTTL   = 7 * 24 * time.Hour
	RoomTTL      = trust.PairTTL
	challengeTTL = 30 * time.Second
	sendBuffer   = 256
)

// Server is the relay.
type Server struct {
	db      *sql.DB
	Origins []string // allowed page origins for browsers; empty allows any
	Now     func() time.Time

	mu       sync.Mutex
	conns    map[string][]*conn
	rooms    map[string]*room
	identity []byte
	roster   *trust.Roster
	usedAsrt map[string]time.Time

	// Tap sees every message the relay forwards. With Inject and InjectDoc
	// it lets tests play a relay that lies (design §12); the sous-relay
	// command never sets or calls them.
	Tap func(from, to string, data json.RawMessage)
}

// Inject delivers data to `to` as if `from` had sent it (tests only).
func (s *Server) Inject(to, from string, data json.RawMessage) {
	s.deliver(to, frame{T: "msg", From: from, Data: data})
}

// InjectDoc pushes a document to everyone without any check (tests only).
func (s *Server) InjectDoc(name string, body json.RawMessage) {
	s.broadcast(frame{T: "doc", Name: name, Body: body})
}

type room struct {
	opener, joiner *conn
	created        time.Time
}

// Open opens (or creates) the relay database in dir.
func Open(dir string) (*Server, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.Join(dir, "relay.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS docs (name TEXT PRIMARY KEY, body BLOB NOT NULL, updated INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS mailbox (id INTEGER PRIMARY KEY AUTOINCREMENT, to_addr TEXT NOT NULL, from_addr TEXT NOT NULL, data BLOB NOT NULL, created INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS mailbox_to ON mailbox(to_addr, id)`,
		`CREATE TABLE IF NOT EXISTS blobs (name TEXT PRIMARY KEY, data BLOB NOT NULL, updated INTEGER NOT NULL)`,
	} {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	s := &Server{db: db, Now: time.Now, conns: map[string][]*conn{}, rooms: map[string]*room{}, usedAsrt: map[string]time.Time{}}
	var id []byte
	if err := db.QueryRow(`SELECT v FROM meta WHERE k = 'identity'`).Scan(&id); err == nil {
		s.identity = id
		if body, err := s.doc("roster"); err == nil {
			var u trust.RosterUpdate
			if json.Unmarshal(body, &u) == nil {
				if r, err := trust.OpenRoster(id, u.Doc); err == nil {
					s.roster = &r
				}
			}
		}
	}
	return s, nil
}

// Close closes the database.
func (s *Server) Close() error { return s.db.Close() }

// Init creates a one-time claim token; the first page that presents it
// becomes the relay's owner. It fails once the relay is claimed.
func (s *Server) Init() (string, error) {
	if s.identity != nil {
		return "", errors.New("这个中继已经有主人了；要重来请删除数据目录")
	}
	tok := trust.B64.EncodeToString(trust.RandomBytes(24))
	h := sha256.Sum256([]byte(tok))
	_, err := s.db.Exec(`INSERT INTO meta(k, v) VALUES ('claim', ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, h[:])
	return tok, err
}

// Claimed reports whether an identity owns the relay.
func (s *Server) Claimed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.identity != nil
}

// Handler serves /v1/ws and /healthz.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "claimed": s.Claimed(), "version": version.Version})
	})
	mux.HandleFunc("GET /v1/ws", s.serveWS)
	return mux
}

func (s *Server) originPatterns() []string {
	if len(s.Origins) == 0 {
		return []string{"*"}
	}
	var out []string
	for _, o := range s.Origins {
		out = append(out, strings.TrimPrefix(strings.TrimPrefix(o, "https://"), "http://"))
	}
	return out
}

type conn struct {
	s    *Server
	ws   *websocket.Conn
	out  chan []byte
	addr string // empty until authenticated
	anon bool
	ctx  context.Context
	stop context.CancelFunc
}

// frame is every message in both directions; fields depend on T.
type frame struct {
	T     string          `json:"t"`
	RID   string          `json:"rid,omitempty"`
	Nonce string          `json:"nonce,omitempty"`
	Addr  string          `json:"addr,omitempty"`
	Pub   string          `json:"pub,omitempty"`
	Sig   string          `json:"sig,omitempty"`
	Cert  *trust.Signed   `json:"cert,omitempty"`
	Token string          `json:"token,omitempty"`
	To    string          `json:"to,omitempty"`
	From  string          `json:"from,omitempty"`
	ID    int64           `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Room  string          `json:"room,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	Body  json.RawMessage `json:"body,omitempty"`
	Blob  string          `json:"blob,omitempty"`
	Error string          `json:"error,omitempty"`
	Live  []string        `json:"online,omitempty"`
}

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.originPatterns()})
	if err != nil {
		return
	}
	ws.SetReadLimit(MaxMessage + 64<<10)
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{s: s, ws: ws, out: make(chan []byte, sendBuffer), ctx: ctx, stop: cancel}
	defer func() {
		cancel()
		ws.CloseNow()
		s.drop(c)
	}()
	go c.writer()

	nonce := trust.RandomBytes(32)
	c.send(frame{T: "challenge", Nonce: trust.B64.EncodeToString(nonce)})
	actx, done := context.WithTimeout(ctx, challengeTTL)
	f, err := c.read(actx)
	done()
	if err != nil {
		return
	}
	switch f.T {
	case "auth":
		if err := s.authenticate(c, f, nonce); err != nil {
			c.send(frame{T: "err", Error: err.Error()})
			ws.Close(websocket.StatusPolicyViolation, "auth")
			return
		}
		c.send(frame{T: "welcome", Addr: c.addr})
		s.flush(c)
		s.presence()
	case "anon":
		c.anon = true
		c.send(frame{T: "welcome"})
	default:
		return
	}
	for {
		f, err := c.read(ctx)
		if err != nil {
			return
		}
		if err := s.handle(c, f); err != nil {
			c.send(frame{T: "err", RID: f.RID, Error: err.Error()})
		}
	}
}

func (c *conn) read(ctx context.Context) (frame, error) {
	var f frame
	_, raw, err := c.ws.Read(ctx)
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal(raw, &f)
}

func (c *conn) send(f frame) {
	raw, _ := json.Marshal(f)
	select {
	case c.out <- raw:
	default:
		// A peer that does not read loses its connection, not the relay's memory.
		c.stop()
	}
}

// stopAfterFlush closes the connection once what was queued went out.
func (c *conn) stopAfterFlush() {
	deadline := time.Now().Add(3 * time.Second)
	for len(c.out) > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	c.stop()
}

func (c *conn) writer() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case raw := <-c.out:
			wctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
			err := c.ws.Write(wctx, websocket.MessageText, raw)
			cancel()
			if err != nil {
				c.stop()
				return
			}
		}
	}
}

func (s *Server) authenticate(c *conn, f frame, nonce []byte) error {
	s.mu.Lock()
	r := s.roster
	s.mu.Unlock()
	if r == nil {
		return errors.New("中继还没有主人")
	}
	pub, err := trust.B64.DecodeString(f.Pub)
	if err != nil {
		return err
	}
	sig, _ := trust.B64.DecodeString(f.Sig)
	if err := trust.Verify(pub, "sous/v1/relay-auth", nonce, sig); err != nil {
		return errors.New("认证失败")
	}
	switch {
	case strings.HasPrefix(f.Addr, "node:"):
		n, ok := r.Node(strings.TrimPrefix(f.Addr, "node:"))
		if !ok || n.SignPub != f.Pub {
			return errors.New("这台机器不在名册里")
		}
	case strings.HasPrefix(f.Addr, "sess:"):
		if f.Cert == nil {
			return errors.New("缺少会话证书")
		}
		cert, err := trust.OpenSessionCert(r, *f.Cert, s.Now())
		if err != nil {
			return err
		}
		if cert.SignPub != f.Pub || trust.SessionAddr(cert.SID) != f.Addr {
			return errors.New("会话证书和密钥不符")
		}
	default:
		return errors.New("未知的地址")
	}
	c.addr = f.Addr
	s.mu.Lock()
	s.conns[c.addr] = append(s.conns[c.addr], c)
	s.mu.Unlock()
	return nil
}

func (s *Server) drop(c *conn) {
	if c.addr == "" && !c.anon {
		return
	}
	s.mu.Lock()
	if c.addr != "" {
		s.conns[c.addr] = slices.DeleteFunc(s.conns[c.addr], func(x *conn) bool { return x == c })
		if len(s.conns[c.addr]) == 0 {
			delete(s.conns, c.addr)
		}
	}
	for id, rm := range s.rooms {
		if rm.opener == c || rm.joiner == c {
			delete(s.rooms, id)
		}
	}
	s.mu.Unlock()
	if c.addr != "" {
		s.presence()
	}
}

func (s *Server) online() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.conns))
	for a := range s.conns {
		out = append(out, a)
	}
	slices.Sort(out)
	return out
}

func (s *Server) presence() {
	live := s.online()
	s.broadcast(frame{T: "presence", Live: live})
}

func (s *Server) broadcast(f frame) {
	s.mu.Lock()
	var all []*conn
	for _, cs := range s.conns {
		all = append(all, cs...)
	}
	s.mu.Unlock()
	for _, c := range all {
		c.send(f)
	}
}

func (s *Server) handle(c *conn, f frame) error {
	if c.anon {
		switch f.T {
		case "claim":
			return s.claim(c, f)
		case "get":
			if f.Name != "identity" {
				return errors.New("未认证的连接只能读取身份包")
			}
		case "room.join", "room.send":
		default:
			return errors.New("请先认证")
		}
	}
	switch f.T {
	case "ping":
		c.send(frame{T: "pong", RID: f.RID})
	case "send":
		return s.sendMsg(c, f)
	case "ack":
		_, err := s.db.Exec(`DELETE FROM mailbox WHERE id = ? AND to_addr = ?`, f.ID, c.addr)
		return err
	case "get":
		body, err := s.doc(f.Name)
		if errors.Is(err, sql.ErrNoRows) {
			c.send(frame{T: "doc", RID: f.RID, Name: f.Name})
			return nil
		}
		if err != nil {
			return err
		}
		c.send(frame{T: "doc", RID: f.RID, Name: f.Name, Body: body})
	case "put":
		if err := s.put(c, f.Name, f.Body); err != nil {
			return err
		}
		c.send(frame{T: "ok", RID: f.RID})
	case "blob.put":
		return s.blobPut(c, f)
	case "blob.get":
		var data []byte
		err := s.db.QueryRow(`SELECT data FROM blobs WHERE name = ?`, f.Name).Scan(&data)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		c.send(frame{T: "blob", RID: f.RID, Name: f.Name, Blob: trust.B64.EncodeToString(data)})
	case "room.open", "room.join", "room.send":
		return s.roomOp(c, f)
	case "presence":
		c.send(frame{T: "presence", RID: f.RID, Live: s.online()})
	default:
		return fmt.Errorf("未知的请求 %q", f.T)
	}
	return nil
}

func (s *Server) claim(c *conn, f frame) error {
	var want []byte
	if err := s.db.QueryRow(`SELECT v FROM meta WHERE k = 'claim'`).Scan(&want); err != nil {
		return errors.New("中继没有待用的认领码：在服务器上运行 sous-relay init")
	}
	got := sha256.Sum256([]byte(f.Token))
	if subtle.ConstantTimeCompare(got[:], want) != 1 {
		return errors.New("认领码不对")
	}
	var body struct {
		Roster trust.RosterUpdate `json:"roster"`
		Bundle json.RawMessage    `json:"bundle"`
	}
	if err := json.Unmarshal(f.Body, &body); err != nil {
		return err
	}
	head, err := body.Roster.Doc.Payload()
	if err != nil {
		return err
	}
	var peek trust.Roster
	if err := json.Unmarshal(head, &peek); err != nil {
		return err
	}
	id, err := trust.B64.DecodeString(peek.User.SignPub)
	if err != nil {
		return err
	}
	roster, err := trust.AcceptRoster(id, nil, body.Roster, s.Now(), nil)
	if err != nil {
		return err
	}
	rb, _ := json.Marshal(body.Roster)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.Now().Unix()
	for _, q := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO meta(k, v) VALUES ('identity', ?)`, []any{id}},
		{`DELETE FROM meta WHERE k = 'claim'`, nil},
		{`INSERT INTO docs(name, body, updated) VALUES ('roster', ?, ?)`, []any{rb, now}},
		{`INSERT INTO docs(name, body, updated) VALUES (?, ?, ?)`, []any{fmt.Sprintf("roster@%d", roster.V), rb, now}},
		{`INSERT INTO docs(name, body, updated) VALUES ('identity', ?, ?)`, []any{[]byte(body.Bundle), now}},
	} {
		if _, err := tx.Exec(q.q, q.args...); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.mu.Lock()
	s.identity, s.roster = id, &roster
	s.mu.Unlock()
	slog.Info("relay: claimed", "identity", trust.KeyID(id))
	c.send(frame{T: "ok", RID: f.RID})
	return nil
}

func (s *Server) doc(name string) ([]byte, error) {
	var body []byte
	err := s.db.QueryRow(`SELECT body FROM docs WHERE name = ?`, name).Scan(&body)
	return body, err
}

func (s *Server) sendMsg(c *conn, f frame) error {
	if len(f.Data) > MaxMessage {
		return errors.New("消息太大")
	}
	if !strings.HasPrefix(f.To, "node:") && !strings.HasPrefix(f.To, "sess:") {
		return errors.New("未知的收件人")
	}
	if s.Tap != nil {
		s.Tap(c.addr, f.To, f.Data)
	}
	if strings.HasPrefix(f.To, "node:") {
		// Machines get a mailbox; sessions only get live delivery.
		res, err := s.db.Exec(`INSERT INTO mailbox(to_addr, from_addr, data, created) VALUES (?,?,?,?)`, f.To, c.addr, []byte(f.Data), s.Now().Unix())
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		s.deliver(f.To, frame{T: "msg", ID: id, From: c.addr, Data: f.Data})
		return nil
	}
	s.deliver(f.To, frame{T: "msg", From: c.addr, Data: f.Data})
	return nil
}

func (s *Server) deliver(to string, f frame) {
	s.mu.Lock()
	cs := slices.Clone(s.conns[to])
	s.mu.Unlock()
	for _, c := range cs {
		c.send(f)
	}
}

// flush sends a reconnecting machine what waited in its mailbox, in order.
func (s *Server) flush(c *conn) {
	if !strings.HasPrefix(c.addr, "node:") {
		return
	}
	s.db.Exec(`DELETE FROM mailbox WHERE created < ?`, s.Now().Add(-MailboxTTL).Unix())
	rows, err := s.db.Query(`SELECT id, from_addr, data FROM mailbox WHERE to_addr = ? ORDER BY id`, c.addr)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var from string
		var data []byte
		if rows.Scan(&id, &from, &data) == nil {
			c.send(frame{T: "msg", ID: id, From: from, Data: data})
		}
	}
}

func (s *Server) blobPut(c *conn, f frame) error {
	if !strings.HasPrefix(c.addr, "node:") && !strings.HasPrefix(c.addr, "sess:") {
		return errors.New("请先认证")
	}
	data, err := trust.B64.DecodeString(f.Blob)
	if err != nil {
		return err
	}
	if len(data) > MaxMessage {
		return errors.New("块太大")
	}
	if strings.HasPrefix(f.Name, "snapshot/") && !s.isHolder(c.addr) {
		return errors.New("只有持有租约的大脑能写快照")
	}
	if _, err := s.db.Exec(`INSERT INTO blobs(name, data, updated) VALUES (?,?,?) ON CONFLICT(name) DO UPDATE SET data = excluded.data, updated = excluded.updated`,
		f.Name, data, s.Now().Unix()); err != nil {
		return err
	}
	c.send(frame{T: "ok", RID: f.RID})
	return nil
}

func (s *Server) isHolder(addr string) bool {
	body, err := s.doc("lease")
	if err != nil {
		return false
	}
	var l trust.Lease
	var sd trust.Signed
	if json.Unmarshal(body, &sd) != nil {
		return false
	}
	raw, _ := sd.Payload()
	json.Unmarshal(raw, &l)
	return trust.NodeAddr(l.Holder) == addr && l.Live(s.Now())
}

func (s *Server) roomOp(c *conn, f frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, rm := range s.rooms {
		if s.Now().Sub(rm.created) > RoomTTL {
			delete(s.rooms, id)
		}
	}
	rm := s.rooms[f.Room]
	switch f.T {
	case "room.open":
		if c.anon {
			return errors.New("只有已解锁的页面能开配对房间")
		}
		if len(s.rooms) > 64 {
			return errors.New("配对房间太多")
		}
		s.rooms[f.Room] = &room{opener: c, created: s.Now()}
		c.send(frame{T: "ok", RID: f.RID})
	case "room.join":
		if rm == nil || rm.joiner != nil {
			return errors.New("配对串无效或已经用过")
		}
		rm.joiner = c
		c.send(frame{T: "ok", RID: f.RID})
	case "room.send":
		if rm == nil {
			return errors.New("配对房间已关闭")
		}
		var peer *conn
		switch c {
		case rm.opener:
			peer = rm.joiner
		case rm.joiner:
			peer = rm.opener
		}
		if peer == nil {
			return errors.New("对方还没进入配对房间")
		}
		peer.send(frame{T: "room", Room: f.Room, Data: f.Data})
	}
	return nil
}
