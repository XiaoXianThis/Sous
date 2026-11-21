//go:build cgo && ((darwin && arm64) || (linux && (amd64 || arm64) && sous_linux_worker))

package cloudworker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/cloudwork"
)

func TestCheckpointBindsRecoveryToSameWorkspaceAndRuntime(t *testing.T) {
	state := t.TempDir()
	root, err := os.OpenRoot(state)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	const origin = "https://hosted.sous.test"
	const runtime = "/tmp/sous-runtime"
	if err := bindState(root, origin, runtime, 0); err != nil {
		t.Fatal(err)
	}
	pending := cloudwork.Complete{JobID: "test", Lease: strings.Repeat("c", 64), Outcome: "succeeded", ExitCode: 0, Files: cloudwork.Bundle{"notes.txt": []byte("durable")}}
	if err := savePending(root, pending); err != nil {
		t.Fatal(err)
	}
	if err := savePending(root, pending); err == nil {
		t.Fatal("replaced unpublished result")
	}
	if err := bindState(root, origin, "/tmp/another-runtime", 0); err == nil {
		t.Fatal("changed runtime with stale Guest journal")
	}
	if err := bindState(root, "https://different.sous.test", runtime, 0); err == nil {
		t.Fatal("changed workspace with pending output")
	}
	if err := bindState(root, origin, runtime, 0); err != nil {
		t.Fatal(err)
	}
	onDisk, err := readPending(root)
	if err != nil || onDisk.Lease != pending.Lease || string(onDisk.Files["notes.txt"]) != "durable" {
		t.Fatalf("persisted state does not match: %v", err)
	}
	if i, err := os.Stat(filepath.Join(state, pendingName)); err != nil || i.Mode().Perm() != 0o600 {
		t.Fatal("checkpoint is not private", err)
	}
}

func TestCorruptCheckpointCannotAuthorizeGuestRecovery(t *testing.T) {
	cases := []struct{ name, file, body string }{
		{"incomplete write", pendingName + ".partial", "{"},
		{"empty object", pendingName, "{}"},
		{"trailing data", pendingName, `{"job_id":"test","lease":"` + strings.Repeat("c", 64) + `","outcome":"succeeded","exit_code":0} null`},
		{"invalid outcome", pendingName, `{"job_id":"test","lease":"` + strings.Repeat("c", 64) + `","outcome":"failed","exit_code":0}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, err := os.OpenRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if err := root.WriteFile(tc.file, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if pending, err := readPending(root); err == nil || pending != nil {
				t.Fatalf("accepted unrecoverable checkpoint: %v, %v", pending != nil, err)
			}
			if actual, err := root.ReadFile(tc.file); err != nil || string(actual) != tc.body {
				t.Fatal("preflight discarded evidence", err)
			}
		})
	}
}

func TestManagedSessionPersistsButCannotCrossGeneration(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := workerSession(root, false); err == nil {
		t.Fatal("missing session was invented for recovery")
	}
	const origin = "https://hosted.sous.test"
	const runtime = "/tmp/sous-runtime"
	if err := bindState(root, origin, runtime, 1); err != nil {
		t.Fatal(err)
	}
	session, err := workerSession(root, true)
	if err != nil || !cloudwork.ValidToken(session) {
		t.Fatal("session could not be provisioned", err)
	}
	if loaded, err := workerSession(root, false); err != nil || loaded != session {
		t.Fatal("recovery did not retain the same session", err)
	}
	for _, generation := range []int64{0, 2} {
		if err := bindState(root, origin, runtime, generation); err == nil {
			t.Fatal("session state rebound to another execution identity")
		}
	}
}
