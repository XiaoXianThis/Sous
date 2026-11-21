package localrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func fixture(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	d, err := filepath.EvalSymlinks(d)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitCaptureAndFiltering(t *testing.T) {
	d := fixture(t)
	p := filepath.Join(d, "project")
	if err := os.MkdirAll(filepath.Join(p, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(p, "main.txt"), "visible")
	put(t, filepath.Join(p, "sub", "测试.txt"), "nested")
	put(t, filepath.Join(p, ".env"), "not imported")
	put(t, filepath.Join(p, "sub", "credentials.json"), "not imported")
	put(t, filepath.Join(p, "local.pem"), "not imported")
	put(t, filepath.Join(d, "outside.txt"), "outside")
	got, err := capture(context.Background(), p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.files) != 2 || len(got.dirs) != 1 || got.skipped != 3 || string(got.files["main.txt"]) != "visible" || string(got.files["sub/测试.txt"]) != "nested" {
		t.Fatalf("incorrect capture: %+v", got)
	}
	one, err := capture(context.Background(), filepath.Join(p, "main.txt"), nil)
	if err != nil || len(one.files) != 1 || string(one.files["main.txt"]) != "visible" {
		t.Fatalf("single file: %+v, %v", one, err)
	}
	if _, err := capture(context.Background(), filepath.Join(p, ".env"), nil); err == nil {
		t.Fatal("sensitive file explicitly selected was imported")
	}
}

func TestCaptureRejectsNonRegularAndLinkedInputs(t *testing.T) {
	d := fixture(t)
	p := filepath.Join(d, "project")
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(d, "outside.txt")
	put(t, outside, "outside")
	for _, tc := range []struct {
		name string
		make func(string) error
	}{
		{"symlink", func(p string) error { return os.Symlink(outside, p) }},
		{"hardlink", func(p string) error { return os.Link(outside, p) }},
		{"fifo", func(p string) error { return unix.Mkfifo(p, 0o600) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := filepath.Join(p, tc.name)
			if err := tc.make(name); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Remove(name) })
			captured, err := capture(context.Background(), p, nil)
			if err == nil || len(captured.files) != 0 {
				t.Fatalf("imported forbidden entry: %+v %v", captured, err)
			}
		})
	}
	alias := filepath.Join(d, "alias")
	if err := os.Symlink(p, alias); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(p, "note.txt"), "letter")
	if _, err := capture(context.Background(), filepath.Join(alias, "note.txt"), nil); err == nil {
		t.Fatal("ancestor symlink followed")
	}
	if _, err := capture(context.Background(), "/", nil); err == nil {
		t.Fatal("root selection not rejected")
	}
	put(t, filepath.Join(p, "too-big"), strings.Repeat("z", MaxFile+1))
	if _, err := capture(context.Background(), p, nil); err == nil {
		t.Fatal("read above file limit")
	}
}

func TestCaptureChecksSelectedIdentity(t *testing.T) {
	d := fixture(t)
	selected := filepath.Join(d, "doc")
	put(t, selected, "authorized")
	want, err := os.Lstat(selected)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(selected, filepath.Join(d, "original")); err != nil {
		t.Fatal(err)
	}
	put(t, selected, "not authorized")
	got, err := capture(context.Background(), selected, want)
	if err == nil || len(got.files) != 0 {
		t.Fatalf("read replacement content: %+v %v", got, err)
	}
}
