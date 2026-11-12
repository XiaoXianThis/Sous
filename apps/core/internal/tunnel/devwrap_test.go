package tunnel_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tunnel"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tunnel/tunneltest"
)

// The relay hands a device wrap out only for a fresh assertion of that
// device's passkey, once per challenge.
func TestDevwrapNeedsFreshAssertion(t *testing.T) {
	ctx := context.Background()
	w := tunneltest.NewWorld(t)
	cred := w.Authn.Passkey().CredID
	wrap := json.RawMessage(`{"iv":"x","ct":"y"}`)
	if _, err := tunnel.DevwrapGet(ctx, w.URL, cred, "nope", trust.Assertion{}); err == nil {
		t.Fatal("got a wrap without a challenge")
	}
	if err := w.Session().DevwrapPut(ctx, "not-in-roster", wrap); err == nil {
		t.Fatal("stored a wrap for a passkey the roster does not list")
	}
	if err := w.Session().DevwrapPut(ctx, cred, wrap); err != nil {
		t.Fatal(err)
	}

	nonce, err := tunnel.DevwrapChallenge(ctx, w.URL)
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := trust.B64.DecodeString(nonce)
	if _, err := tunnel.DevwrapGet(ctx, w.URL, cred, nonce, w.Authn.Assert(ch, false)); err == nil {
		t.Fatal("an assertion without user verification opened the wrap")
	}
	nonce, _ = tunnel.DevwrapChallenge(ctx, w.URL)
	other, _ := tunnel.DevwrapChallenge(ctx, w.URL)
	ch, _ = trust.B64.DecodeString(other)
	if _, err := tunnel.DevwrapGet(ctx, w.URL, cred, nonce, w.Authn.Assert(ch, true)); err == nil {
		t.Fatal("an assertion over another challenge opened the wrap")
	}
	nonce, _ = tunnel.DevwrapChallenge(ctx, w.URL)
	ch, _ = trust.B64.DecodeString(nonce)
	a := w.Authn.Assert(ch, true)
	got, err := tunnel.DevwrapGet(ctx, w.URL, cred, nonce, a)
	if err != nil || string(got) != string(wrap) {
		t.Fatalf("wrap: %s %v", got, err)
	}
	if _, err := tunnel.DevwrapGet(ctx, w.URL, cred, nonce, a); err == nil {
		t.Fatal("a challenge worked twice")
	}
}
