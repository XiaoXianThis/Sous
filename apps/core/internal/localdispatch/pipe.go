package localdispatch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const PipeVersion = 1
const DesktopAuthVersion = 1

type pipeRequest struct {
	ID       uint64 `json:"id"`
	Action   string `json:"action"`
	Dispatch string `json:"dispatch_id,omitempty"`
	Lease    string `json:"lease,omitempty"`
	Outcome  string `json:"outcome,omitempty"`
	Changes  int    `json:"changes,omitempty"`
}

type pipeReply struct {
	ID    uint64 `json:"id"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// Serve is the post-readiness protocol on inherited process pipes only. It
// accepts no destinations or shell args; stdout is never a terminal log.
func (q *Queue) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	return q.ServeDesktop(ctx, in, out, nil, nil)
}

// ServeDesktop can be called on a nil Queue when only desktop auth is enabled.
func (q *Queue) ServeDesktop(ctx context.Context, in io.Reader, out io.Writer, grant func(bool) (string, error), lock func()) error {
	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 1024), 8192)
	enc := json.NewEncoder(out)
	for scan.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var r pipeRequest
		d := json.NewDecoder(bytes.NewReader(scan.Bytes()))
		d.DisallowUnknownFields()
		var extra json.RawMessage
		if d.Decode(&r) != nil || r.ID == 0 || !errors.Is(d.Decode(&extra), io.EOF) {
			return errInvalid
		}
		call, done := context.WithTimeout(ctx, 5*time.Second)
		var data any
		var err error
		switch {
		case r.Action == "desktop-login" && grant != nil && r.Dispatch == "" && r.Lease == "" && r.Changes == 0 && (r.Outcome == "native" || r.Outcome == "debug"):
			data, err = grant(r.Outcome == "native")
		case r.Action == "desktop-lock" && lock != nil && r.Dispatch == "" && r.Lease == "" && r.Outcome == "" && r.Changes == 0:
			lock()
			data = true
		case q != nil:
			data, err = q.handle(call, r)
		default:
			err = errInvalid
		}
		done()
		reply := pipeReply{ID: r.ID, Data: data}
		if err != nil {
			reply.Data = nil
			switch err {
			case errConflict, errBusy, errInvalid, errNotFound:
				reply.Error = err.Error()
			default:
				reply.Error = "无法处理本机执行状态"
			}
		}
		if err := enc.Encode(reply); err != nil {
			return err
		}
	}
	return scan.Err()
}

func (q *Queue) handle(ctx context.Context, r pipeRequest) (any, error) {
	if len(r.Dispatch) > 64 || r.Changes < 0 || r.Changes > maxChanges {
		return nil, errInvalid
	}
	switch r.Action {
	case "list":
		if r.Dispatch != "" || r.Lease != "" || r.Outcome != "" || r.Changes != 0 {
			break
		}
		return q.pending(ctx)
	case "claim":
		if r.Dispatch == "" || r.Lease != "" || r.Outcome != "" || r.Changes != 0 {
			break
		}
		return q.claim(ctx, r.Dispatch)
	case "heartbeat":
		if r.Dispatch == "" || len(r.Lease) != 64 || r.Outcome != "" || r.Changes != 0 {
			break
		}
		return q.heartbeat(ctx, r.Dispatch, r.Lease)
	case "complete":
		if r.Dispatch == "" {
			break
		}
		return q.complete(ctx, r.Dispatch, r.Lease, r.Outcome, r.Changes)
	}
	return nil, errInvalid
}
