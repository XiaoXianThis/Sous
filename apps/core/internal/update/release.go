package update

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
)

// ReleaseKeys are the public keys allowed to sign releases (decision 0030).
// The private key never leaves the author's machine: CI builds and uploads,
// the author signs checksums.txt with `sous release sign` and uploads
// checksums.txt.sig. A machine installs nothing without a valid signature.
// To rotate, ship the new key here in a release signed by the old one.
var ReleaseKeys = []string{releaseKey}

// SigAsset is the release signature's asset name.
const SigAsset = "checksums.txt.sig"

// VerifyRelease checks that checksums.txt of tag was signed by a release key.
func VerifyRelease(tag string, checksums, sig []byte) error {
	var s trust.Signed
	if err := json.Unmarshal(sig, &s); err != nil {
		return errors.New("发布签名的格式不对")
	}
	sum := sha256.Sum256(checksums)
	for _, k := range ReleaseKeys {
		pub, err := trust.B64.DecodeString(k)
		if err != nil || len(pub) == 0 {
			continue
		}
		var rs trust.ReleaseSig
		if _, err := s.Open(pub, trust.TRelease, &rs); err != nil {
			continue
		}
		if rs.Tag != tag {
			return fmt.Errorf("签名属于 %s，不是 %s", rs.Tag, tag)
		}
		if rs.Checksums != trust.B64.EncodeToString(sum[:]) {
			return errors.New("checksums.txt 和签名不符")
		}
		return nil
	}
	return errors.New("发布签名无效：不是作者的发布密钥签的")
}

// SignRelease signs checksums.txt of tag (the author runs this locally).
func SignRelease(k *trust.SignKey, tag string, checksums []byte, now int64) ([]byte, error) {
	sum := sha256.Sum256(checksums)
	doc, err := trust.Sign(k, trust.TRelease, trust.ReleaseSig{T: trust.TRelease, Tag: tag, Checksums: trust.B64.EncodeToString(sum[:]), IAT: now})
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(doc, "", "  ")
}
