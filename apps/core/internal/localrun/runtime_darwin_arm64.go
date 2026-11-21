//go:build cgo

package localrun

import "golang.org/x/sys/unix"

// Official Microsandbox v0.7.16 release artifacts, not automatically updated.
const runtimeSHA256 = "5f21b45d23e5af599c56c97f0593ce7e8251deb86970e7b9d3bb21e42917fda6"
const firmwareSHA256 = "3421360f67cdc993b45f3bf9aec4c2d793407c38c710bd40ea279b6d51cc7b8f"

func checkHypervisor() error { return nil }

func publishNew(fd int, temporary, name string) error {
	return unix.RenameatxNp(fd, temporary, fd, name, unix.RENAME_EXCL|unix.RENAME_NOFOLLOW_ANY)
}
