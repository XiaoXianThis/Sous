package ec2driver

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const ledgerName = "ec2-ledger.json"

type Ledger struct {
	Version      int    `json:"version"`
	Binding      string `json:"binding"`
	Generation   int64  `json:"generation"`
	Reservation  string `json:"reservation,omitempty"`
	Phase        string `json:"phase"`
	InstanceID   string `json:"instance_id,omitempty"`
	RootVolume   string `json:"root_volume_id,omitempty"`
	Attempts     int    `json:"attempts"`
	LastLaunchAt int64  `json:"last_launch_at,omitempty"`
	Plan         string `json:"plan_sha256,omitempty"`
	Rejected     bool   `json:"definitively_rejected,omitempty"`
	Terminal     bool   `json:"observed_terminated,omitempty"`
}

type ledgerFile struct {
	root *os.Root
	lock *os.File
}

func openLedger(c Config) (*ledgerFile, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(c.StateDir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("EC2 ledger requires an operator-created private directory (0700)")
	}
	if resolved, err := filepath.EvalSymlinks(c.StateDir); err != nil || resolved != c.StateDir {
		return nil, errors.New("EC2 ledger directory must not follow links")
	}
	root, err := os.OpenRoot(c.StateDir)
	if err != nil {
		return nil, err
	}
	// Private service directory; the same inode is locked through each entire
	// reconcile (including the AWS call). Unlike the ledger it is never renamed.
	lock, err := root.OpenFile("ec2.lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		root.Close()
		return nil, err
	}
	if err := lockFile(lock); err != nil {
		lock.Close()
		root.Close()
		return nil, err
	}
	return &ledgerFile{root, lock}, nil
}

func (f *ledgerFile) Close() { _ = f.lock.Close(); _ = f.root.Close() }

func (f *ledgerFile) load(c Config) (s Ledger, err error) {
	info, err := f.root.Lstat(ledgerName)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 8192 {
		return s, errors.New("missing or invalid EC2 ledger; do not recreate it over an active deployment")
	}
	raw, err := f.root.Open(ledgerName)
	if err != nil {
		return s, err
	}
	defer raw.Close()
	if err := decode(raw, 8192, &s); err != nil {
		return s, err
	}
	if s.Version != 1 || s.Binding != c.fingerprint() ||
		(s.Phase != "off" && s.Phase != "open" && s.Phase != "retiring") || s.Generation < 0 ||
		(s.Generation == 0 && (s.Reservation != "" || s.Phase != "off" || s.Attempts != 0 || s.InstanceID != "")) || (s.Generation > 0 && s.Reservation == "") ||
		s.Attempts < 0 || s.Attempts > maxLaunchAttempts || (s.Attempts == 0 && (s.Rejected || s.Plan != "" || s.LastLaunchAt != 0)) ||
		(s.Attempts > 0 && (len(s.Plan) != 64 || s.LastLaunchAt <= 0)) ||
		(s.InstanceID != "" && !instancePattern.MatchString(s.InstanceID)) ||
		(s.RootVolume != "" && (!volumePattern.MatchString(s.RootVolume) || s.InstanceID == "")) ||
		(s.Terminal && s.InstanceID == "") || (s.Phase == "off" && s.InstanceID != "" && !s.Terminal) {
		return Ledger{}, errors.New("EC2 ledger does not match this deployment or contains invalid state")
	}
	return s, nil
}

func (f *ledgerFile) save(s Ledger) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	name := fmt.Sprintf("ledger-%x.tmp", nonce)
	tmp, err := f.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.root.Remove(name)
	n, err := tmp.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	if err = errors.Join(err, tmp.Sync(), tmp.Close()); err != nil {
		return err
	}
	if err = f.root.Rename(name, ledgerName); err != nil {
		return err
	}
	dir, err := f.root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// Init is explicit. A missing ledger is otherwise never treated as zero
// capacity: it may be the only local evidence of a launch with a lost reply.
func Init(c Config) error {
	f, err := openLedger(c)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.root.Lstat(ledgerName); !errors.Is(err, os.ErrNotExist) {
		return errors.New("EC2 ledger already exists or cannot be inspected")
	}
	return f.save(Ledger{Version: 1, Binding: c.fingerprint(), Phase: "off"})
}

func Status(c Config) (Ledger, error) {
	f, err := openLedger(c)
	if err != nil {
		return Ledger{}, err
	}
	defer f.Close()
	return f.load(c)
}
