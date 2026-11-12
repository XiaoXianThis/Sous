package brain

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
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
// snapshot on the relay (design §8.3): a tar of the Hub's data directory,
// cut into content-defined chunks (cdc.go). Each chunk is gzipped, sealed
// with the domain key and stored under a name keyed by its content, so an
// upload sends only the chunks the relay does not have yet. The manifest the
// holder signs lists the chunks in order; the relay keeps the chunks of the
// newest two manifests and drops the rest.
//
// Manifests before v2 (0.3.0-alpha1) used two alternating slots of fixed
// chunks of one tar.gz; restore still reads them.

const snapshotV2 = 2

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
	b.mu.Lock()
	seq := b.snapSeq
	onRelay := b.onRelay
	prev := b.snapNames
	b.mu.Unlock()
	if m, ok := b.C.Snapshot(); ok && m.Seq > seq {
		seq = m.Seq
	}
	if m, ok := b.C.Snapshot(); ok && m.V >= snapshotV2 && m.Epoch == epoch && onRelay == nil {
		// Another brain (or this one before a restart) left these behind.
		onRelay, prev = setOf(m.Names), m.Names
	}
	seq++
	data := buf.Bytes()
	var names, sums []string
	sent, sentBytes := 0, 0
	for _, chunk := range cdcSplit(data) {
		name := chunkName(key, chunk)
		ct, err := sealChunk(key, name, chunk)
		if err != nil {
			return err
		}
		if !onRelay[name] {
			if err := b.C.BlobPut(ctx, name, ct); err != nil {
				return err
			}
			sent++
			sentBytes += len(ct)
		}
		sum := sha256.Sum256(ct)
		names = append(names, name)
		sums = append(sums, trust.B64.EncodeToString(sum[:]))
	}
	b.mu.Lock()
	term := b.term
	b.mu.Unlock()
	m := trust.SnapshotManifest{T: trust.TSnapshot, V: snapshotV2, Holder: b.NodeID, Term: term, Seq: seq, Epoch: epoch, IAT: time.Now().Unix(), Size: int64(len(data)), Chunks: sums, Names: names}
	doc, err := trust.Sign(b.C.SignKey(), trust.TSnapshot, m)
	if err != nil {
		return err
	}
	if err := b.C.Put(ctx, "snapshot", doc); err != nil {
		return err
	}
	// Keep this manifest's chunks and the previous one's (a brain may be
	// restoring from it right now); everything else under snapshot/ goes.
	if err := b.C.BlobKeep(ctx, "snapshot/", append(append([]string{}, names...), prev...)); err != nil {
		slog.Warn("brain: dropping old snapshot chunks failed", "err", err)
	}
	b.mu.Lock()
	b.onRelay, b.snapNames = setOf(names), names
	b.mu.Unlock()
	slog.Debug("brain: snapshot uploaded", "seq", seq, "bytes", len(data), "chunks", len(names), "sent", sent, "sent_bytes", sentBytes)
	b.mu.Lock()
	b.snapSeq, b.lastEv = seq, last
	b.mu.Unlock()
	writeLocalSeq(b.Root, seq)
	return nil
}

// slotChunkName is where 0.3.0-alpha1 stored chunk i of snapshot seq.
func slotChunkName(seq int64, i int) string {
	return fmt.Sprintf("snapshot/%d/%d", seq%2, i)
}

// chunkName names a chunk by a keyed hash of its plaintext: equal chunks
// get equal names, and the relay cannot test a guess without the key.
func chunkName(key, chunk []byte) string {
	nk := sha256.Sum256(append([]byte("sous/v1/snapshot-name\x00"), key...))
	mac := hmac.New(sha256.New, nk[:])
	mac.Write(chunk)
	return "snapshot/c/" + trust.B64.EncodeToString(mac.Sum(nil))
}

func chunkAAD(name string) []byte { return []byte("sous/v1/snapshot-chunk\x00" + name) }

func sealChunk(key []byte, name string, chunk []byte) ([]byte, error) {
	var z bytes.Buffer
	w, _ := gzip.NewWriterLevel(&z, gzip.BestSpeed)
	w.Write(chunk)
	if err := w.Close(); err != nil {
		return nil, err
	}
	return trust.SymSeal(key, chunkAAD(name), z.Bytes())
}

func openChunk(key []byte, name string, ct []byte) ([]byte, error) {
	z, err := trust.SymOpen(key, chunkAAD(name), ct)
	if err != nil {
		return nil, err
	}
	r, err := gzip.NewReader(bytes.NewReader(z))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(r, cdcMax+1))
}

func setOf(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// restore replaces the local data with the newest snapshot when it is newer.
func (b *Brain) restore(ctx context.Context) error {
	m, ok := b.C.Snapshot()
	b.mu.Lock()
	mine := b.snapSeq
	b.mu.Unlock()
	if !ok || m.Seq <= mine {
		return nil // nothing newer than what this machine has
	}
	_, key, err := b.domainKey(m.Epoch)
	if err != nil {
		return err
	}
	var data []byte
	if m.V >= snapshotV2 && len(m.Names) != len(m.Chunks) {
		return errors.New("快照清单的块名和校验和数目不符")
	}
	for i, want := range m.Chunks {
		name := slotChunkName(m.Seq, i)
		if m.V >= snapshotV2 {
			name = m.Names[i]
		}
		ct, err := b.C.BlobGet(ctx, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(ct)
		if trust.B64.EncodeToString(sum[:]) != want {
			return fmt.Errorf("快照第 %d 块和清单不符", i)
		}
		var pt []byte
		if m.V >= snapshotV2 {
			pt, err = openChunk(key, name, ct)
		} else {
			pt, err = trust.SymOpen(key, trust.SnapshotAAD(m.Seq, i), ct)
		}
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
	b.mu.Lock()
	b.snapSeq = m.Seq
	if m.V >= snapshotV2 {
		b.onRelay, b.snapNames = setOf(m.Names), m.Names
	}
	b.mu.Unlock()
	writeLocalSeq(b.Root, m.Seq)
	slog.Info("brain: restored the newest state", "seq", m.Seq, "from", m.Holder)
	return nil
}

// packDir writes dir as a plain tar (chunks are compressed one by one),
// with db in place of hub.db and without the live database files.
func packDir(w io.Writer, dir, db string) error {
	tw := tar.NewWriter(w)
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
	return tw.Close()
}

// unpack reads a plain tar, or the tar.gz of an alpha1 snapshot.
func unpack(r *bytes.Reader, dir string) error {
	var src io.Reader = r
	if head := make([]byte, 2); r.Len() >= 2 {
		r.ReadAt(head, 0)
		if head[0] == 0x1f && head[1] == 0x8b {
			gz, err := gzip.NewReader(r)
			if err != nil {
				return err
			}
			src = gz
		}
	}
	tr := tar.NewReader(src)
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
