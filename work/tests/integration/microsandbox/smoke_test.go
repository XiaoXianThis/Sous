package microsandbox_test

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// TestLocalIsolation is a prerequisite check, not proof that the whole Node
// boundary is correct. It never mounts a real project or uses account data.
func TestLocalIsolation(t *testing.T) {
	if os.Getenv("SOUS_TEST_MICROSANDBOX") != "1" {
		t.Skip("set SOUS_TEST_MICROSANDBOX=1 to boot a real local VM")
	}
	for _, env := range []string{"MSB_PATH", "MSB_LIBKRUNFW_PATH"} {
		p := os.Getenv(env)
		if !filepath.IsAbs(p) {
			t.Fatalf("%s must name a pre-provisioned runtime file", env)
		}
		if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
			t.Fatalf("%s must name a regular file", env)
		}
	}
	msbHome, err := os.MkdirTemp(os.Getenv("SOUS_TEST_SHORT_TMP"), "s-msb-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(msbHome) })
	t.Setenv("MSB_HOME", msbHome)
	backend, err := msb.DefaultBackendInfo()
	if err != nil || backend.Kind != msb.BackendLocal {
		t.Fatalf("requires Microsandbox's local backend, kind %q: %v", backend.Kind, err)
	}
	name := "s-smoke-" + rand.Text()[:8]
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if _, err := msb.ResolveRuntime(msb.RuntimeConfig{}); err != nil {
		t.Fatal(err)
	}

	original := filepath.Join(t.TempDir(), "project-note.txt")
	outside := filepath.Join(t.TempDir(), "not-granted.txt")
	if err := os.WriteFile(original, []byte("user file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("host-only marker"), 0o600); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	box, err := msb.CreateSandbox(ctx, name, msb.WithImage("alpine:3.22"), msb.WithNetwork(msb.NetworkPolicy.None()))
	if err != nil {
		cleanup, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		if handle, err := msb.GetSandbox(cleanup, name); err == nil {
			if err := handle.Destroy(cleanup, msb.WithDestroyForce()); err != nil {
				t.Errorf("cleanup after failed start: %v", err)
			}
		} else if !msb.IsKind(err, msb.ErrSandboxNotFound) {
			t.Errorf("check guest after failed start: %v", err)
		}
		t.Fatalf("boot fresh guest: %v", err)
	}
	t.Logf("create and start fresh guest: %s (includes any image pull)", time.Since(start))
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 45*time.Second)
		defer done()
		at := time.Now()
		if err := box.Destroy(cleanup, msb.WithDestroyForce()); err != nil {
			t.Errorf("destroy guest: %v", err)
		}
		if err := box.Close(); err != nil {
			t.Errorf("release guest handle: %v", err)
		}
		if _, err := msb.GetSandbox(cleanup, name); !msb.IsKind(err, msb.ErrSandboxNotFound) {
			t.Errorf("guest still present or cleanup unconfirmed: %v", err)
		}
		t.Logf("stop and destroy guest: %s", time.Since(at))
	})

	guest := "/tmp/project-note.txt"
	data, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := box.FS().Write(ctx, guest, data); err != nil {
		t.Fatalf("no-network file import: %v", err)
	}
	out, err := box.Exec(ctx, "/bin/sh", []string{"-c", `test ! -e "$1" && test ! -e "$2" && test "$(cat "$3")" = 'user file' && printf 'guest change' > "$3"`, "sh", original, outside, guest})
	if err != nil {
		t.Fatalf("no-network guest exec: %v", err)
	}
	if !out.Success() {
		t.Fatalf("expected no host file visibility and writable guest copy: %d %s", out.ExitCode(), out.Stderr())
	}
	if got, err := box.FS().ReadString(ctx, guest); err != nil || got != "guest change" {
		t.Fatalf("result readback: %q %v", got, err)
	}
	if b, err := os.ReadFile(original); err != nil || string(b) != "user file" {
		t.Fatalf("host file changed: %q %v", b, err)
	}

	netCtx, finish := context.WithTimeout(ctx, 15*time.Second)
	defer finish()
	out, err = box.Exec(netCtx, "/bin/sh", []string{"-c", `if wget -q -T 3 -O /dev/null http://example.com/; then echo unexpected-egress; else echo no-egress; fi`})
	if err != nil {
		t.Fatalf("run egress probe: %v", err)
	}
	if !out.Success() || strings.TrimSpace(out.Stdout()) != "no-egress" {
		t.Fatalf("expected rejected egress, exit=%d output=%q", out.ExitCode(), out.Stdout())
	}
}
