package cloudcontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Driver invokes only an operator-selected, trusted provider executable,
// without a shell. Neither a workspace owner nor the Hub supplies that path.
// Request and response bytes must never be logged (bootstrap is a credential).
type Driver struct{ path string }

func NewDriver(path string) (*Driver, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("provider driver needs a clean absolute executable path")
	}
	if st, err := os.Lstat(path); err != nil || !st.Mode().IsRegular() {
		return nil, errors.New("provider driver needs a regular executable, not a link")
	}
	return &Driver{path}, nil
}

func (p *Driver) Reconcile(ctx context.Context, req Request) (string, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	ctx, end := context.WithTimeout(ctx, 30*time.Second)
	defer end()
	cmd := exec.CommandContext(ctx, p.path)
	cmd.WaitDelay = time.Second
	var stdout boundedOutput
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(raw), &stdout, io.Discard
	if err := cmd.Run(); err != nil {
		return "", errors.New("provider driver did not confirm operation")
	}
	var reply struct {
		State string `json:"state"`
	}
	dec := json.NewDecoder(&stdout.Buffer)
	dec.DisallowUnknownFields()
	if dec.Decode(&reply) != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		return "", errors.New("provider driver returned invalid response")
	}
	return reply.State, nil
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > 4096-b.Len() {
		return 0, errors.New("provider response exceeded limit")
	}
	return b.Buffer.Write(p)
}
