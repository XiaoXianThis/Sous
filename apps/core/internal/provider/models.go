package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// DefaultBaseURL is the usual endpoint of a kind, for a provider added
// without one.
func DefaultBaseURL(kind string) string {
	switch kind {
	case "anthropic-messages":
		return "https://api.anthropic.com"
	case "openai-responses", "openai-chat":
		return "https://api.openai.com/v1"
	}
	return ""
}

// ListModels asks a provider which models it offers (GET /models, or
// /v1/models for anthropic-messages). The mock kind lists its fixed ones.
func ListModels(ctx context.Context, cfg Config) ([]string, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL(cfg.Kind)
	}
	var url string
	hdr := map[string]string{}
	switch cfg.Kind {
	case "mock":
		return []string{"echo", "verdict"}, nil
	case "anthropic-messages":
		url = base + "/v1/models?limit=100"
		hdr["x-api-key"], hdr["anthropic-version"] = cfg.APIKey, "2023-06-01"
	case "openai-chat", "openai-responses":
		url = base + "/models"
		hdr["Authorization"] = "Bearer " + cfg.APIKey
	default:
		return nil, fmt.Errorf("unknown provider kind %q", cfg.Kind)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		return nil, &APIError{Status: resp.StatusCode, Body: string(raw)}
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("unexpected answer from %s: %w", url, err)
	}
	out := []string{}
	for _, m := range body.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	sort.Strings(out)
	return out, nil
}
