package sysmap

import (
	"context"
	"sync"
)

type registry struct {
	sync.Mutex
	probes    map[string]Prober
	instances map[string]func(context.Context) []Instance
}

var reg = &registry{}

func RegisterProbe(id string, p Prober) {
	reg.Lock()
	defer reg.Unlock()
	if reg.probes == nil {
		reg.probes = make(map[string]Prober)
	}
	reg.probes[id] = p
}
func RegisterInstances(kind string, fn func(context.Context) []Instance) {
	reg.Lock()
	defer reg.Unlock()
	if reg.instances == nil {
		reg.instances = make(map[string]func(context.Context) []Instance)
	}
	reg.instances[kind] = fn
}
func Reset() { reg.Lock(); defer reg.Unlock(); reg.probes = nil; reg.instances = nil }
