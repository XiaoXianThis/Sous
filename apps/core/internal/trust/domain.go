package trust

import (
	"errors"
	"fmt"
	"strconv"
)

// Domain keys encrypt the brain's state (snapshots on the relay). The user's
// identity makes them and wraps one copy to itself and one to each brain
// machine; a new epoch starts whenever a brain leaves.

// DomainKeys is signed by the identity. Epochs holds the newest first and
// keeps a few older ones so a snapshot made just before a rotation still opens.
type DomainKeys struct {
	T      string        `json:"t"`
	V      int64         `json:"v"`
	IAT    int64         `json:"iat"`
	Epochs []DomainEpoch `json:"epochs"`
}

type DomainEpoch struct {
	Epoch int64             `json:"epoch"`
	Wraps map[string]string `json:"wraps"` // member ("user" or a node id) -> sealed key
}

// DomainInfo binds a wrapped key to its epoch and member.
func DomainInfo(epoch int64, member string) string {
	return "sous/v1/domain\x00" + strconv.FormatInt(epoch, 10) + "\x00" + member
}

// WrapDomainKey seals a domain key to a member's KEM key.
func WrapDomainKey(key []byte, epoch int64, member string, kemPub []byte) (string, error) {
	ct, err := SealTo(kemPub, DomainInfo(epoch, member), key)
	if err != nil {
		return "", err
	}
	return B64.EncodeToString(ct), nil
}

// Key unwraps the key of an epoch for member.
func (d DomainKeys) Key(epoch int64, member string, k *KEMKey) ([]byte, error) {
	for _, e := range d.Epochs {
		if e.Epoch != epoch {
			continue
		}
		w, ok := e.Wraps[member]
		if !ok {
			return nil, fmt.Errorf("trust: %s has no key for epoch %d", member, epoch)
		}
		ct, err := B64.DecodeString(w)
		if err != nil {
			return nil, err
		}
		return k.OpenFrom(DomainInfo(epoch, member), ct)
	}
	return nil, fmt.Errorf("trust: epoch %d is unknown", epoch)
}

// Current is the newest epoch.
func (d DomainKeys) Current() (int64, error) {
	if len(d.Epochs) == 0 {
		return 0, errors.New("trust: no domain key yet")
	}
	return d.Epochs[0].Epoch, nil
}

// SnapshotManifest describes the brain's encrypted state on the relay.
type SnapshotManifest struct {
	T      string   `json:"t"`
	Holder string   `json:"holder"`
	Term   int64    `json:"term"`
	Seq    int64    `json:"seq"` // grows with every snapshot, across terms
	Epoch  int64    `json:"epoch"`
	IAT    int64    `json:"iat"`
	Size   int64    `json:"size"`
	Chunks []string `json:"chunks"` // SHA-256 of each encrypted chunk
}

// SnapshotAAD binds a chunk to its snapshot and position.
func SnapshotAAD(seq int64, i int) []byte {
	return []byte("sous/v1/snapshot\x00" + strconv.FormatInt(seq, 10) + "\x00" + strconv.Itoa(i))
}

// ReleaseSig is the release signature of a version's checksums.txt.
type ReleaseSig struct {
	T         string `json:"t"`
	Tag       string `json:"tag"`
	Checksums string `json:"checksums_sha256"`
	IAT       int64  `json:"iat"`
}
