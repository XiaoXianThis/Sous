package trust_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
)

// The page (apps/web/src/trust) and this package must agree byte for byte.
// SOUS_WRITE_VECTORS=1 writes Go's vectors for the page's tests; the page's
// tests write theirs (SOUS_WRITE_VECTORS=1 bun run test) and this test
// checks them whenever the file exists.

var fixtures = filepath.Join("..", "..", "..", "..", "work", "tests", "fixtures")

type goVectors struct {
	Sign struct {
		Pub, Ctx, Msg, Sig string
	} `json:"sign"`
	Doc struct {
		Pub string       `json:"pub"`
		Doc trust.Signed `json:"doc"`
	} `json:"doc"`
	Seal struct {
		Seed, Pub, Info, CT, PT string
	} `json:"seal"`
	Chan struct {
		SenderPub     string       `json:"sender_pub"`
		RecipientSeed string       `json:"recipient_seed"`
		Me            string       `json:"me"`
		Wires         []trust.Wire `json:"wires"`
		Plain         []string     `json:"plain"`
	} `json:"chan"`
	Pair struct {
		Code, Secret, FP, RelayHash string
		Exp                         int64
	} `json:"pair"`
	Domain struct {
		Seed, Member, Wrap, Key string
		Epoch                   int64
	} `json:"domain"`
}

type webVectors struct {
	Sign struct {
		Pub, Ctx, Msg, Sig string
	} `json:"sign"`
	Doc struct {
		Pub string       `json:"pub"`
		Doc trust.Signed `json:"doc"`
	} `json:"doc"`
	Seal struct {
		Info, CT, PT string
	} `json:"seal"`
	Chan struct {
		SenderPub string       `json:"sender_pub"`
		Me        string       `json:"me"`
		Wires     []trust.Wire `json:"wires"`
		Plain     []string     `json:"plain"`
	} `json:"chan"`
	Pair struct {
		Code, Secret string
	} `json:"pair"`
}

func TestWriteVectors(t *testing.T) {
	if os.Getenv("SOUS_WRITE_VECTORS") != "1" {
		t.Skip("set SOUS_WRITE_VECTORS=1 to rewrite work/tests/fixtures/trust-vectors.json")
	}
	var v goVectors
	k, _ := trust.NewSignKey()
	v.Sign.Pub, v.Sign.Ctx, v.Sign.Msg = trust.B64.EncodeToString(k.Public()), "sous/v1/test", "hello 你好"
	v.Sign.Sig = trust.B64.EncodeToString(k.Sign(v.Sign.Ctx, []byte(v.Sign.Msg)))

	w := newWorld(t)
	v.Doc.Pub, v.Doc.Doc = trust.B64.EncodeToString(w.id.Public()), w.doc

	rk, _ := trust.NewKEMKey()
	v.Seal.Seed, v.Seal.Pub, v.Seal.Info, v.Seal.PT = trust.B64.EncodeToString(rk.Bytes()), trust.B64.EncodeToString(rk.Public()), "sous/v1/test-seal", "sealed 密文"
	ct, err := trust.SealTo(rk.Public(), v.Seal.Info, []byte(v.Seal.PT))
	if err != nil {
		t.Fatal(err)
	}
	v.Seal.CT = trust.B64.EncodeToString(ct)

	out, err := trust.NewOutChannel("node:a", "sess:b", k, rk.Public(), time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	v.Chan.SenderPub, v.Chan.RecipientSeed, v.Chan.Me = v.Sign.Pub, v.Seal.Seed, "sess:b"
	for _, p := range []string{`{"k":"one"}`, `{"k":"two","x":"中文"}`} {
		v.Chan.Wires = append(v.Chan.Wires, out.Seal([]byte(p)))
		v.Chan.Plain = append(v.Chan.Plain, p)
	}

	pc := trust.PairCode{RelayHash: trust.RelayHash("wss://relay.example"), IdentityFP: trust.Fingerprint(w.id.Public()), Secret: trust.RandomBytes(16), Exp: time.Unix(1893456000, 0)}
	v.Pair.Code, v.Pair.Secret, v.Pair.FP, v.Pair.RelayHash, v.Pair.Exp = pc.Encode(), trust.B64.EncodeToString(pc.Secret),
		trust.B64.EncodeToString(pc.IdentityFP), trust.B64.EncodeToString(pc.RelayHash), pc.Exp.Unix()

	key := trust.RandomBytes(32)
	v.Domain.Seed, v.Domain.Member, v.Domain.Epoch, v.Domain.Key = v.Seal.Seed, "user", 7, trust.B64.EncodeToString(key)
	v.Domain.Wrap, _ = trust.WrapDomainKey(key, 7, "user", rk.Public())

	raw, _ := json.MarshalIndent(v, "", "  ")
	if err := os.WriteFile(filepath.Join(fixtures, "trust-vectors.json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestWebVectors checks what the page produced.
func TestWebVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(fixtures, "trust-vectors-web.json"))
	if os.IsNotExist(err) {
		t.Skip("no web vectors yet")
	}
	if err != nil {
		t.Fatal(err)
	}
	var v webVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	pub, _ := trust.B64.DecodeString(v.Sign.Pub)
	sig, _ := trust.B64.DecodeString(v.Sign.Sig)
	if err := trust.Verify(pub, v.Sign.Ctx, []byte(v.Sign.Msg), sig); err != nil {
		t.Fatal("web signature:", err)
	}
	dpub, _ := trust.B64.DecodeString(v.Doc.Pub)
	var r trust.Roster
	if _, err := v.Doc.Doc.Open(dpub, trust.TRoster, &r); err != nil || r.V != 1 {
		t.Fatal("web roster:", err)
	}
	var g goVectors
	graw, _ := os.ReadFile(filepath.Join(fixtures, "trust-vectors.json"))
	json.Unmarshal(graw, &g)
	seed, _ := trust.B64.DecodeString(g.Seal.Seed)
	rk, err := trust.KEMKeyFromBytes(seed)
	if err != nil {
		t.Fatal(err)
	}
	ct, _ := trust.B64.DecodeString(v.Seal.CT)
	pt, err := rk.OpenFrom(v.Seal.Info, ct)
	if err != nil || string(pt) != v.Seal.PT {
		t.Fatal("web seal:", err)
	}
	spub, _ := trust.B64.DecodeString(v.Chan.SenderPub)
	// The vectors are older than ChanOpenTTL: accept them at their own time.
	o, _ := trust.PeekOpen(*v.Chan.Wires[0].Open)
	in, err := trust.AcceptChannel(*v.Chan.Wires[0].Open, spub, v.Chan.Me, rk, time.Unix(o.IAT, 0))
	if err != nil {
		t.Fatal("web channel:", err)
	}
	for i, w := range v.Chan.Wires {
		pt, err := in.Open(w)
		if err != nil || string(pt) != v.Chan.Plain[i] {
			t.Fatal("web channel message", i, err)
		}
	}
	pc, err := trust.ParsePairCode(v.Pair.Code)
	if err != nil || trust.B64.EncodeToString(pc.Secret) != v.Pair.Secret {
		t.Fatal("web pair code:", err)
	}
}
