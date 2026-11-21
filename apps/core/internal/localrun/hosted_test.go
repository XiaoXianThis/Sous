//go:build cgo && ((darwin && arm64) || (linux && (amd64 || arm64) && sous_linux_worker))

package localrun

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
)

func TestPortableSnapshotOrdersDirectoriesAndCopiesBytes(t *testing.T) {
	input := map[string][]byte{"z/doc.txt": []byte("z"), "a/b/note.txt": []byte("safe"), "a/other.txt": nil}
	s, err := portableSnapshot(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.dirs, []string{"a", "a/b", "z"}) || s.binding != nil || s.entries != 6 {
		t.Fatalf("unexpected portable snapshot: %+v", s)
	}
	input["a/b/note.txt"][0] = 'x'
	if string(s.files["a/b/note.txt"]) != "safe" {
		t.Fatal("snapshot aliases the lease payload")
	}
}

func TestPortableSnapshotRejectsUnsafeMaterial(t *testing.T) {
	for _, input := range []map[string][]byte{
		{}, {"../path": nil}, {".env": nil}, {"sub/note.txt": nil, "sub": nil}, {"big.txt": make([]byte, MaxFile+1)},
	} {
		if _, err := portableSnapshot(context.Background(), input); err == nil {
			t.Fatal("accepted invalid staged files")
		}
	}
}

func TestHostedRefusesUnreconciledGuest(t *testing.T) {
	home := t.TempDir()
	if err := requireNoGuest(home); err != nil {
		t.Fatal(err)
	}
	const marker = "sous-run-testfixture"
	if err := os.WriteFile(journal(home), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireNoGuest(home); !errors.Is(err, ErrCleanupUnconfirmed) {
		t.Fatalf("unreconciled Guest was accepted: %v", err)
	}
	if data, err := os.ReadFile(journal(home)); err != nil || string(data) != marker {
		t.Fatalf("unreconciled state was changed: %q %v", data, err)
	}
}
