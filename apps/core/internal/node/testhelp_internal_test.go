package node

import (
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/policy"
)

// needShell skips a test whose cases depend on POSIX shell parsing, which
// the policy does not do on Windows (every command takes the strict path).
func needShell(t *testing.T) {
	t.Helper()
	if !policy.ShellAnalysis() {
		t.Skip("commands are not parsed as POSIX shell here (Windows)")
	}
}

// testDaemon is a daemon that is never run: close its outbox at the end, or
// Windows cannot delete the test's temporary directory.
func testDaemon(t *testing.T, root string, cfg Config) *Daemon {
	d := NewDaemon(root, cfg, nil)
	t.Cleanup(d.ob.close)
	return d
}
