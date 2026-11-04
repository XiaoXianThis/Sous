package nodehub

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
)

// archiveBatch bounds how many runs one Archive call compresses.
const archiveBatch = 200

// ArchiveDir holds the compressed logs and diffs of old runs.
func (h *Hub) ArchiveDir() string { return filepath.Join(h.runsDir, "archive") }

// Archive compresses the log and diff of every run whose task closed
// before cutoff into runs/archive/<id>.{log,diff}.gz and removes the
// originals (design §4, data retention). The events stay. It returns how
// many runs it archived.
func (h *Hub) Archive(ctx context.Context, cutoff time.Time) (int, error) {
	runs, err := h.log.Store.ArchivableRuns(ctx, cutoff, archiveBatch)
	if err != nil {
		return 0, err
	}
	if len(runs) == 0 {
		return 0, nil
	}
	if err := os.MkdirAll(h.ArchiveDir(), 0o700); err != nil {
		return 0, err
	}
	n := 0
	for _, r := range runs {
		if ctx.Err() != nil {
			break
		}
		for _, src := range []string{h.LogPath(r.ID), h.DiffPath(r.ID)} {
			if err := gzipFile(src, filepath.Join(h.ArchiveDir(), filepath.Base(src)+".gz")); err != nil {
				return n, err
			}
		}
		ev := event.New(event.RunArchived, event.ActorSystem, r.ThreadID, struct{}{})
		ev.RunID, ev.TaskID = r.ID, r.TaskID
		if _, err := h.log.Append(ctx, ev); err != nil {
			return n, err
		}
		n++
	}
	slog.Info("nodehub: archived old runs", "runs", n, "before", cutoff.Format(time.DateOnly))
	return n, nil
}

// gzipFile compresses src into dst and removes src. A missing src is fine.
func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(out)
	_, err = io.Copy(zw, in)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	in.Close()
	return os.Remove(src)
}

// openRunFile opens a run's log or diff, from the archive if it was moved there.
func (h *Hub) openRunFile(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return f, err
	}
	gz, err := os.Open(filepath.Join(h.ArchiveDir(), filepath.Base(path)+".gz"))
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(gz)
	if err != nil {
		gz.Close()
		return nil, err
	}
	return readCloser{zr, gz}, nil
}

type readCloser struct {
	*gzip.Reader
	f *os.File
}

func (r readCloser) Close() error {
	r.Reader.Close()
	return r.f.Close()
}

// ReadRunFile reads a run's log or diff wherever it is; missing is empty.
func (h *Hub) ReadRunFile(path string) ([]byte, error) {
	f, err := h.openRunFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
