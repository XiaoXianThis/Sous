//go:build !darwin || !cgo

package main

const nativeUnlockSupported = false

func authenticateDeviceOwner() bool { return false }
