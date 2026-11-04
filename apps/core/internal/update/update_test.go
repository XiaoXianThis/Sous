package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "v0.1.9", true}, {"v0.1.0", "v0.1.0", false}, {"v1.0.0", "v0.99.0", true},
		{"v0.1.0", "dev", true}, {"dev", "v0.1.0", false}, {"0.1.1", "v0.1.0", true}, {"v0.1.0-rc1", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

// fakeGitHub serves one release with the sous archive for this platform.
func fakeGitHub(t *testing.T, binary []byte, corrupt bool) *httptest.Server {
	t.Helper()
	name := AssetName("sous", "v0.2.0", runtime.GOOS, runtime.GOARCH)
	var archive bytes.Buffer
	bin := "sous"
	if runtime.GOOS == "windows" {
		bin = "sous.exe"
		zw := zip.NewWriter(&archive)
		f, _ := zw.Create(bin)
		f.Write(binary)
		zw.Close()
	} else {
		gz := gzip.NewWriter(&archive)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
		tw.Write([]byte("hi"))
		tw.WriteHeader(&tar.Header{Name: bin, Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg})
		tw.Write(binary)
		tw.Close()
		gz.Close()
	}
	sum := sha256.Sum256(archive.Bytes())
	sums := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	if corrupt {
		sums = strings.Repeat("0", 64) + "  " + name + "\n"
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/u/sous/releases/latest", "/repos/u/sous/releases/tags/v0.2.0":
			json.NewEncoder(w).Encode(Release{Tag: "v0.2.0", Assets: []Asset{
				{Name: name, URL: srv.URL + "/dl/" + name}, {Name: "checksums.txt", URL: srv.URL + "/dl/checksums.txt"},
			}})
		case "/dl/" + name:
			w.Write(archive.Bytes())
		case "/dl/checksums.txt":
			w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestInstallReplacesExecutable(t *testing.T) {
	srv := fakeGitHub(t, []byte("new binary"), false)
	exe := filepath.Join(t.TempDir(), "sous")
	os.WriteFile(exe, []byte("old binary"), 0o755)
	tag, err := Client{Repo: "u/sous", APIBase: srv.URL}.Install(context.Background(), "", exe)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(exe)
	if tag != "v0.2.0" || string(got) != "new binary" {
		t.Fatalf("tag %s, exe %q", tag, got)
	}
	if info, _ := os.Stat(exe); runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		t.Fatal("the new executable is not executable")
	}
}

func TestInstallRefusesBadChecksum(t *testing.T) {
	srv := fakeGitHub(t, []byte("evil"), true)
	exe := filepath.Join(t.TempDir(), "sous")
	os.WriteFile(exe, []byte("old binary"), 0o755)
	if _, err := (Client{Repo: "u/sous", APIBase: srv.URL}).Install(context.Background(), "v0.2.0", exe); err == nil || !strings.Contains(err.Error(), "校验和") {
		t.Fatalf("err: %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old binary" {
		t.Fatal("the executable was replaced")
	}
}

func TestNoRelease(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if _, err := (Client{Repo: "u/none", APIBase: srv.URL}).Release(context.Background(), ""); err != ErrNoRelease {
		t.Fatalf("err: %v", err)
	}
}
