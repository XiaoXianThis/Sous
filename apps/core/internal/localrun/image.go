//go:build cgo && ((darwin && arm64) || (linux && (amd64 || arm64) && sous_linux_worker))

package localrun

import (
	"archive/tar"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
	"golang.org/x/sys/unix"
)

const image = "registry-1.docker.io/library/alpine@" + imageDigest

func verifyImageArchive(r io.Reader) (map[string][]byte, error) {
	reader := tar.NewReader(io.LimitReader(r, 16<<20))
	blobs := make(map[string][]byte, len(imageBlobs))
	for n := 0; ; n++ {
		h, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if n >= 128 {
			return nil, errors.New("OCI 归档含有过多条目")
		}
		name := strings.TrimPrefix(h.Name, "./")
		size, needed := imageBlobs[name]
		if !needed {
			continue
		}
		if h.Typeflag != tar.TypeReg || h.Size != int64(size) || blobs[name] != nil {
			return nil, errors.New("固定 OCI 镜像条目无效或重复")
		}
		data, err := io.ReadAll(io.LimitReader(reader, int64(size)+1))
		if err != nil {
			return nil, err
		}
		if len(data) != size || digest(data) != strings.TrimPrefix(name, "blobs/sha256/") {
			return nil, errors.New("固定 OCI 镜像内容校验失败")
		}
		blobs[name] = data
	}
	if len(blobs) != len(imageBlobs) {
		return nil, errors.New("OCI 归档中缺少当前运行器要求的固定镜像或镜像层")
	}
	return blobs, nil
}

func importImage(ctx context.Context, home, archive string) error {
	parent, name, err := openParent(archive)
	if err != nil {
		return err
	}
	defer parent.Close()
	f, _, err := openChecked(int(parent.Fd()), name)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := verifyImageArchive(f)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := completeLayout(b); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(home, ".sous-image-*.tar")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := errors.Join(writeImageArchive(tmp, b), tmp.Close()); err != nil {
		return err
	}
	if _, err := msb.Image.Load(ctx, tmp.Name(), image); err != nil {
		return fmt.Errorf("无法导入固定 OCI 镜像：%w", err)
	}
	return verifyCachedImage(ctx)
}

func verifyCachedImage(ctx context.Context) error {
	cached, err := msb.Image.Get(ctx, image)
	if err != nil || cached.ManifestDigest() != imageDigest || cached.Architecture() != runtime.GOARCH || cached.OS() != "linux" {
		return errors.New("镜像缓存不能确认此架构的固定 Linux 镜像")
	}
	return nil
}

func completeLayout(b map[string][]byte) error {
	manifest, err := json.Marshal(map[string]any{"schemaVersion": 2, "manifests": []any{map[string]any{
		"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": imageDigest, "size": imageBlobs["blobs/sha256/"+strings.TrimPrefix(imageDigest, "sha256:")],
	}}})
	if err != nil {
		return err
	}
	b["index.json"] = manifest
	b["oci-layout"] = []byte(`{"imageLayoutVersion":"1.0.0"}`)
	return nil
}

// DownloadImageArchive deliberately runs as a separate owner command. It does
// not open a workspace or read credentials, does not install VM software, and
// writes only a new destination the owner designated. All image bytes are
// checked against the same pins before publication.
func DownloadImageArchive(ctx context.Context, destination string) error {
	parent, filename, err := openParent(destination)
	if err != nil {
		return err
	}
	defer parent.Close()
	if !safeName(filename) {
		return errors.New("请选择有效的归档文件名")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.User != nil || len(via) > 4 {
			return errors.New("拒绝非 HTTPS 或过多的镜像重定向")
		}
		req.Header.Del("Authorization")
		return nil
	}}
	fetch := func(uri, token string, limit int) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
		if err != nil {
			return nil, err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/octet-stream, application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, errors.New("无法连接固定镜像源")
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("镜像源返回 HTTP %d，请稍后重试", resp.StatusCode)
		}
		content, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
		if err != nil || len(content) > limit {
			return nil, errors.New("镜像下载中断或超过预期大小")
		}
		return content, nil
	}
	raw, err := fetch("https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/alpine:pull", "", 32<<10)
	if err != nil {
		return err
	}
	var auth struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &auth) != nil || auth.Token == "" {
		return errors.New("无法获取公开镜像的匿名访问许可")
	}
	const base = "https://registry-1.docker.io/v2/library/alpine/"
	blobs := make(map[string][]byte, len(imageBlobs)+2)
	for name, size := range imageBlobs {
		sha := strings.TrimPrefix(name, "blobs/sha256/")
		url := base + "blobs/sha256:" + sha
		if "sha256:"+sha == imageDigest {
			url = base + "manifests/" + imageDigest
		}
		content, err := fetch(url, auth.Token, size)
		if err != nil {
			return err
		}
		if len(content) != size || digest(content) != sha {
			return errors.New("下载内容不匹配固定 OCI 镜像")
		}
		blobs[name] = content
	}
	if err := completeLayout(blobs); err != nil {
		return err
	}
	var archive strings.Builder
	if err := writeImageArchive(&archive, blobs); err != nil {
		return err
	}
	fd := int(parent.Fd())
	tmp := ".sous-provision-" + strings.ToLower(rand.Text()[:16])
	if err := createNew(fd, tmp, []byte(archive.String())); err != nil {
		return err
	}
	defer unix.Unlinkat(fd, tmp, 0)
	return publishNew(fd, tmp, filename)
}

func writeImageArchive(out io.Writer, blobs map[string][]byte) (err error) {
	tw := tar.NewWriter(out)
	defer func() { err = errors.Join(err, tw.Close()) }()
	names := make([]string, 0, len(blobs))
	for name := range blobs {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		data := blobs[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), Format: tar.FormatUSTAR, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	return nil
}
