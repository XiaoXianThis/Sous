package brain

import (
	"crypto/sha256"
	"encoding/binary"
)

// Content-defined chunking (a gear rolling hash, as in FastCDC): cut points
// depend on the bytes around them, not on their offset. VACUUM INTO moves
// tables when one before them grows, so fixed-size chunks would all change;
// with content-defined ones only the chunks around the change do, and the
// rest are already on the relay.

const (
	cdcMin  = 16 << 10
	cdcMax  = 256 << 10
	cdcMask = 1<<16 - 1 // average chunk about 64 KB
)

var gear = func() (g [256]uint64) {
	for i := range g {
		s := sha256.Sum256([]byte{'s', 'o', 'u', 's', byte(i)})
		g[i] = binary.LittleEndian.Uint64(s[:8])
	}
	return g
}()

// cdcSplit cuts data into chunks of cdcMin..cdcMax bytes.
func cdcSplit(data []byte) [][]byte {
	var out [][]byte
	for len(data) > 0 {
		n := cdcCut(data)
		out = append(out, data[:n])
		data = data[n:]
	}
	return out
}

func cdcCut(data []byte) int {
	if len(data) <= cdcMin {
		return len(data)
	}
	end := min(len(data), cdcMax)
	var h uint64
	for i := cdcMin; i < end; i++ {
		h = h<<1 + gear[data[i]]
		if h&cdcMask == 0 {
			return i + 1
		}
	}
	return end
}
