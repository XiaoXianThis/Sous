package trust

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Assertion is a WebAuthn assertion (navigator.credentials.get), the proof
// that the user verified with Face ID, a fingerprint or a PIN just now.
type Assertion struct {
	CredID            string `json:"cred_id"`
	AuthenticatorData string `json:"authenticator_data"`
	ClientDataJSON    string `json:"client_data_json"`
	Signature         string `json:"signature"` // ASN.1 DER, as WebAuthn returns it
}

// AssertionTTL is how old the signed operation may be.
const AssertionTTL = CommandTTL

// CriticalChallenge is the WebAuthn challenge for a critical operation: it
// is the operation itself, so a page cannot show one thing and sign another.
func CriticalChallenge(payload []byte) []byte {
	h := sha256.Sum256(labeled("sous/v1/critical", payload))
	return h[:]
}

const (
	flagUP = 0x01
	flagUV = 0x04
)

// VerifyAssertion checks an assertion against the roster's passkeys, rp_id
// and origins, and that it signs challenge with user verification. used,
// when set, records the challenge and reports whether it was seen before.
func VerifyAssertion(r *Roster, a Assertion, challenge []byte, now time.Time, used func(string) bool) error {
	pk, ok := r.Passkey(a.CredID)
	if !ok {
		return errors.New("不认识这个 Passkey")
	}
	authData, err1 := B64.DecodeString(a.AuthenticatorData)
	cdj, err2 := B64.DecodeString(a.ClientDataJSON)
	sig, err3 := B64.DecodeString(a.Signature)
	pub, err4 := B64.DecodeString(pk.Pub)
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		return fmt.Errorf("断言编码不对：%w", err)
	}
	var cd struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		Origin    string `json:"origin"`
	}
	if err := json.Unmarshal(cdj, &cd); err != nil {
		return errors.New("断言的客户端数据不对")
	}
	if cd.Type != "webauthn.get" {
		return errors.New("不是 WebAuthn 断言")
	}
	got, err := B64.DecodeString(cd.Challenge)
	if err != nil || !bytes.Equal(got, challenge) {
		return errors.New("Face ID 签的是另一个操作")
	}
	if !slices.Contains(r.Origins, cd.Origin) {
		return fmt.Errorf("断言来自其他网站 %q", cd.Origin)
	}
	if len(authData) < 37 {
		return errors.New("断言数据不完整")
	}
	rpHash := sha256.Sum256([]byte(r.RPID))
	if !bytes.Equal(authData[:32], rpHash[:]) {
		return errors.New("断言属于其他网站")
	}
	if authData[32]&flagUP == 0 || authData[32]&flagUV == 0 {
		return errors.New("没有验证本人（需要 Face ID、指纹或 PIN，只碰一下不算）")
	}
	k, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), pub)
	if err != nil {
		return err
	}
	cdHash := sha256.Sum256(cdj)
	h := sha256.Sum256(append(slices.Clone(authData), cdHash[:]...))
	if !ecdsa.VerifyASN1(k, h[:], sig) {
		return ErrBadSignature
	}
	if used != nil && used(B64.EncodeToString(challenge)) {
		return errors.New("这次 Face ID 已经用过了")
	}
	_ = now
	return nil
}

// Permit lets the brain do one thing on one machine that it may not do on
// its own. A session sends it straight to the machine; Critical permits
// carry an assertion over the permit bytes.
type Permit struct {
	T        string            `json:"t"`
	ID       string            `json:"id"`
	SID      string            `json:"sid"` // the session that granted it
	Node     string            `json:"node"`
	Action   string            `json:"action"` // admin / update / run / unfreeze
	Match    map[string]string `json:"match,omitempty"`
	Critical bool              `json:"critical,omitempty"`
	IAT      int64             `json:"iat"`
	Exp      int64             `json:"exp"`
}

// Permit actions.
const (
	PermitAdmin    = "admin"    // match: op, agent, file, name, root, version
	PermitUpdate   = "update"   // match: version
	PermitRun      = "run"      // match: task_id
	PermitUnfreeze = "unfreeze" // a machine that froze itself
)

// Intent is a critical request to the brain itself (models, MCP servers):
// the assertion signs these bytes.
type Intent struct {
	T          string `json:"t"`
	SID        string `json:"sid"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	BodySHA256 string `json:"body_sha256"`
	Nonce      string `json:"nonce"`
	IAT        int64  `json:"iat"`
}

// CheckFresh rejects timestamps outside [now-ttl-skew, now+skew].
func CheckFresh(iat int64, ttl time.Duration, now time.Time) error {
	t := time.Unix(iat, 0)
	if t.After(now.Add(ClockSkew)) {
		return errors.New("时间戳在未来，请检查设备时钟")
	}
	if t.Before(now.Add(-ttl - ClockSkew)) {
		return errors.New("已经过期")
	}
	return nil
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
