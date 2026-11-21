//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework Foundation -framework LocalAuthentication
int sousAuthenticateOwner(void);
*/
import "C"

const nativeUnlockSupported = true

func authenticateDeviceOwner() bool {
	return C.sousAuthenticateOwner() == 1
}
