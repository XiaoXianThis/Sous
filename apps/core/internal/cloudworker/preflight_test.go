//go:build cgo && ((darwin && arm64) || (linux && (amd64 || arm64) && sous_linux_worker))

package cloudworker

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type denyRequest func(*http.Request) (*http.Response, error)

func (d denyRequest) RoundTrip(r *http.Request) (*http.Response, error) { return d(r) }

func TestInvalidRuntimeDoesNotClaimAJob(t *testing.T) {
	// Keep the short runtime path within the SDK's limit on both test hosts.
	dir, err := os.MkdirTemp("/tmp", "s-p-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{origin: "https://hosted.sous.test", http: &http.Client{Transport: denyRequest(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("preflight contacted Hub: %s", r.URL)
		return nil, nil
	})}}
	err = Run(context.Background(), c, Options{State: filepath.Join(dir, "state"), Runtime: filepath.Join(dir, "runtime"), MSB: filepath.Join(dir, "missing-msb"), Firmware: filepath.Join(dir, "missing-firmware"), Image: filepath.Join(dir, "missing-image"), Once: true})
	if err == nil {
		t.Fatal("invalid backend reported ready")
	}
}
