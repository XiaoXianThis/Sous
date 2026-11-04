package acp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/acp/acptest"
)

func TestConfigOptions(t *testing.T) {
	dir := t.TempDir()
	cmd := acptest.Command("write", dir)
	cmd.Env = append(cmd.Env, "FAKE_ACP_OPTIONS=1")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cl, err := acp.Start(ctx, cmd, acp.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	s, err := cl.NewSession(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Options) != 2 { // the boolean option is skipped
		t.Fatalf("options = %+v", s.Options)
	}
	m := s.Options[0]
	if m.ID != "model" || m.Category != "model" || m.Current != "fast" || len(m.Values) != 2 || m.Values[1].Group != "Deep" || !m.Has("smart") {
		t.Fatalf("model option = %+v", m)
	}
	opts, err := cl.SetConfigOption(ctx, s, "effort", "high")
	if err != nil {
		t.Fatal(err)
	}
	if opts[1].Current != "high" {
		t.Fatalf("after set: %+v", opts[1])
	}
}

func TestParseLegacyModels(t *testing.T) {
	raw := []json.RawMessage{json.RawMessage(`{"id":"x","name":"X","type":"select","currentValue":"a","options":[{"value":"a","name":"A"}]}`),
		json.RawMessage(`{"id":"b","type":"boolean","currentValue":true}`), json.RawMessage(`not json`)}
	opts := acp.ParseConfigOptions(raw)
	if len(opts) != 1 || opts[0].Values[0].Name != "A" {
		t.Fatalf("got %+v", opts)
	}
}
