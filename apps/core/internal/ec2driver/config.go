// Package ec2driver manages one dedicated Sous worker reservation in a pinned
// EC2 account/region/subnet. It has no Hub API credentials or work materials.
package ec2driver

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/cloudcontroller"
	"github.com/XiaoXianThis/Sous/apps/core/internal/cloudwork"
	"github.com/oklog/ulid/v2"
)

type Config struct {
	Hub          string `json:"hub"`
	Account      string `json:"account_id"`
	Region       string `json:"region"`
	AMI          string `json:"ami"`
	Subnet       string `json:"subnet_id"`
	Group        string `json:"security_group_id"`
	InstanceType string `json:"instance_type"`
	DiskGiB      int32  `json:"disk_gib"`
	StateDir     string `json:"state_dir"`
}

var (
	accountPattern  = regexp.MustCompile(`^[0-9]{12}$`)
	regionPattern   = regexp.MustCompile(`^(af|ap|ca|eu|il|me|mx|sa|us)-[a-z]+-[1-9][0-9]*$`)
	amiPattern      = regexp.MustCompile(`^ami-[0-9a-f]{17}$`)
	subnetPattern   = regexp.MustCompile(`^subnet-[0-9a-f]{17}$`)
	groupPattern    = regexp.MustCompile(`^sg-[0-9a-f]{17}$`)
	instancePattern = regexp.MustCompile(`^i-[0-9a-f]{17}$`)
	volumePattern   = regexp.MustCompile(`^vol-[0-9a-f]{17}$`)
	sizePattern     = regexp.MustCompile(`^(c8i|m8i|r8i)\.(large|xlarge)$`)
	dnsPattern      = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

func canonicalOrigin(s string) bool {
	u, err := url.Parse(s)
	if err != nil || len(s) > 315 || u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(s, "#") {
		return false
	}
	host := u.Hostname()
	if len(host) > 253 || !strings.Contains(host, ".") || !strings.ContainsAny(host, "abcdefghijklmnopqrstuvwxyz") {
		return false
	}
	for _, name := range strings.Split(host, ".") {
		if !dnsPattern.MatchString(name) {
			return false
		}
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 || n == 443 || p != strconv.Itoa(n) || u.Host != host+":"+p {
			return false
		}
	} else if u.Host != host {
		return false
	}
	return s == "https://"+u.Host
}

func (c Config) validate() error {
	if !canonicalOrigin(c.Hub) || !accountPattern.MatchString(c.Account) || !regionPattern.MatchString(c.Region) ||
		!amiPattern.MatchString(c.AMI) || !subnetPattern.MatchString(c.Subnet) || !groupPattern.MatchString(c.Group) ||
		!sizePattern.MatchString(c.InstanceType) || c.DiskGiB < 12 || c.DiskGiB > 64 || !filepath.IsAbs(c.StateDir) || filepath.Clean(c.StateDir) != c.StateDir {
		return errors.New("EC2 requires a canonical Hub, commercial region, pinned account/AMI/subnet/group, a supported small instance type, 12–64 GiB disk and a private absolute ledger directory")
	}
	return nil
}

func (c Config) scope() string { return cloudwork.Hash([]byte(c.Hub)) }
func (c Config) fingerprint() string {
	// A changed launch plan must never be retried under the same provider token.
	// Omit the OS path; possession of a copy of the ledger is not permission to
	// operate two controllers against different copies.
	c.StateDir = ""
	b, _ := json.Marshal(c)
	return cloudwork.Hash(b)
}

func ReadConfig(path string) (c Config, err error) {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 8192 || st.Mode().Perm()&0o022 != 0 {
		return c, errors.New("invalid EC2 operator configuration file")
	}
	f, err := os.Open(path)
	if err != nil {
		return c, errors.New("cannot open EC2 operator configuration")
	}
	defer f.Close()
	if err := decode(f, 8192, &c); err != nil {
		return c, err
	}
	return c, c.validate()
}

func ReadRequest(in io.Reader) (r cloudcontroller.Request, err error) {
	err = decode(in, 4096, &r)
	return
}

func decode(in io.Reader, limit int64, out any) error {
	d := json.NewDecoder(io.LimitReader(in, limit+1))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(json.RawMessage)) != io.EOF {
		return errors.New("invalid EC2 driver JSON")
	}
	return nil
}

func (c Config) validateRequest(r cloudcontroller.Request) error {
	id, err := ulid.ParseStrict(r.Reservation)
	if err != nil || id.String() != r.Reservation || r.Generation < 1 || r.Scope != c.scope() || r.Hub != c.Hub {
		return errors.New("reservation is not bound to this EC2 ledger's workspace")
	}
	switch r.Action {
	case "ensure":
		if cloudwork.ValidToken(r.Bootstrap) && r.BootstrapUntil > 0 {
			return nil
		}
	case "retire":
		if r.Bootstrap == "" && r.BootstrapUntil == 0 {
			return nil
		}
	}
	return errors.New("invalid EC2 capacity operation")
}
