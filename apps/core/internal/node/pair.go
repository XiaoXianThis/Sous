package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// Pair trades a pairing code for a device token and saves the config.
func Pair(ctx context.Context, root, hubURL, code, name string) (Config, error) {
	hub, err := NormalizeHubURL(hubURL)
	if err != nil {
		return Config{}, err
	}
	host, _ := os.Hostname()
	if name == "" {
		name = host
	}
	body, _ := json.Marshal(proto.PairRequest{Code: code, Name: name, OS: runtime.GOOS, Arch: runtime.GOARCH, Hostname: host})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hub+"/api/v1/node/pair", bytes.NewReader(body))
	if err != nil {
		return Config{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Config{}, fmt.Errorf("连接 Hub 失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		return Config{}, fmt.Errorf("配对失败（HTTP %d）：%s", resp.StatusCode, e.Error.Message)
	}
	var pr proto.PairResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return Config{}, err
	}
	c := Config{HubURL: hub, NodeID: pr.NodeID, Name: name, Token: pr.Token, Concurrency: 2}
	if old, err := LoadConfig(root); err == nil {
		c.Agents = old.Agents // keep local agent overrides across re-pairing
		c.Concurrency = old.Concurrency
	}
	return c, SaveConfig(root, c)
}
