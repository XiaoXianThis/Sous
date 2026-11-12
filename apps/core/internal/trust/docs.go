package trust

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// Roster lists who belongs: the user's identity and passkeys, and every
// machine. Only the identity key signs it; versions only grow.
type Roster struct {
	T       string       `json:"t"`
	V       int64        `json:"v"`
	IAT     int64        `json:"iat"`
	Prev    string       `json:"prev,omitempty"` // Signed.Hash of the version before
	User    User         `json:"user"`
	RPID    string       `json:"rp_id"`
	Origins []string     `json:"origins"` // page origins an assertion may come from
	Relay   string       `json:"relay"`
	Nodes   []RosterNode `json:"nodes"`
}

type User struct {
	SignPub  string    `json:"sign_pub"`
	KEMPub   string    `json:"kem_pub"`
	Passkeys []Passkey `json:"passkeys"`
}

type Passkey struct {
	CredID string `json:"cred_id"`
	Pub    string `json:"pub"` // uncompressed P-256 point
	Label  string `json:"label,omitempty"`
	Added  int64  `json:"added,omitempty"`
}

type RosterNode struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	SignPub    string `json:"sign_pub"`
	KEMPub     string `json:"kem_pub"`
	KeyStorage string `json:"key_storage,omitempty"` // hardware / software
	OS         string `json:"os,omitempty"`
	Role       string `json:"role,omitempty"`
	Brain      *Brain `json:"brain,omitempty"`
	Frozen     bool   `json:"frozen,omitempty"`
	Added      int64  `json:"added,omitempty"`
}

// Brain marks a machine the user allows to run the brain. Lower Priority
// wins the election.
type Brain struct {
	Priority int `json:"priority"`
}

// Node finds a machine by id.
func (r *Roster) Node(id string) (RosterNode, bool) {
	for _, n := range r.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return RosterNode{}, false
}

// Passkey finds a passkey by credential id.
func (r *Roster) Passkey(credID string) (Passkey, bool) {
	for _, p := range r.User.Passkeys {
		if p.CredID == credID {
			return p, true
		}
	}
	return Passkey{}, false
}

// Brains lists the brain machines by priority.
func (r *Roster) Brains() []RosterNode {
	var out []RosterNode
	for _, n := range r.Nodes {
		if n.Brain != nil && !n.Frozen {
			out = append(out, n)
		}
	}
	slices.SortStableFunc(out, func(a, b RosterNode) int { return a.Brain.Priority - b.Brain.Priority })
	return out
}

// IdentityPub decodes the user's signing key.
func (r *Roster) IdentityPub() []byte {
	b, _ := B64.DecodeString(r.User.SignPub)
	return b
}

// RosterUpdate is how a new roster travels: the signed document, plus an
// assertion when the change widens who belongs or removes a machine.
type RosterUpdate struct {
	Doc       Signed     `json:"doc"`
	Assertion *Assertion `json:"assertion,omitempty"`
}

// OpenRoster verifies a roster signed by the pinned identity key.
func OpenRoster(identityPub []byte, s Signed) (Roster, error) {
	var r Roster
	if _, err := s.Open(identityPub, TRoster, &r); err != nil {
		return r, err
	}
	if r.User.SignPub != B64.EncodeToString(identityPub) {
		return r, errors.New("名册属于另一个身份")
	}
	return r, nil
}

// AcceptRoster checks an update against the current roster (nil before the
// first one) and returns the new roster. used reports and records assertion
// challenges already spent (nil skips the replay check).
func AcceptRoster(identityPub []byte, cur *Roster, u RosterUpdate, now time.Time, used func(string) bool) (Roster, error) {
	next, err := OpenRoster(identityPub, u.Doc)
	if err != nil {
		return next, err
	}
	if cur != nil && next.V <= cur.V {
		return next, fmt.Errorf("名册 v%d 不比 v%d 新", next.V, cur.V)
	}
	if cur != nil && RosterNeedsAssertion(cur, &next) {
		if u.Assertion == nil {
			return next, errors.New("这个名册改动需要你本人验证（Face ID）")
		}
		raw, _ := u.Doc.Payload()
		// The current roster names the passkeys allowed to approve the change.
		// Versions only grow, so a later reader may check an old approval:
		// there is no freshness check here, only the binding to these bytes.
		if err := VerifyAssertion(cur, *u.Assertion, CriticalChallenge(raw), now, used); err != nil {
			return next, err
		}
	}
	return next, nil
}

// RosterNeedsAssertion reports whether going from a to b needs the user's
// fresh verification: anything that adds or removes a member, changes a
// key, a brain, the relay or the origins, or unfreezes a machine. Freezing
// and renaming do not.
func RosterNeedsAssertion(a, b *Roster) bool {
	if a.RPID != b.RPID || a.Relay != b.Relay || !slices.Equal(a.Origins, b.Origins) || a.User.KEMPub != b.User.KEMPub {
		return true
	}
	if len(a.User.Passkeys) != len(b.User.Passkeys) {
		return true
	}
	for i := range a.User.Passkeys {
		if a.User.Passkeys[i].CredID != b.User.Passkeys[i].CredID || a.User.Passkeys[i].Pub != b.User.Passkeys[i].Pub {
			return true
		}
	}
	if len(a.Nodes) != len(b.Nodes) {
		return true
	}
	for _, nb := range b.Nodes {
		na, ok := a.Node(nb.ID)
		if !ok || na.SignPub != nb.SignPub || na.KEMPub != nb.KEMPub {
			return true
		}
		if (na.Brain == nil) != (nb.Brain == nil) || (na.Brain != nil && na.Brain.Priority != nb.Brain.Priority) {
			return true
		}
		if na.Frozen && !nb.Frozen {
			return true
		}
	}
	return false
}

// SessionCert lets a page's session key act for the user until Exp.
type SessionCert struct {
	T       string `json:"t"`
	SID     string `json:"sid"`
	SignPub string `json:"sign_pub"`
	KEMPub  string `json:"kem_pub"`
	Label   string `json:"label,omitempty"`
	IAT     int64  `json:"iat"`
	Exp     int64  `json:"exp"`
}

// MaxSessionCert caps a certificate's lifetime; the page renews it.
const MaxSessionCert = 24 * time.Hour

// OpenSessionCert verifies a session certificate against the roster.
func OpenSessionCert(r *Roster, s Signed, now time.Time) (SessionCert, error) {
	var c SessionCert
	if _, err := s.Open(r.IdentityPub(), TSession, &c); err != nil {
		return c, err
	}
	pub, err := B64.DecodeString(c.SignPub)
	if err != nil || c.SID != KeyID(pub) {
		return c, errors.New("会话 id 和密钥不符")
	}
	if now.Unix() > c.Exp+int64(ClockSkew/time.Second) {
		return c, errors.New("会话证书已过期")
	}
	if c.Exp-c.IAT > int64(MaxSessionCert/time.Second) {
		return c, errors.New("会话证书有效期太长")
	}
	return c, nil
}

// Scope is what the brain may do on its own (decision 0031).
type Scope struct {
	Nodes      []string `json:"nodes,omitempty"`    // empty: every machine
	MaxMode    string   `json:"max_mode,omitempty"` // default / auto; empty means auto
	AllowMerge bool     `json:"allow_merge,omitempty"`
	AllowOps   bool     `json:"allow_ops,omitempty"`  // ops runs: deploys and maintenance on servers
	AllowOpen  bool     `json:"allow_open,omitempty"` // open permission mode
}

// Scopes is the user's signed autonomy scope: a default and per-project
// overrides keyed by project key (internal/project.Key).
type Scopes struct {
	T        string           `json:"t"`
	V        int64            `json:"v"`
	IAT      int64            `json:"iat"`
	Default  Scope            `json:"default"`
	Projects map[string]Scope `json:"projects,omitempty"`
}

// DefaultScopes is what applies before the user signs any: auto mode,
// branches and pull requests, no merges, no ops, no open mode.
func DefaultScopes() Scopes {
	return Scopes{T: TScopes, Default: Scope{MaxMode: "auto"}}
}

// For returns the scope of a project key.
func (s Scopes) For(projectKey string) Scope {
	if sc, ok := s.Projects[projectKey]; ok && projectKey != "" {
		return sc
	}
	return s.Default
}

// Pause stops the brain's work when Paused; only a session signs it.
type Pause struct {
	T      string `json:"t"`
	V      int64  `json:"v"`
	IAT    int64  `json:"iat"`
	Paused bool   `json:"paused"`
}

// Lease says which brain machine thinks for everyone until Exp.
type Lease struct {
	T      string `json:"t"`
	Term   int64  `json:"term"`
	Holder string `json:"holder"`
	IAT    int64  `json:"iat"`
	Exp    int64  `json:"exp"`
}

// Lease timing (design §8.2). Variables only so tests can shorten them.
var (
	LeaseTTL   = 90 * time.Second
	LeaseRenew = 30 * time.Second
)

// OpenLease verifies a lease signed by a brain machine of the roster.
func OpenLease(r *Roster, s Signed) (Lease, error) {
	var l Lease
	raw, err := s.Payload()
	if err != nil {
		return l, err
	}
	var head Lease
	if err := jsonUnmarshal(raw, &head); err != nil {
		return l, err
	}
	n, ok := r.Node(head.Holder)
	if !ok || n.Brain == nil {
		return l, fmt.Errorf("trust: %s may not hold the lease", head.Holder)
	}
	if n.Frozen {
		return l, fmt.Errorf("trust: %s is frozen", head.Holder)
	}
	pub, _ := B64.DecodeString(n.SignPub)
	if _, err := s.Open(pub, TLease, &l); err != nil {
		return l, err
	}
	return l, nil
}

// Live reports whether the lease is still valid at now.
func (l Lease) Live(now time.Time) bool { return l.Holder != "" && now.Unix() <= l.Exp }

// Newer reports whether l should replace cur.
func (l Lease) Newer(cur Lease) bool {
	return l.Term > cur.Term || (l.Term == cur.Term && l.Holder == cur.Holder && l.Exp > cur.Exp)
}

// ClockSkew is the tolerance for every timestamp check.
const ClockSkew = 2 * time.Minute

// CommandTTL is how long a permit or intent stays usable.
const CommandTTL = 5 * time.Minute

// ScopesUpdate carries the autonomy scope with the assertion that approved it.
type ScopesUpdate struct {
	Doc       Signed     `json:"doc"`
	Assertion *Assertion `json:"assertion,omitempty"`
}

func (u ScopesUpdate) Open(id []byte) (Scopes, error) {
	var sc Scopes
	_, err := u.Doc.Open(id, TScopes, &sc)
	return sc, err
}

// AcceptScopes checks a new autonomy scope: signed by the identity, newer,
// and approved by the user just now (changing it is a critical operation).
func AcceptScopes(r *Roster, prev *Scopes, u ScopesUpdate, now time.Time, used func(string) bool) (Scopes, error) {
	if r == nil {
		return Scopes{}, errors.New("没有名册")
	}
	sc, err := u.Open(r.IdentityPub())
	if err != nil {
		return sc, err
	}
	if prev != nil && sc.V <= prev.V {
		return sc, fmt.Errorf("自主范围 v%d 不比现有的 v%d 新", sc.V, prev.V)
	}
	if u.Assertion == nil {
		return sc, errors.New("修改自主范围需要你本人验证")
	}
	raw, _ := u.Doc.Payload()
	if err := VerifyAssertion(r, *u.Assertion, CriticalChallenge(raw), now, used); err != nil {
		return sc, err
	}
	return sc, nil
}
