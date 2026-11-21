package localrun

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Report has no host paths or authorization tokens. Guest names are data in
// the manifest, never paths when writing files to the host.
type Report struct {
	Version  int      `json:"version"`
	RunID    string   `json:"run_id"`
	ExitCode int      `json:"exit_code"`
	Imported int      `json:"imported_files"`
	Skipped  int      `json:"excluded_entries"`
	Changes  []Change `json:"changes"`
}

type Change struct {
	Path       string `json:"path"`
	Operation  string `json:"operation"`
	Before     string `json:"before_sha256,omitempty"`
	After      string `json:"after_sha256,omitempty"`
	BeforeFile string `json:"before_artifact,omitempty"`
	AfterFile  string `json:"after_artifact,omitempty"`
}

func writeReport(parent string, id string, before, after snapshot, exit int, log []byte) (string, error) {
	root, err := os.OpenRoot(parent)
	if err != nil {
		return "", err
	}
	defer root.Close()
	return writeReportUnder(root, id, before, after, exit, log)
}

func writeReportUnder(parent *os.Root, id string, before, after snapshot, exit int, log []byte) (string, error) {
	out := "sous-result-" + strings.ToLower(rand.Text()[:12])
	if err := parent.Mkdir(out, 0o700); err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			_ = parent.RemoveAll(out)
		}
	}()
	root, err := parent.OpenRoot(out)
	if err != nil {
		return "", err
	}
	defer root.Close()
	write := func(name string, data []byte) error {
		f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return errors.Join(err, f.Close())
	}
	all := make(map[string]struct{}, len(before.files)+len(after.files))
	for k := range before.files {
		all[k] = struct{}{}
	}
	for k := range after.files {
		all[k] = struct{}{}
	}
	paths := make([]string, 0, len(all))
	for k := range all {
		paths = append(paths, k)
	}
	slices.Sort(paths)
	report := Report{Version: 1, RunID: id, ExitCode: exit, Imported: len(before.files), Skipped: before.skipped, Changes: []Change{}}
	for _, p := range paths {
		old, had := before.files[p]
		fresh, has := after.files[p]
		if had && has && bytes.Equal(old, fresh) {
			continue
		}
		c := Change{Path: p, Operation: "modified"}
		switch {
		case !had:
			c.Operation = "added"
		case !has:
			c.Operation = "removed"
		}
		n := len(report.Changes) + 1
		if had {
			c.Before, c.BeforeFile = digest(old), fmt.Sprintf("change-%04d-before", n)
			if err := write(c.BeforeFile, old); err != nil {
				return "", err
			}
		}
		if has {
			c.After, c.AfterFile = digest(fresh), fmt.Sprintf("change-%04d-after", n)
			if err := write(c.AfterFile, fresh); err != nil {
				return "", err
			}
		}
		report.Changes = append(report.Changes, c)
	}
	if err := write("guest.log", log); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	// The manifest is the completion marker. It is never published if guest
	// cleanup or copying any artifact failed.
	data = append(data, '\n')
	if before.binding != nil {
		local := *before.binding
		local.Revision = digest(data)
		state, err := json.Marshal(local)
		if err != nil {
			return "", err
		}
		if err := write(".local-authorization.json", state); err != nil {
			return "", err
		}
	}
	if err := write("manifest.pending", data); err != nil {
		return "", err
	}
	if err := root.Rename("manifest.pending", "manifest.json"); err != nil {
		return "", err
	}
	ok = true
	return filepath.Abs(filepath.Join(parent.Name(), out))
}
