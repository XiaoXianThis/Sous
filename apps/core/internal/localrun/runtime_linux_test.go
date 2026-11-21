//go:build (amd64 || arm64) && cgo && sous_linux_worker

package localrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestKVMProbeRejectsNonKVM(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-kvm")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(dir, "link")
	if err := os.Symlink("/dev/null", linked); err != nil {
		t.Fatal(err)
	}
	for _, device := range []string{file, linked, "/dev/null", filepath.Join(dir, "absent")} {
		if err := checkKVM(device); err == nil {
			t.Fatalf("accepted non-KVM path %s", device)
		}
	}
}

// Explicit gate: run on the intended service host, as its unprivileged
// service user. Test uses untouched synthetic data and no live Hub credentials.
func TestLiveHostedKVM(t *testing.T) {
	if os.Getenv("SOUS_TEST_LINUX_KVM") != "1" {
		t.Skip("requires an actual KVM host and pinned Linux fixtures")
	}
	home, err := os.MkdirTemp("/tmp", "sous-kvm-")
	if err != nil {
		t.Fatal(err)
	}
	runtimeHome := filepath.Join(home, "run")
	t.Cleanup(func() {
		if _, err := os.Lstat(journal(runtimeHome)); !errors.Is(err, os.ErrNotExist) {
			t.Logf("runtime retained for reconciliation: %s", home)
			return
		}
		if err := os.RemoveAll(home); err != nil {
			t.Error(err)
		}
	})
	sentinel := filepath.Join(home, "host-only")
	// Initialize refuses unrelated material in the runtime home. Keep our
	// sentinel next to the runtime rather than inside its directory.
	if err := os.WriteFile(sentinel, []byte("synthetic host data"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	o := GuestOptions{Home: runtimeHome, RuntimePath: os.Getenv("MSB_PATH"), FirmwarePath: os.Getenv("MSB_LIBKRUNFW_PATH"), ImageArchive: os.Getenv("SOUS_TEST_MSBOX_IMAGE_ARCHIVE"), Timeout: 20 * time.Second}
	if err := InitializeHosted(ctx, o); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"project/note.txt": []byte("baseline")}
	o.Command = []string{"/bin/sh", "-ec", `
   test ! -e "$1"
   test -z "$(ip route)"
   test "$(cat project/note.txt)" = baseline
   printf changed > project/note.txt
  `, "test", sentinel}
	var checkpointed bool
	if err := RunHosted(ctx, o, files, func(exit int, snapshot map[string][]byte) error {
		if exit != 0 || string(snapshot["project/note.txt"]) != "changed" || len(snapshot) != 1 {
			return errors.New("offline Guest or snapshot verification failed")
		}
		if _, err := os.Stat(journal(runtimeHome)); err != nil {
			return errors.New("Guest journal was missing before checkpoint")
		}
		// Durable write, as in the hosted worker, before it destroys the Guest.
		f, err := os.OpenFile(filepath.Join(home, "checkpoint"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, err = f.Write(snapshot["project/note.txt"])
		err = errors.Join(err, f.Sync(), f.Close())
		checkpointed = err == nil
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(journal(runtimeHome)); !checkpointed || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checkpoint or Guest removal did not finish: checkpoint=%v journal=%v", checkpointed, err)
	}
	if got := string(files["project/note.txt"]); got != "baseline" {
		t.Fatalf("Guest changed input snapshot: %q", got)
	}
}
