// Package trust is the zero-trust core of sous 0.3 (docs/decisions/0030,
// work/specs/zero-trust): keys, signed documents, the roster, WebAuthn
// assertion checks, end-to-end encrypted channels and pairing strings.
//
// Formats are shared with the page (apps/web/src/trust) and pinned by the
// vectors in work/tests/fixtures/trust. Nothing here invents primitives:
// signatures are ECDSA P-256 (IEEE P1363 r||s), key encapsulation is HPKE
// with MLKEM768-X25519 (X-Wing), symmetric encryption is AES-256-GCM.
package trust

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hpke"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// B64 is the encoding of every binary field: base64url without padding.
var B64 = base64.RawURLEncoding

// ErrBadSignature is returned when a signature does not verify.
var ErrBadSignature = errors.New("trust: bad signature")

// SignKey is an ECDSA P-256 private key.
type SignKey struct{ k *ecdsa.PrivateKey }

// NewSignKey generates a signing key.
func NewSignKey() (*SignKey, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &SignKey{k}, nil
}

// SignKeyFromBytes parses a 32-byte private scalar.
func SignKeyFromBytes(b []byte) (*SignKey, error) {
	k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), b)
	if err != nil {
		return nil, err
	}
	return &SignKey{k}, nil
}

// Bytes is the 32-byte private scalar.
func (s *SignKey) Bytes() []byte {
	b, _ := s.k.Bytes()
	return b
}

// Public is the uncompressed SEC1 point (65 bytes), WebCrypto's "raw" format.
func (s *SignKey) Public() []byte {
	b, _ := s.k.PublicKey.Bytes()
	return b
}

// Sign signs msg under a context label and returns r||s (64 bytes).
func (s *SignKey) Sign(ctx string, msg []byte) []byte {
	h := sha256.Sum256(labeled(ctx, msg))
	r, ss, err := ecdsa.Sign(rand.Reader, s.k, h[:])
	if err != nil {
		panic(err) // only fails when the random source does
	}
	out := make([]byte, 64)
	r.FillBytes(out[:32])
	ss.FillBytes(out[32:])
	return out
}

// Verify checks a P1363 signature made by Sign.
func Verify(pub []byte, ctx string, msg, sig []byte) error {
	k, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), pub)
	if err != nil {
		return fmt.Errorf("trust: bad public key: %w", err)
	}
	if len(sig) != 64 {
		return ErrBadSignature
	}
	h := sha256.Sum256(labeled(ctx, msg))
	r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(k, h[:], r, s) {
		return ErrBadSignature
	}
	return nil
}

// labeled binds a message to its purpose: ctx, a zero byte, then the message.
func labeled(ctx string, msg []byte) []byte {
	out := make([]byte, 0, len(ctx)+1+len(msg))
	out = append(out, ctx...)
	out = append(out, 0)
	return append(out, msg...)
}

// KEM is the key encapsulation of every channel and wrapped key: X-Wing.
func KEM() hpke.KEM { return hpke.MLKEM768X25519() }

// KEMKey is an X-Wing private key.
type KEMKey struct{ k hpke.PrivateKey }

// NewKEMKey generates an X-Wing key.
func NewKEMKey() (*KEMKey, error) {
	k, err := KEM().GenerateKey()
	if err != nil {
		return nil, err
	}
	return &KEMKey{k}, nil
}

// KEMKeyFromBytes parses the 32-byte seed.
func KEMKeyFromBytes(b []byte) (*KEMKey, error) {
	k, err := KEM().NewPrivateKey(b)
	if err != nil {
		return nil, err
	}
	return &KEMKey{k}, nil
}

// Bytes is the 32-byte seed.
func (k *KEMKey) Bytes() []byte {
	b, err := k.k.Bytes()
	if err != nil {
		panic(err)
	}
	return b
}

// Public is the 1216-byte encapsulation key.
func (k *KEMKey) Public() []byte { return k.k.PublicKey().Bytes() }

func kemPublic(b []byte) (hpke.PublicKey, error) { return KEM().NewPublicKey(b) }

// KeyID is a short stable id of a public key.
func KeyID(pub []byte) string {
	h := sha256.Sum256(pub)
	return B64.EncodeToString(h[:12])
}

// Fingerprint is the full SHA-256 of a public key.
func Fingerprint(pub []byte) []byte {
	h := sha256.Sum256(pub)
	return h[:]
}

// NodeIDOf derives a machine's id from its signing key, so an id always
// names exactly one key.
func NodeIDOf(signPub []byte) string {
	h := sha256.Sum256(append([]byte("sous/v1/node-id\x00"), signPub...))
	return "n" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(h[:15]))
}

// RandomBytes returns n random bytes.
func RandomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}
