package trust

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

// Signed is a signed document. The signature covers the payload bytes
// exactly as sent, so no canonical JSON is needed: verify the bytes, then
// parse them.
type Signed struct {
	P string `json:"p"` // base64url of the JSON payload
	K string `json:"k"` // KeyID of the signer
	S string `json:"s"` // base64url P1363 signature
}

// Doc types; each doubles as the signature context "sous/v1/doc/<type>".
const (
	TRoster   = "roster"
	TSession  = "session"
	TScopes   = "scopes"
	TPause    = "pause"
	TLease    = "lease"
	TDomain   = "domain"
	TSnapshot = "snapshot"
	TPermit   = "permit"
	TIntent   = "intent"
	TChan     = "chan"
	TAudit    = "audit"
	TIdentity = "identity"
	TRelease  = "release"
)

func docCtx(t string) string { return "sous/v1/doc/" + t }

// Sign marshals payload (which must carry "t": typ) and signs it.
func Sign(k *SignKey, typ string, payload any) (Signed, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Signed{}, err
	}
	var head struct {
		T string `json:"t"`
	}
	if json.Unmarshal(raw, &head) != nil || head.T != typ {
		return Signed{}, fmt.Errorf("trust: payload type %q, want %q", head.T, typ)
	}
	return SignRaw(k, typ, raw), nil
}

// SignRaw signs payload bytes as a document of type typ.
func SignRaw(k *SignKey, typ string, raw []byte) Signed {
	return Signed{P: B64.EncodeToString(raw), K: KeyID(k.Public()), S: B64.EncodeToString(k.Sign(docCtx(typ), raw))}
}

// Payload returns the payload bytes without checking the signature.
func (s Signed) Payload() ([]byte, error) { return B64.DecodeString(s.P) }

// Hash is the SHA-256 of the payload bytes, base64url: what "prev" links.
func (s Signed) Hash() string {
	raw, _ := s.Payload()
	h := sha256.Sum256(raw)
	return B64.EncodeToString(h[:])
}

// Type reads the payload's "t" without checking the signature.
func (s Signed) Type() string {
	raw, err := s.Payload()
	if err != nil {
		return ""
	}
	var head struct {
		T string `json:"t"`
	}
	json.Unmarshal(raw, &head)
	return head.T
}

// Open checks that pub signed the document as type typ and decodes it.
func (s Signed) Open(pub []byte, typ string, out any) ([]byte, error) {
	raw, err := B64.DecodeString(s.P)
	if err != nil {
		return nil, errors.New("trust: bad payload encoding")
	}
	sig, err := B64.DecodeString(s.S)
	if err != nil {
		return nil, ErrBadSignature
	}
	if s.K != KeyID(pub) {
		return nil, fmt.Errorf("trust: signed by %s, want %s", s.K, KeyID(pub))
	}
	if err := Verify(pub, docCtx(typ), raw, sig); err != nil {
		return nil, err
	}
	var head struct {
		T string `json:"t"`
	}
	if err := json.Unmarshal(raw, &head); err != nil || head.T != typ {
		return nil, fmt.Errorf("trust: document type %q, want %q", head.T, typ)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

// Valid reports whether s is non-empty.
func (s Signed) Valid() bool { return s.P != "" && s.S != "" }
