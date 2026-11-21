package localrun

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReportUsesOpaqueArtifactNames(t *testing.T) {
	orig := newSnapshot()
	orig.files["same.txt"] = []byte("same")
	orig.files["a.txt"] = []byte("before")
	orig.files["z.txt"] = []byte("removed")
	next := newSnapshot()
	next.files["same.txt"] = []byte("same")
	next.files["a.txt"] = []byte("after")
	next.files["b.txt"] = []byte("added")
	output, err := writeReport(t.TempDir(), "run", orig, next, 3, []byte("message"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.ExitCode != 3 || report.Imported != 3 || len(report.Changes) != 3 {
		t.Fatalf("unexpected report: %+v", report)
	}
	for i, operation := range []string{"modified", "added", "removed"} {
		change := report.Changes[i]
		if change.Operation != operation {
			t.Fatalf("change: %+v", change)
		}
		for _, pair := range [][2]string{{change.BeforeFile, change.Before}, {change.AfterFile, change.After}} {
			if pair[0] == "" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(output, pair[0]))
			if err != nil || digest(data) != pair[1] {
				t.Fatalf("artifact checksum failed: %v", err)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(output, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("guest controlled path was written")
	}
}
