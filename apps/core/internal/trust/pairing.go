package trust

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"strings"
	"time"
)

// A pairing string travels from the page to the machine by the user's own
// hands (clipboard, terminal), never through the relay: it carries the
// identity's fingerprint and a 128-bit secret that authenticates both sides
// of the exchange the relay then carries.

const pairPrefix = "sous1-"

var pairEnc = base32.StdEncoding.WithPadding(base32.NoPadding)

// PairCode is the content of a pairing string.
type PairCode struct {
	RelayHash  []byte // 8 bytes of SHA-256 of the relay URL
	IdentityFP []byte // 32 bytes: Fingerprint of the identity signing key
	Secret     []byte // 16 bytes
	Exp        time.Time
}

// PairTTL is how long a pairing string works.
const PairTTL = 10 * time.Minute

// RelayHash shortens a relay URL for the pairing string.
func RelayHash(relayURL string) []byte {
	h := sha256.Sum256([]byte(strings.TrimRight(relayURL, "/")))
	return h[:8]
}

// Encode renders the pairing string.
func (c PairCode) Encode() string {
	var b bytes.Buffer
	b.WriteByte(1)
	b.Write(c.RelayHash)
	b.Write(c.IdentityFP)
	b.Write(c.Secret)
	binary.Write(&b, binary.BigEndian, uint32(c.Exp.Unix()))
	return pairPrefix + strings.ToLower(pairEnc.EncodeToString(b.Bytes()))
}

// ParsePairCode reads a pairing string.
func ParsePairCode(s string) (PairCode, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, pairPrefix) {
		return PairCode{}, errors.New("不是 sous 的配对串（应以 sous1- 开头）")
	}
	raw, err := pairEnc.DecodeString(strings.ToUpper(strings.TrimPrefix(s, pairPrefix)))
	if err != nil || len(raw) != 1+8+32+16+4 || raw[0] != 1 {
		return PairCode{}, errors.New("配对串不完整，请重新复制")
	}
	return PairCode{RelayHash: raw[1:9], IdentityFP: raw[9:41], Secret: raw[41:57],
		Exp: time.Unix(int64(binary.BigEndian.Uint32(raw[57:61])), 0)}, nil
}

// PairRoom is the relay room both sides meet in; it does not reveal the secret.
func PairRoom(secret []byte) string {
	h := sha256.Sum256(labeled("sous/v1/pair-room", secret))
	return B64.EncodeToString(h[:16])
}

// PairMAC authenticates one side's message with the shared secret.
func PairMAC(secret []byte, side string, msg []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(labeled("sous/v1/pair-"+side, msg))
	return B64.EncodeToString(m.Sum(nil))
}

// CheckPairMAC verifies PairMAC in constant time.
func CheckPairMAC(secret []byte, side string, msg []byte, mac string) bool {
	want := PairMAC(secret, side, msg)
	return hmac.Equal([]byte(want), []byte(mac))
}

// PairHello is the machine's half: its keys and what it is.
type PairHello struct {
	T          string `json:"t"` // "pair.hello"
	Node       string `json:"node"`
	Name       string `json:"name"`
	OS         string `json:"os"`
	Arch       string `json:"arch,omitempty"`
	Hostname   string `json:"hostname,omitempty"`
	Role       string `json:"role,omitempty"`
	SignPub    string `json:"sign_pub"`
	KEMPub     string `json:"kem_pub"`
	KeyStorage string `json:"key_storage"`
}

// PairWelcome is the page's half: the roster that now includes the machine.
type PairWelcome struct {
	T      string       `json:"t"` // "pair.welcome"
	Roster RosterUpdate `json:"roster"`
}

// PairMsg wraps either half with its MAC.
type PairMsg struct {
	Body string `json:"body"` // base64url JSON of PairHello or PairWelcome
	MAC  string `json:"mac"`
}
