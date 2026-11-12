// Package trusttest has a software WebAuthn authenticator for tests. It is
// never linked into a binary: only _test files import it.
package trusttest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
)

// Authenticator signs assertions like a platform passkey would.
type Authenticator struct {
	Key    *ecdsa.PrivateKey
	CredID string
	RPID   string
	Origin string
	count  uint32
}

// NewAuthenticator makes a passkey for rpID used from origin.
func NewAuthenticator(rpID, origin string) *Authenticator {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	return &Authenticator{Key: k, CredID: trust.B64.EncodeToString(trust.RandomBytes(16)), RPID: rpID, Origin: origin}
}

// Passkey is the roster entry of this authenticator.
func (a *Authenticator) Passkey() trust.Passkey {
	pub, _ := a.Key.PublicKey.Bytes()
	return trust.Passkey{CredID: a.CredID, Pub: trust.B64.EncodeToString(pub), Label: "test"}
}

// Assert signs challenge; uv false leaves the user-verified flag off.
func (a *Authenticator) Assert(challenge []byte, uv bool) trust.Assertion {
	return a.AssertFrom(a.Origin, a.RPID, challenge, uv)
}

// AssertFrom lets a test lie about the origin or rp id.
func (a *Authenticator) AssertFrom(origin, rpID string, challenge []byte, uv bool) trust.Assertion {
	a.count++
	rp := sha256.Sum256([]byte(rpID))
	ad := append([]byte{}, rp[:]...)
	flags := byte(0x01)
	if uv {
		flags |= 0x04
	}
	ad = append(ad, flags)
	ad = binary.BigEndian.AppendUint32(ad, a.count)
	cd, _ := json.Marshal(map[string]any{"type": "webauthn.get", "challenge": trust.B64.EncodeToString(challenge), "origin": origin, "crossOrigin": false})
	cdh := sha256.Sum256(cd)
	h := sha256.Sum256(append(append([]byte{}, ad...), cdh[:]...))
	sig, _ := ecdsa.SignASN1(rand.Reader, a.Key, h[:])
	return trust.Assertion{CredID: a.CredID, AuthenticatorData: trust.B64.EncodeToString(ad),
		ClientDataJSON: trust.B64.EncodeToString(cd), Signature: trust.B64.EncodeToString(sig)}
}
