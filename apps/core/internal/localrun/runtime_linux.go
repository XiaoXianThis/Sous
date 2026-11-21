//go:build (amd64 || arm64) && cgo && sous_linux_worker

package localrun

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

func checkHypervisor() error { return checkKVM("/dev/kvm") }

// Probe the device as the actual worker service user, before claiming work.
// A regular path, a symlink, or an emulated software backend is not KVM.
func checkKVM(path string) error {
	const kvmGetAPIVersion = 0xae00 // Linux uapi: _IO(KVMIO, 0x00)
	const kvmAPIVersion = 12
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("云执行需要当前服务账户可读写 /dev/kvm：%w", err)
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFCHR {
		return errors.New("/dev/kvm 不是可验证的 KVM 字符设备")
	}
	version, err := unix.IoctlRetInt(fd, kvmGetAPIVersion)
	if err != nil || version != kvmAPIVersion {
		return errors.New("/dev/kvm 未通过 Linux KVM API 检查")
	}
	return nil
}

func publishNew(fd int, temporary, name string) error {
	return unix.Renameat2(fd, temporary, fd, name, unix.RENAME_NOREPLACE)
}
