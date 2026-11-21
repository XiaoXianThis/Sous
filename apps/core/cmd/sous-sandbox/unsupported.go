//go:build !darwin || !arm64 || !cgo

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "此平台尚未通过 Sous 的 Microsandbox 本机执行验收；不会回退到实体机执行。")
	os.Exit(1)
}
