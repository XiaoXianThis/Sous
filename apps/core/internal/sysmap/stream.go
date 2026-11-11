package sysmap

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// maxTickFlows caps the records one tick carries. The page draws every one
// of them as a step, so a busy second (a task starting fans out into many
// events, each pushed to every open page) must not lose its beginning.
const maxTickFlows = 1000

type Tick struct {
	T       int64             `json:"t"`
	Edges   map[string]uint32 `json:"edges"`
	Modules map[string]Status `json:"modules,omitempty"`
	Flows   []Record          `json:"flows,omitempty"`
}

var tickEvery = time.Second
var stream = struct {
	sync.Mutex
	subs map[chan Tick]bool
	stop chan struct{}
	done chan struct{}
}{}

func running() bool { stream.Lock(); defer stream.Unlock(); return stream.stop != nil }
func Subscribe() (<-chan Tick, func()) {
	stream.Lock()
	defer stream.Unlock()
	ch := make(chan Tick, 4)
	if stream.subs == nil {
		stream.subs = make(map[chan Tick]bool)
	}
	stream.subs[ch] = true
	if stream.stop == nil {
		stream.stop = make(chan struct{})
		stream.done = make(chan struct{})
		go tickLoop(stream.stop, stream.done)
	}
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			stream.Lock()
			delete(stream.subs, ch)
			var done chan struct{}
			if len(stream.subs) == 0 && stream.stop != nil {
				close(stream.stop)
				stream.stop = nil
				done = stream.done
				stream.done = nil
			}
			stream.Unlock()
			if done != nil {
				<-done
			}
		})
	}
}
func tickLoop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()
	previous := map[string]string{}
	_, sequence := recordsSince(0)
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			t := now().Unix() - 1
			counts := Counts(t)
			edges := map[string]uint32{}
			for edge, values := range counts {
				if len(values) > 0 && values[0] > 0 {
					edges[edge] = values[0]
				}
			}
			graph := Snapshot(context.Background())
			changed := map[string]Status{}
			for id, status := range graph.Status {
				b, _ := json.Marshal(status)
				if previous[id] != string(b) {
					changed[id] = status
				}
				previous[id] = string(b)
			}
			records, next := recordsSince(sequence)
			sequence = next
			if len(records) > maxTickFlows {
				records = records[len(records)-maxTickFlows:]
			}
			tick := Tick{T: t, Edges: edges, Modules: changed, Flows: records}
			stream.Lock()
			if stream.stop == stop {
				for ch, first := range stream.subs {
					outgoing := tick
					if first {
						outgoing.Modules = graph.Status
					}
					select {
					case ch <- outgoing:
						stream.subs[ch] = false
					default:
					}
				}
			}
			stream.Unlock()
		}
	}
}
