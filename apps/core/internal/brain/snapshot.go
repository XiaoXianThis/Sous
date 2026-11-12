package brain

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
)

// The brain's state travels between brain machines as an encrypted
// snapshot on the relay (design §8.3): a tar.gz of the Hub's data
// directory, cut into chunks sealed with the domain key, listed in a
// manifest the holder signs. Two slots alternate so the manifest in force
// always points at complete chunks.

const chunkSize = 4 << 20

func localSeqPath(root string) string { return filepath.Join(Dir(root), "snapshot.seq") }

func readLocalSeq(root string) int64 {
	raw, err := os.ReadFile(localSeqPath(root))
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	return n
}

func writeLocalSeq(root string, n int64) {
	os.WriteFile(localSeqPath(root), []byte(strconv.FormatInt(n, 10)), 0o600)
}

// domainKey returns the newest epoch and its key for this machine.
func (b *Brain) domainKey(epoch int64) (int64, []byte, error) {
	d := b.C.Domain()
	if d == nil {
		return 0, nil, errors.New("还没有数据密钥：在页面上指定大脑机器时会生成")
	}
	if epoch == 0 {
		var err error
		if epoch, err = d.Current(); err != nil {
			return 0, nil, err
		}
	}
	key, err := d.Key(epoch, b.NodeID, b.C.KEMKey())
	return epoch, key, err
}

func (b *Brain) lastEvent(ctx context.Context) int64 {
	h := b.Hub()
	if h == nil {
		return 0
	}
	var n int64
	h.Store.DB().QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM events`).Scan(&n)
	return n
}

// snapshot uploads the state when it changed (or always when force).
func (b *Brain) snapshot(ctx context.Context, force bool) error {
	h := b.Hub()
	if h == nil {
		return errNotLeading
	}
	last := b.lastEvent(ctx)
	b.mu.Lock()
	unchanged := last == b.lastEv && b.snapSeq > 0
	b.mu.Unlock()
	if unchanged && !force {
		return nil
	}
	epoch, key, err := b.domainKey(0)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(Dir(b.Root), "snap-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := h.Store.Backup(ctx, filepath.Join(tmp, "hub.db")); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	var buf bytes.Buffer
	if err := packDir(&buf, DataDir(b.Root), filepath.Join(tmp, "hub.db")); err != nil {
		return err
	}
	seq := b.snapSeq
	if m, ok := b.C.Snapshot(); ok && m.Seq > seq {
		seq = m.Seq
	}
	seq++
	data := buf.Bytes()
	var sums []string
	for i := 0; i*chunkSize < len(data) || i == 0; i++ {
		end := min((i+1)*chunkSize, len(data))
		ct, err := trust.SymSeal(key, trust.SnapshotAAD(seq, i), data[i*chunkSize:end])
		if err != nil {
			return err
		}
		sum := sha256.Sum256(ct)
		sums = append(sums, trust.B64.EncodeToString(sum[:]))
		if err := b.C.BlobPut(ctx, chunkName(seq, i), ct); err != nil {
			return err
		}
		if end == len(data) {
			break
		}
	}
	b.mu.Lock()
	term := b.term
	b.mu.Unlock()
	m := trust.SnapshotManifest{T: trust.TSnapshot, Holder: b.NodeID, Term: term, Seq: seq, Epoch: epoch, IAT: time.Now().Unix(), Size: int64(len(data)), Chunks: sums}
	doc, err := trust.Sign(b.C.SignKey(), trust.TSnapshot, m)
	if err != nil {
		return err
	}
	if err := b.C.Put(ctx, "snapshot", doc); err != nil {
		return err
	}
	b.mu.Lock()
	b.snapSeq, b.lastEv = seq, last
	b.mu.Unlock()
	writeLocalSeq(b.Root, seq)
	slog.Debug("brain: snapshot uploaded", "seq", seq, "bytes", len(data))
	return nil
}

func chunkName(seq int64, i int) string {
	return fmt.Sprintf("snapshot/%d/%d", seq%2, i)
}

// restore replaces the local data with the newest snapshot when it is newer.
func (b *Brain) restore(ctx context.Context) error {
	m, ok := b.C.Snapshot()
	if !ok || m.Seq <= b.snapSeq {
		return nil // nothing newer than what this machine has
	}
	_, key, err := b.domainKey(m.Epoch)
	if err != nil {
		return err
	}
	var data []byte
	for i, want := range m.Chunks {
		ct, err := b.C.BlobGet(ctx, chunkName(m.Seq, i))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(ct)
		if trust.B64.EncodeToString(sum[:]) != want {
			return fmt.Errorf("快照第 %d 块和清单不符", i)
		}
		pt, err := trust.SymOpen(key, trust.SnapshotAAD(m.Seq, i), ct)
		if err != nil {
			return fmt.Errorf("快照第 %d 块解不开：%w", i, err)
		}
		data = append(data, pt...)
	}
	if int64(len(data)) != m.Size {
		return errors.New("快照大小和清单不符")
	}
	tmp := DataDir(b.Root) + ".restoring"
	os.RemoveAll(tmp)
	if err := unpack(bytes.NewReader(data), tmp); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if _, err := os.Stat(DataDir(b.Root)); err == nil {
		old := DataDir(b.Root) + ".before-" + time.Now().Format("20060102-150405")
		if err := os.Rename(DataDir(b.Root), old); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, DataDir(b.Root)); err != nil {
		return err
	}
	b.snapSeq = m.Seq
	writeLocalSeq(b.Root, m.Seq)
	slog.Info("brain: restored the newest state", "seq", m.Seq, "from", m.Holder)
	return nil
}

// packDir writes dir as tar.gz, with db in place of hub.db and without the
// live database files.
func packDir(w io.Writer, dir, db string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	add := func(name, path string) error {
		fi, err := os.Stat(path)
		if err != nil {
			return err
		}
		hdr, _ := tar.FileInfoHeader(fi, "")
		hdr.Name = filepath.ToSlash(name)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	}
	if err := add("hub.db", db); err != nil {
		return err
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if strings.HasPrefix(rel, "hub.db") {
			return nil
		}
		return add(rel, p)
	})
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func unpack(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(hdr.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("快照里有不安全的路径 %q", hdr.Name)
		}
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, io.LimitReader(tr, 2<<30)); err != nil {
			f.Close()
			return err
		}
		f.Close()
	}
}
