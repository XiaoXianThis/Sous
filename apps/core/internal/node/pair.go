package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tunnel"
)

// PairOptions are optional settings written into node.json when pairing.
type PairOptions struct {
	Role       string   // dev / server
	Workspaces []string // directories searched for repositories
}

// PairRelay pairs this machine with the user (decision 0030): the pairing
// string came from the page by the user's hands; the relay only carries the
// exchange, which both sides authenticate with the string's secret.
func PairRelay(ctx context.Context, root, relayURL, code, name string, opt PairOptions) (Config, error) {
	relayURL = strings.TrimRight(strings.TrimSpace(relayURL), "/")
	pc, err := trust.ParsePairCode(code)
	if err != nil {
		return Config{}, err
	}
	if time.Now().After(pc.Exp) {
		return Config{}, errors.New("配对串已经过期，请在页面上重新生成")
	}
	if !bytes.Equal(pc.RelayHash, trust.RelayHash(relayURL)) {
		return Config{}, errors.New("这个配对串不是为这个中继地址生成的，请检查地址")
	}
	sign, err := trust.NewSignKey()
	if err != nil {
		return Config{}, err
	}
	kem, err := trust.NewKEMKey()
	if err != nil {
		return Config{}, err
	}
	host, _ := os.Hostname()
	if name == "" {
		name = host
	}
	role := ""
	if opt.Role == "server" {
		role = "server"
	}
	id := trust.NodeIDOf(sign.Public())
	hello := trust.PairHello{T: "pair.hello", Node: id, Name: name, OS: runtime.GOOS, Arch: runtime.GOARCH, Hostname: host, Role: role,
		SignPub: trust.B64.EncodeToString(sign.Public()), KEMPub: trust.B64.EncodeToString(kem.Public()), KeyStorage: "software"}
	body, _ := json.Marshal(hello)
	room := trust.PairRoom(pc.Secret)
	r, err := tunnel.JoinRoom(ctx, relayURL, room)
	if err != nil {
		return Config{}, fmt.Errorf("进入配对房间失败（页面上的“添加机器”窗口要保持打开）：%w", err)
	}
	defer r.Close()
	if err := r.Send(ctx, room, trust.PairMsg{Body: trust.B64.EncodeToString(body), MAC: trust.PairMAC(pc.Secret, "node", body)}); err != nil {
		return Config{}, err
	}
	fmt.Fprintf(os.Stderr, "已发送配对请求，请在页面上确认添加“%s”（需要刷一次 Face ID）……\n", name)
	wctx, cancel := context.WithTimeout(ctx, trust.PairTTL)
	defer cancel()
	m, err := r.Receive(wctx)
	if err != nil {
		return Config{}, fmt.Errorf("等待页面确认：%w", err)
	}
	wb, err := trust.B64.DecodeString(m.Body)
	if err != nil || !trust.CheckPairMAC(pc.Secret, "app", wb, m.MAC) {
		return Config{}, errors.New("页面的应答没有通过校验，配对中止")
	}
	var w trust.PairWelcome
	if err := json.Unmarshal(wb, &w); err != nil || w.T != "pair.welcome" {
		return Config{}, errors.New("页面的应答格式不对")
	}
	rp, _ := w.Roster.Doc.Payload()
	var peek trust.Roster
	json.Unmarshal(rp, &peek)
	idPub, err := trust.B64.DecodeString(peek.User.SignPub)
	if err != nil || !bytes.Equal(trust.Fingerprint(idPub), pc.IdentityFP) {
		return Config{}, errors.New("应答里的身份和配对串不符，配对中止")
	}
	roster, err := trust.OpenRoster(idPub, w.Roster.Doc)
	if err != nil {
		return Config{}, err
	}
	n, ok := roster.Node(id)
	if !ok || n.SignPub != hello.SignPub || n.KEMPub != hello.KEMPub {
		return Config{}, errors.New("名册里没有这台机器，配对中止")
	}
	if err := saveKeys(root, sign, kem); err != nil {
		return Config{}, err
	}
	// Seed the tunnel's state with this roster so the first connection
	// already knows who is who.
	st, _ := json.Marshal(map[string]any{"roster": w.Roster})
	os.WriteFile(filepath.Join(trustDir(root), "tunnel.json"), st, 0o600)
	os.Remove(filepath.Join(trustDir(root), "frozen.json"))
	c := Config{RelayURL: relayURL, Identity: trust.B64.EncodeToString(idPub), NodeID: id, Name: name, Concurrency: 2}
	if old, err := LoadConfig(root); err == nil {
		c.Agents, c.Concurrency, c.Role, c.Workspaces = old.Agents, old.Concurrency, old.Role, old.Workspaces
		c.AgentOptions, c.AgentEnv, c.Policy, c.RemoteAdmin, c.RemoteShell = old.AgentOptions, old.AgentEnv, old.Policy, old.RemoteAdmin, old.RemoteShell
		c.DefaultAgent, c.DisabledAgents, c.CustomAgents, c.AllowOpen = old.DefaultAgent, old.DisabledAgents, old.CustomAgents, old.AllowOpen
		if c.Concurrency <= 0 {
			c.Concurrency = 2
		}
	}
	if opt.Role == "server" {
		c.Role = "server"
	} else if opt.Role == "dev" {
		c.Role = ""
	}
	if len(opt.Workspaces) > 0 {
		c.Workspaces = opt.Workspaces
	}
	return c, SaveConfig(root, c)
}

// ResetTrust forgets the pairing: keys, roster, audit stay only as a backup.
func ResetTrust(root string) error {
	dir := trustDir(root)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	return os.Rename(dir, dir+".reset-"+time.Now().Format("20060102-150405"))
}
