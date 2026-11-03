// Package notify sends Web Push notifications to the user's devices
// (requirements R6): VAPID keys made on first start, RFC 8291 message
// encryption, per-kind switches and quiet hours.
package notify

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

var b64 = base64.RawURLEncoding

// VAPID is the Hub's application server key pair (RFC 8292).
type VAPID struct {
	key *ecdsa.PrivateKey
	// Public is the uncompressed public key, base64url: the browser's
	// applicationServerKey.
	Public string
}

type vapidFile struct {
	Private string `json:"private"` // the scalar D, base64url
	Public  string `json:"public"`
}

// LoadVAPID reads the key pair from dataDir/vapid.json, making one on first use.
func LoadVAPID(dataDir string) (*VAPID, error) {
	path := filepath.Join(dataDir, "vapid.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		v := vapidFromKey(key)
		d := make([]byte, 32)
		key.D.FillBytes(d)
		out, _ := json.MarshalIndent(vapidFile{Private: b64.EncodeToString(d), Public: v.Public}, "", "  ")
		if err := os.MkdirAll(dataDir, 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, out, 0o600); err != nil {
			return nil, err
		}
		return v, nil
	} else if err != nil {
		return nil, err
	}
	var f vapidFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	d, err := b64.DecodeString(f.Private)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	key := &ecdsa.PrivateKey{D: new(big.Int).SetBytes(d)}
	key.Curve = elliptic.P256()
	key.X, key.Y = key.Curve.ScalarBaseMult(d)
	return vapidFromKey(key), nil
}

func vapidFromKey(key *ecdsa.PrivateKey) *VAPID {
	pub := make([]byte, 65)
	pub[0] = 4
	key.X.FillBytes(pub[1:33])
	key.Y.FillBytes(pub[33:])
	return &VAPID{key: key, Public: b64.EncodeToString(pub)}
}

// authorization builds the VAPID Authorization header for an endpoint.
func (v *VAPID) authorization(endpoint, subject string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("bad push endpoint %q", endpoint)
	}
	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{
		"aud": u.Scheme + "://" + u.Host,
		"exp": now.Add(12 * time.Hour).Unix(),
		"sub": subject,
	})
	signing := header + "." + b64.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, v.key, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return "vapid t=" + signing + "." + b64.EncodeToString(sig) + ", k=" + v.Public, nil
}

// Subscription is a browser's push subscription.
type Subscription struct {
	Endpoint string
	P256DH   string // base64url
	Auth     string // base64url
}

// recordSize is the RFC 8188 record size we declare; one record holds a
// whole notification.
const recordSize = 4096

// encrypt encrypts a message for a subscription (RFC 8291, aes128gcm).
func encrypt(sub Subscription, plaintext []byte) ([]byte, error) {
	uaPublic, err := decode64(sub.P256DH)
	if err != nil {
		return nil, fmt.Errorf("p256dh: %w", err)
	}
	authSecret, err := decode64(sub.Auth)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	uaKey, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("p256dh: %w", err)
	}
	asKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := asKey.ECDH(uaKey)
	if err != nil {
		return nil, err
	}
	asPublic := asKey.PublicKey().Bytes()

	prkKey, err := hkdf.Extract(sha256.New, shared, authSecret)
	if err != nil {
		return nil, err
	}
	keyInfo := "WebPush: info\x00" + string(uaPublic) + string(asPublic)
	ikm, err := hkdf.Expand(sha256.New, prkKey, keyInfo, 32)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	if len(plaintext)+1+16 > recordSize-86 {
		return nil, errors.New("notification is too large")
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, nonce, append(append([]byte{}, plaintext...), 2), nil) // 0x02: the last record

	var out bytes.Buffer
	out.Write(salt)
	binary.Write(&out, binary.BigEndian, uint32(recordSize))
	out.WriteByte(byte(len(asPublic)))
	out.Write(asPublic)
	out.Write(sealed)
	return out.Bytes(), nil
}

// decode64 accepts base64url with or without padding, and standard base64.
func decode64(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("not base64")
}

// ErrGone means the subscription expired or was revoked: delete it.
var ErrGone = errors.New("push subscription is gone")

// Message is what the service worker shows.
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url,omitempty"`
	Tag   string `json:"tag,omitempty"`
	Kind  string `json:"kind,omitempty"`
}

// Sender delivers messages to push services.
type Sender struct {
	VAPID   *VAPID
	Subject string // a mailto: or https: contact for the push service
	Client  *http.Client
}

// Send delivers one message to one subscription.
func (s *Sender) Send(ctx context.Context, sub Subscription, m Message, urgent bool) error {
	payload, _ := json.Marshal(m)
	body, err := encrypt(sub, payload)
	if err != nil {
		return err
	}
	auth, err := s.VAPID.authorization(sub.Endpoint, s.Subject, time.Now())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.Itoa(24*3600))
	if urgent {
		req.Header.Set("Urgency", "high")
	} else {
		req.Header.Set("Urgency", "normal")
	}
	if m.Tag != "" {
		req.Header.Set("Topic", topic(m.Tag))
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	case resp.StatusCode >= 300:
		return fmt.Errorf("push service answered %s", resp.Status)
	}
	return nil
}

// topic turns a tag into a Topic header value (at most 32 base64url chars),
// so a newer message about the same thing replaces an undelivered one.
func topic(tag string) string {
	sum := sha256.Sum256([]byte(tag))
	return b64.EncodeToString(sum[:])[:32]
}
