// Command sous is the client: the daemon that runs work on this machine, and
// the command line (pair, status, chat). The daemon arrives in milestone M2.
package main

import (
	"fmt"
	"os"

	"github.com/XiaoXianThis/Sous/apps/core/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version.Version)
		return
	}
	fmt.Fprintln(os.Stderr, "sous: the daemon and commands are not implemented yet (milestone M2)")
	os.Exit(2)
}
