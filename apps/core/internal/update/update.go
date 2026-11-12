// Package update checks GitHub Releases for a newer sous and installs it
// (docs/decisions/0008: manual updates, no code signing). The Hub checks and
// shows what is available; a daemon downloads the archive for its platform,
// verifies it against the release's checksums.txt, and replaces its own
// executable. Release archives are built by goreleaser (apps/core/.goreleaser.yaml).
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
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultRepo is where releases are published.
const DefaultRepo = "XiaoXianThis/Sous"

// maxDownload bounds a release archive.
const maxDownload = 200 << 20

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type Release struct {
	Tag         string  `json:"tag_name"`
	Name        string  `json:"name"`
	HTMLURL     string  `json:"html_url"`
	PublishedAt string  `json:"published_at"`
	Body        string  `json:"body"`
	Assets      []Asset `json:"assets"`
}

// Client talks to the GitHub API.
type Client struct {
	Repo    string // owner/name; empty: DefaultRepo
	APIBase string // empty: https://api.github.com
	HTTP    *http.Client
}

func (c Client) repo() string {
	if c.Repo == "" {
		return DefaultRepo
	}
	return c.Repo
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

// Release fetches a release by tag, or the latest one when tag is empty.
func (c Client) Release(ctx context.Context, tag string) (Release, error) {
	base := strings.TrimRight(c.APIBase, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	u := fmt.Sprintf("%s/repos/%s/releases/latest", base, c.repo())
	if tag != "" {
		u = fmt.Sprintf("%s/repos/%s/releases/tags/%s", base, c.repo(), tag)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http().Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Release{}, ErrNoRelease
	}
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub 返回 %s", resp.Status)
	}
	var r Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return Release{}, err
	}
	return r, nil
}

// ErrNoRelease means the repository has no (such) release.
var ErrNoRelease = errors.New("还没有发布任何版本")

// Checker caches the latest release for the Hub's settings page.
type Checker struct {
	Client Client
	TTL    time.Duration

	mu    sync.Mutex
	at    time.Time
	rel   Release
	err   error
	fresh bool
}

// Latest returns the cached latest release, refreshing it after TTL or when force.
func (c *Checker) Latest(ctx context.Context, force bool) (Release, time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := c.TTL
	if ttl <= 0 {
		ttl = time.Hour
	}
	if force || !c.fresh || time.Since(c.at) > ttl {
		c.rel, c.err = c.Client.Release(ctx, "")
		c.at, c.fresh = time.Now(), true
	}
	return c.rel, c.at, c.err
}

// Newer reports whether version a is newer than b. Versions look like
// v1.2.3; anything else (such as "dev") is older than every release.
func Newer(a, b string) bool {
	pa, oka := parse(a)
	pb, okb := parse(b)
	switch {
	case !oka:
		return false
	case !okb:
		return true
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// AssetName is the archive of a binary for a platform, as goreleaser names it.
func AssetName(binary, tag, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("%s_%s_%s_%s%s", binary, strings.TrimPrefix(tag, "v"), goos, goarch, ext)
}

// Install downloads the sous binary of release tag (latest when empty) for
// this platform, checks it against checksums.txt and replaces exe with it.
// It returns the installed tag.
func (c Client) Install(ctx context.Context, tag, exe string) (string, error) {
	rel, err := c.Release(ctx, tag)
	if err != nil {
		return "", err
	}
	name := AssetName("sous", rel.Tag, runtime.GOOS, runtime.GOARCH)
	var archive, sums, sig Asset
	for _, a := range rel.Assets {
		switch a.Name {
		case name:
			archive = a
		case "checksums.txt":
			sums = a
		case SigAsset:
			sig = a
		}
	}
	if archive.URL == "" {
		return "", fmt.Errorf("版本 %s 没有 %s/%s 的安装包（%s）", rel.Tag, runtime.GOOS, runtime.GOARCH, name)
	}
	if sums.URL == "" {
		return "", fmt.Errorf("版本 %s 没有 checksums.txt，无法校验，不安装", rel.Tag)
	}
	data, err := c.download(ctx, archive.URL)
	if err != nil {
		return "", err
	}
	sumsData, err := c.download(ctx, sums.URL)
	if err != nil {
		return "", err
	}
	// The release signature first: checksums.txt is only worth something
	// when the author's offline key signed it.
	if sig.URL == "" {
		return "", fmt.Errorf("版本 %s 没有发布签名（%s），不安装", rel.Tag, SigAsset)
	}
	sigData, err := c.download(ctx, sig.URL)
	if err != nil {
		return "", err
	}
	if err := VerifyRelease(rel.Tag, sumsData, sigData); err != nil {
		return "", err
	}
	if err := verify(data, name, string(sumsData)); err != nil {
		return "", err
	}
	bin := "sous"
	if runtime.GOOS == "windows" {
		bin = "sous.exe"
	}
	exeData, err := extract(data, name, bin)
	if err != nil {
		return "", err
	}
	if err := Replace(exe, exeData); err != nil {
		return "", err
	}
	return rel.Tag, nil
}

func (c Client) download(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载 %s：%s", filepath.Base(u), resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDownload {
		return nil, errors.New("安装包太大")
	}
	return data, nil
}

func verify(data []byte, name, sums string) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	for _, line := range strings.Split(sums, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			if !strings.EqualFold(f[0], got) {
				return fmt.Errorf("%s 的校验和不对，可能下载损坏或被篡改，不安装", name)
			}
			return nil
		}
	}
	return fmt.Errorf("checksums.txt 里没有 %s", name)
}

// extract returns the file named bin from a .tar.gz or .zip archive.
func extract(data []byte, name, bin string) ([]byte, error) {
	if strings.HasSuffix(name, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == bin && !f.FileInfo().IsDir() {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, maxDownload))
			}
		}
		return nil, fmt.Errorf("%s 里没有 %s", name, bin)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("%s 里没有 %s", name, bin)
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(h.Name) == bin && h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, maxDownload))
		}
	}
}

// Replace swaps the executable at exe for data. On Unix the new file is
// renamed over the old one (a running process keeps its open copy). On
// Windows a running executable cannot be overwritten but can be renamed,
// so the old one moves to exe.old first; CleanOld removes it later.
func Replace(exe string, data []byte) error {
	info, err := os.Stat(exe)
	if err != nil {
		return err
	}
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, data, info.Mode().Perm()|0o100); err != nil {
		return fmt.Errorf("写入新版本：%w", err)
	}
	if runtime.GOOS == "windows" {
		old := exe + ".old"
		os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			os.Remove(tmp)
			return err
		}
		if err := os.Rename(tmp, exe); err != nil {
			os.Rename(old, exe)
			return err
		}
		return nil
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// CleanOld removes what Replace left behind on Windows.
func CleanOld(exe string) { os.Remove(exe + ".old") }
