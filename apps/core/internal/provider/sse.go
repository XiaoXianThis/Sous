package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// sseEvent is one server-sent event.
type sseEvent struct {
	Event string
	Data  string
}

// readSSE calls fn for each event in r until EOF, a "[DONE]" data line, or fn
// returns an error.
func readSSE(r io.Reader, fn func(sseEvent) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	var ev sseEvent
	var data []string
	flush := func() error {
		if len(data) == 0 {
			ev = sseEvent{}
			return nil
		}
		ev.Data = strings.Join(data, "\n")
		data = data[:0]
		if ev.Data == "[DONE]" {
			return io.EOF
		}
		err := fn(ev)
		ev = sseEvent{}
		return err
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event:"):
			ev.Event = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(line[len("data:"):], " "))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

// postStream sends a JSON body and returns the streaming response, or an
// *APIError for non-2xx answers.
func postStream(ctx context.Context, client *http.Client, url string, headers map[string]string, body any) (*http.Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		return nil, &APIError{Status: resp.StatusCode, Body: string(b)}
	}
	return resp, nil
}

func emptySchema(s json.RawMessage) json.RawMessage {
	if len(s) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return s
}

func argsOrEmpty(a json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(a)) == 0 {
		return json.RawMessage(`{}`)
	}
	return a
}
