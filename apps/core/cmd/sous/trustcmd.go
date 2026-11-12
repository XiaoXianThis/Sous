package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/brain"
	"github.com/XiaoXianThis/Sous/apps/core/internal/node"
	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
	"github.com/XiaoXianThis/Sous/apps/core/internal/update"
)

func trustCommand(root, sub string) error {
	switch sub {
	case "status":
		cfg, err := node.LoadConfig(root)
		if err != nil {
			return err
		}
		fmt.Printf("机器：%s（id %s）\n中继：%s\n", cfg.Name, cfg.NodeID, cfg.RelayURL)
		if id, err := trust.B64.DecodeString(cfg.Identity); err == nil && len(id) > 0 {
			fmt.Printf("只认这个身份：%s\n", fingerprint(id))
		}
		if s, _, err := node.LoadKeys(root); err == nil {
			fmt.Printf("本机密钥：%s（软件存储，文件权限 0600）\n", trust.KeyID(s.Public()))
		} else {
			fmt.Println("本机密钥：没有（需要重新配对）")
		}
		if raw, err := os.ReadFile(filepath.Join(root, "trust", "frozen.json")); err == nil {
			fmt.Printf("状态：已自我冻结 %s\n", strings.TrimSpace(string(raw)))
		}
		if raw, err := os.ReadFile(filepath.Join(root, "trust", "tunnel.json")); err == nil {
			var st struct {
				Roster *trust.RosterUpdate `json:"roster"`
			}
			if json.Unmarshal(raw, &st) == nil && st.Roster != nil {
				p, _ := st.Roster.Doc.Payload()
				var r trust.Roster
				json.Unmarshal(p, &r)
				fmt.Printf("名册：v%d，%d 台机器，%d 个 Passkey\n", r.V, len(r.Nodes), len(r.User.Passkeys))
				for _, n := range r.Nodes {
					tag := ""
					if n.Brain != nil {
						tag = fmt.Sprintf("（大脑，优先级 %d）", n.Brain.Priority)
					}
					if n.Frozen {
						tag += "（已冻结）"
					}
					fmt.Printf("  - %s %s%s\n", n.Name, n.ID, tag)
				}
			}
		}
		return nil
	case "reset":
		fmt.Print("这会让这台机器忘掉配对（密钥、名册），之后要重新配对。继续吗？输入 yes：")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(line) != "yes" {
			return errors.New("已取消")
		}
		if err := node.ResetTrust(root); err != nil {
			return err
		}
		os.Remove(filepath.Join(root, "node.json"))
		fmt.Println("已重置。用页面上的“添加机器”生成配对串，再运行 sous pair。")
		return nil
	}
	return fmt.Errorf("未知的子命令 %q", sub)
}

// fingerprint shows a key as groups the user can compare by eye.
func fingerprint(pub []byte) string {
	fp := trust.B64.EncodeToString(trust.Fingerprint(pub))[:24]
	return fp[:6] + "-" + fp[6:12] + "-" + fp[12:18] + "-" + fp[18:24]
}

// brainImport puts a 0.2 Hub backup in place as this brain's data. The
// next time this machine leads, it keeps this data (its snapshot counter
// is set ahead of any existing snapshot) and uploads it encrypted.
func brainImport(root, backup string) error {
	if _, err := os.Stat(backup); err != nil {
		return err
	}
	dir := brain.DataDir(root)
	if _, err := os.Stat(dir); err == nil {
		old := dir + ".before-import-" + time.Now().Format("20060102-150405")
		if err := os.Rename(dir, old); err != nil {
			return err
		}
		fmt.Println("原来的大脑数据移到了", old)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	in, err := os.Open(backup)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(filepath.Join(dir, "hub.db"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	out.Close()
	os.WriteFile(filepath.Join(brain.Dir(root), "snapshot.seq"), []byte(fmt.Sprint(time.Now().Unix())), 0o600)
	fmt.Println("已导入。这台机器成为大脑后会用这份数据，并加密上传到中继。")
	fmt.Println("导入后请在服务器上删除旧 Hub 的数据目录和全部备份，并重新生成模型的 API Key。")
	return nil
}

func releaseCommand(args []string, keyFile, tag string) error {
	if len(args) == 0 {
		return errors.New("用法：sous release keygen --key FILE | sign --key FILE --tag vX.Y.Z <checksums.txt>")
	}
	if keyFile == "" {
		return errors.New("需要 --key：发布私钥文件（放在仓库和 CI 以外）")
	}
	switch args[0] {
	case "keygen":
		if _, err := os.Stat(keyFile); err == nil {
			return fmt.Errorf("%s 已经存在，不覆盖", keyFile)
		}
		k, err := trust.NewSignKey()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
			return err
		}
		raw, _ := json.MarshalIndent(map[string]string{"sign": trust.B64.EncodeToString(k.Bytes()), "pub": trust.B64.EncodeToString(k.Public())}, "", "  ")
		if err := os.WriteFile(keyFile, raw, 0o600); err != nil {
			return err
		}
		fmt.Printf("私钥写入 %s（0600）。公钥（填进 internal/update/releasekey.go）：\n%s\n", keyFile, trust.B64.EncodeToString(k.Public()))
		return nil
	case "sign":
		if len(args) != 2 || tag == "" {
			return errors.New("用法：sous release sign --key FILE --tag vX.Y.Z <checksums.txt>")
		}
		raw, err := os.ReadFile(keyFile)
		if err != nil {
			return err
		}
		var kf struct{ Sign string }
		if err := json.Unmarshal(raw, &kf); err != nil {
			return err
		}
		kb, _ := trust.B64.DecodeString(kf.Sign)
		k, err := trust.SignKeyFromBytes(kb)
		if err != nil {
			return err
		}
		sums, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		sig, err := update.SignRelease(k, tag, sums, time.Now().Unix())
		if err != nil {
			return err
		}
		out := args[1] + ".sig"
		if err := os.WriteFile(out, sig, 0o644); err != nil {
			return err
		}
		if err := update.VerifyRelease(tag, sums, sig); err != nil {
			fmt.Fprintln(os.Stderr, "提醒：这把密钥不在程序内置的发布公钥里：", err)
		}
		fmt.Printf("已写出 %s；上传：gh release upload %s %s\n", out, tag, out)
		return nil
	}
	return fmt.Errorf("未知的子命令 %q", args[0])
}
