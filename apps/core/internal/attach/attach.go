// Package attach keeps the files a user sends with a message: pictures the
// model can see and files whose text it can read. Files live in a directory
// of the Hub, named by an unguessable id; the message only refers to them.
package attach

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
)

const (
	// MaxSize caps one file.
	MaxSize = 20 << 20
	// MaxPerMessage caps the files of one message.
	MaxPerMessage = 8
	// MaxTextBytes is how much of a text file the model reads.
	MaxTextBytes = 100 << 10
)

var (
	ErrTooLarge = errors.New("文件太大")
	ErrNotFound = errors.New("附件不存在")
	idPattern   = regexp.MustCompile(`^[0-9A-Za-z]{26}$`)
)

type Store struct{ dir string }

func Open(dir string) *Store { return &Store{dir: dir} }

// IsImage reports whether mime is a picture models accept.
func IsImage(mime string) bool {
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}

// IsText reports whether the model reads the file as text.
func IsText(mime string) bool { return strings.HasPrefix(mime, "text/") }

// Save stores r. The type comes from the bytes, never from the client.
func (s *Store) Save(r io.Reader, name string) (event.Attachment, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return event.Attachment{}, err
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if err != nil {
		return event.Attachment{}, err
	}
	if len(data) > MaxSize {
		return event.Attachment{}, ErrTooLarge
	}
	if len(data) == 0 {
		return event.Attachment{}, errors.New("文件是空的")
	}
	a := event.Attachment{ID: ids.New(), Name: cleanName(name), Mime: sniff(data), Size: int64(len(data))}
	if err := os.WriteFile(s.path(a.ID), data, 0o600); err != nil {
		return event.Attachment{}, err
	}
	meta, _ := json.Marshal(a)
	if err := os.WriteFile(s.path(a.ID)+".json", meta, 0o600); err != nil {
		_ = os.Remove(s.path(a.ID))
		return event.Attachment{}, err
	}
	return a, nil
}

// Meta returns what Save recorded about id.
func (s *Store) Meta(id string) (event.Attachment, error) {
	var a event.Attachment
	if !idPattern.MatchString(id) {
		return a, ErrNotFound
	}
	raw, err := os.ReadFile(s.path(id) + ".json")
	if err != nil {
		return a, ErrNotFound
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.ID != id {
		return a, ErrNotFound
	}
	return a, nil
}

// Open opens the stored bytes of id.
func (s *Store) Open(id string) (*os.File, error) {
	if !idPattern.MatchString(id) {
		return nil, ErrNotFound
	}
	f, err := os.Open(s.path(id))
	if err != nil {
		return nil, ErrNotFound
	}
	return f, nil
}

// Read returns the bytes of a; text files are cut at MaxTextBytes by the caller.
func (s *Store) Read(a event.Attachment) ([]byte, error) {
	if !idPattern.MatchString(a.ID) {
		return nil, ErrNotFound
	}
	return os.ReadFile(s.path(a.ID))
}

// Resolve checks that every id exists and returns the stored metadata, so a
// client cannot claim a different name or type.
func (s *Store) Resolve(ids []string) ([]event.Attachment, error) {
	if len(ids) > MaxPerMessage {
		return nil, fmt.Errorf("一条消息最多带 %d 个附件", MaxPerMessage)
	}
	out := make([]event.Attachment, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		a, err := s.Meta(id)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id) }

var control = regexp.MustCompile(`[\x00-\x1f\x7f/\\]`)

func cleanName(name string) string {
	name = control.ReplaceAllString(strings.TrimSpace(name), "_")
	if r := []rune(name); len(r) > 120 {
		name = string(r[len(r)-120:])
	}
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	return name
}

// sniff names the type of data. Only the pictures models accept keep their
// type; text is text/plain; anything else (SVG and HTML included) is opaque
// so it is never shown inline.
func sniff(data []byte) string {
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	mime := http.DetectContentType(head)
	if i := strings.Index(mime, ";"); i >= 0 {
		mime = mime[:i]
	}
	switch {
	case IsImage(mime):
		return mime
	case mime == "application/pdf":
		return mime
	case isUTF8Text(data):
		return "text/plain"
	}
	return "application/octet-stream"
}

func isUTF8Text(data []byte) bool {
	if len(data) > MaxTextBytes {
		data = data[:MaxTextBytes]
	}
	// A cut can split a character: drop the broken tail before checking.
	for i := 0; i < 3 && len(data) > 0 && !utf8.Valid(data); i++ {
		data = data[:len(data)-1]
	}
	if !utf8.Valid(data) {
		return false
	}
	for _, b := range data {
		if b == 0 {
			return false
		}
	}
	return true
}
