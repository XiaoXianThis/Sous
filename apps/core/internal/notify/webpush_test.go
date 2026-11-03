package notify

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// browser plays the user agent side of RFC 8291.
type browser struct {
	key  *ecdh.PrivateKey
	auth []byte
}

func newBrowser(t *testing.T) *browser {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	rand.Read(auth)
	return &browser{key: k, auth: auth}
}

func (b *browser) sub(endpoint string) Subscription {
	return Subscription{Endpoint: endpoint, P256DH: b64.EncodeToString(b.key.PublicKey().Bytes()), Auth: b64.EncodeToString(b.auth)}
}

func (b *browser) decrypt(t *testing.T, body []byte) []byte {
	t.Helper()
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	if rs != recordSize || idlen != 65 {
		t.Fatalf("header: rs %d idlen %d", rs, idlen)
	}
	asPub, ct := body[21:21+idlen], body[21+idlen:]
	pub, err := ecdh.P256().NewPublicKey(asPub)
	if err != nil {
		t.Fatal(err)
	}
	shared, _ := b.key.ECDH(pub)
	prkKey, _ := hkdf.Extract(sha256.New, shared, b.auth)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, "WebPush: info\x00"+string(b.key.PublicKey().Bytes())+string(asPub), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if plain[len(plain)-1] != 2 {
		t.Fatal("missing the last-record delimiter")
	}
	return plain[:len(plain)-1]
}

func TestSendEncryptsAndSigns(t *testing.T) {
	v, err := LoadVAPID(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := newBrowser(t)
	var got []byte
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		hdr = r.Header
		w.WriteHeader(201)
	}))
	defer srv.Close()

	s := &Sender{VAPID: v, Subject: "https://agent.example"}
	msg := Message{Title: "需要你审批", Body: "push 分支", URL: "/#approvals", Tag: "approval-1"}
	if err := s.Send(context.Background(), b.sub(srv.URL+"/push/abc"), msg, true); err != nil {
		t.Fatal(err)
	}
	var back Message
	if err := json.Unmarshal(b.decrypt(t, got), &back); err != nil || back != msg {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	if hdr.Get("Content-Encoding") != "aes128gcm" || hdr.Get("Urgency") != "high" || hdr.Get("TTL") == "" || len(hdr.Get("Topic")) != 32 {
		t.Fatalf("headers: %v", hdr)
	}

	// The VAPID JWT verifies with the public key and names the push service.
	auth := hdr.Get("Authorization")
	tok := strings.TrimPrefix(strings.Split(auth, ", k=")[0], "vapid t=")
	if !strings.HasSuffix(auth, "k="+v.Public) {
		t.Fatalf("authorization: %s", auth)
	}
	parts := strings.Split(tok, ".")
	claims, _ := b64.DecodeString(parts[1])
	var c struct {
		Aud string
		Exp int64
		Sub string
	}
	json.Unmarshal(claims, &c)
	if c.Aud != srv.URL || c.Sub != "https://agent.example" || c.Exp < time.Now().Unix() {
		t.Fatalf("claims: %+v", c)
	}
	sig, _ := b64.DecodeString(parts[2])
	pub, _ := b64.DecodeString(v.Public)
	x, y := elliptic.Unmarshal(elliptic.P256(), pub)
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("JWT signature does not verify")
	}
}

func TestVAPIDPersistsAndGoneSubscriptions(t *testing.T) {
	dir := t.TempDir()
	a, _ := LoadVAPID(dir)
	b, err := LoadVAPID(dir)
	if err != nil || a.Public != b.Public {
		t.Fatalf("key changed across loads: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(410) }))
	defer srv.Close()
	err = (&Sender{VAPID: a, Subject: "mailto:x@y"}).Send(context.Background(), newBrowser(t).sub(srv.URL), Message{Title: "x"}, false)
	if err != ErrGone {
		t.Fatalf("410 must mean gone, got %v", err)
	}
}

func TestQuietHours(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 3, h, m, 0, 0, time.UTC) }
	q := Quiet{Enabled: true, Start: "22:00", End: "08:00", TZ: "UTC"}
	for _, c := range []struct {
		t    time.Time
		want bool
	}{{at(23, 0), true}, {at(3, 0), true}, {at(8, 0), false}, {at(12, 0), false}, {at(22, 0), true}} {
		if got := q.In(c.t); got != c.want {
			t.Errorf("%v: got %v", c.t, got)
		}
	}
	day := Quiet{Enabled: true, Start: "12:00", End: "13:30"}
	if !day.In(at(12, 45)) || day.In(at(13, 30)) {
		t.Error("same-day window")
	}
	shanghai := Quiet{Enabled: true, Start: "22:00", End: "08:00", TZ: "Asia/Shanghai"}
	if !shanghai.In(at(15, 0)) { // 23:00 in Shanghai
		t.Error("time zone ignored")
	}
	if (Quiet{Start: "22:00", End: "08:00"}).In(at(23, 0)) {
		t.Error("disabled quiet hours")
	}
}
