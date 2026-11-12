package trust

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hpke"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// A channel carries messages one way, from one address to another. The
// sender encapsulates a fresh key to the recipient's X-Wing key and signs
// the opening; every message is then AES-256-GCM with a counter nonce, so
// the relay can neither read, forge, replay nor reorder unnoticed.

// ChanOpen is the signed opening of a channel.
type ChanOpen struct {
	T     string `json:"t"`
	Ch    string `json:"ch"`
	From  string `json:"from"`
	To    string `json:"to"`
	ToKey string `json:"to_key"` // KeyID of the recipient's KEM key
	Enc   string `json:"enc"`
	IAT   int64  `json:"iat"`
}

// Wire is one message as the relay carries it.
type Wire struct {
	Open *Signed `json:"open,omitempty"` // first message of a channel
	Cert *Signed `json:"cert,omitempty"` // a session's certificate, with Open
	Ch   string  `json:"ch"`
	Seq  uint64  `json:"seq"`
	CT   string  `json:"ct"`
}

// ChanOpenTTL bounds how old an opening may be, so the relay cannot replay
// an old channel long after it ended.
const ChanOpenTTL = 10 * time.Minute

// A sender opens a fresh channel (a fresh key) every ChanRekeyAfter or
// ChanRekeyMsgs messages, whichever comes first, so one leaked channel key
// exposes at most that much. Variables only so tests can shorten them.
var (
	ChanRekeyAfter        = time.Hour
	ChanRekeyMsgs  uint64 = 100_000
)

// Addresses on the relay.
func NodeAddr(id string) string     { return "node:" + id }
func SessionAddr(sid string) string { return "sess:" + sid }

func chanInfo(ch, from, to string) []byte {
	return []byte("sous/v1/chan\x00" + ch + "\x00" + from + "\x00" + to)
}

// OutChannel seals messages to one recipient.
type OutChannel struct {
	ID     string
	open   Signed
	aead   cipher.AEAD
	seq    uint64
	opened time.Time
}

// Stale reports whether the sender should open a new channel instead.
func (c *OutChannel) Stale(now time.Time) bool {
	return now.Sub(c.opened) >= ChanRekeyAfter || c.seq >= ChanRekeyMsgs
}

// NewOutChannel opens a channel from one address to another.
func NewOutChannel(from, to string, signer *SignKey, recipientKEM []byte, now time.Time) (*OutChannel, error) {
	pub, err := kemPublic(recipientKEM)
	if err != nil {
		return nil, fmt.Errorf("trust: recipient key: %w", err)
	}
	ch := B64.EncodeToString(RandomBytes(16))
	enc, s, err := hpke.NewSender(pub, hpke.HKDFSHA256(), hpke.AES256GCM(), chanInfo(ch, from, to))
	if err != nil {
		return nil, err
	}
	key, err := s.Export("sous/v1/chan-key", 32)
	if err != nil {
		return nil, err
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	open, err := Sign(signer, TChan, ChanOpen{T: TChan, Ch: ch, From: from, To: to, ToKey: KeyID(recipientKEM),
		Enc: B64.EncodeToString(enc), IAT: now.Unix()})
	if err != nil {
		return nil, err
	}
	return &OutChannel{ID: ch, open: open, aead: aead, opened: now}, nil
}

// Seal encrypts the next message. The first one carries the opening.
func (c *OutChannel) Seal(plaintext []byte) Wire {
	c.seq++
	w := Wire{Ch: c.ID, Seq: c.seq, CT: B64.EncodeToString(c.aead.Seal(nil, nonce(c.seq), plaintext, msgAAD(c.ID, c.seq)))}
	if c.seq == 1 {
		o := c.open
		w.Open = &o
	}
	return w
}

// Opening returns the signed opening, to resend with any message.
func (c *OutChannel) Opening() Signed { return c.open }

// InChannel opens messages from one sender.
type InChannel struct {
	ID   string
	From string
	aead cipher.AEAD
	last uint64
}

// ErrGap means messages were lost or reordered before this one.
var ErrGap = errors.New("trust: messages missing before this one")

// PeekOpen reads an opening without checking it, to find out who sent it.
func PeekOpen(s Signed) (ChanOpen, error) {
	var o ChanOpen
	raw, err := s.Payload()
	if err != nil {
		return o, err
	}
	return o, jsonUnmarshal(raw, &o)
}

// AcceptChannel checks an opening signed by senderPub and addressed to me.
func AcceptChannel(open Signed, senderPub []byte, me string, mine *KEMKey, now time.Time) (*InChannel, error) {
	var o ChanOpen
	if _, err := open.Open(senderPub, TChan, &o); err != nil {
		return nil, err
	}
	if o.To != me || o.ToKey != KeyID(mine.Public()) {
		return nil, errors.New("trust: channel is for someone else")
	}
	if err := CheckFresh(o.IAT, ChanOpenTTL, now); err != nil {
		return nil, fmt.Errorf("channel opening: %w", err)
	}
	enc, err := B64.DecodeString(o.Enc)
	if err != nil {
		return nil, err
	}
	r, err := hpke.NewRecipient(enc, mine.k, hpke.HKDFSHA256(), hpke.AES256GCM(), chanInfo(o.Ch, o.From, o.To))
	if err != nil {
		return nil, fmt.Errorf("trust: cannot open channel: %w", err)
	}
	key, err := r.Export("sous/v1/chan-key", 32)
	if err != nil {
		return nil, err
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	return &InChannel{ID: o.Ch, From: o.From, aead: aead}, nil
}

// Open decrypts the next message. A replayed or older message fails; a
// message after a gap decrypts but returns ErrGap too.
func (c *InChannel) Open(w Wire) ([]byte, error) {
	if w.Ch != c.ID {
		return nil, errors.New("trust: wrong channel")
	}
	if w.Seq <= c.last {
		return nil, errors.New("trust: replayed message")
	}
	ct, err := B64.DecodeString(w.CT)
	if err != nil {
		return nil, err
	}
	pt, err := c.aead.Open(nil, nonce(w.Seq), ct, msgAAD(c.ID, w.Seq))
	if err != nil {
		return nil, errors.New("trust: message does not decrypt")
	}
	gap := w.Seq != c.last+1
	c.last = w.Seq
	if gap {
		return pt, ErrGap
	}
	return pt, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func nonce(seq uint64) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[4:], seq)
	return n
}

func msgAAD(ch string, seq uint64) []byte {
	b := []byte("sous/v1/msg\x00" + ch + "\x00")
	return binary.BigEndian.AppendUint64(b, seq)
}

// SealTo encrypts a one-off message to a KEM key (wrapped keys, pairing):
// info binds it to its purpose.
func SealTo(recipientKEM []byte, info string, plaintext []byte) ([]byte, error) {
	pub, err := kemPublic(recipientKEM)
	if err != nil {
		return nil, err
	}
	return hpke.Seal(pub, hpke.HKDFSHA256(), hpke.AES256GCM(), []byte(info), plaintext)
}

// OpenFrom decrypts what SealTo made.
func (k *KEMKey) OpenFrom(info string, ct []byte) ([]byte, error) {
	return hpke.Open(k.k, hpke.HKDFSHA256(), hpke.AES256GCM(), []byte(info), ct)
}

// SymSeal encrypts with a 32-byte key and a random nonce (snapshots).
func SymSeal(key, aad, plaintext []byte) ([]byte, error) {
	a, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	n := RandomBytes(12)
	return append(n, a.Seal(nil, n, plaintext, aad)...), nil
}

// SymOpen decrypts what SymSeal made.
func SymOpen(key, aad, ct []byte) ([]byte, error) {
	a, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(ct) < 12 {
		return nil, errors.New("trust: short ciphertext")
	}
	return a.Open(nil, ct[:12], ct[12:], aad)
}
