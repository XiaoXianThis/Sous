package event

import "sync"

// Bus fans events out to subscribers. A subscriber that falls behind is
// dropped: its channel is closed, and it is expected to reconnect and catch
// up from the store with the last seq it saw.
type Bus struct {
	mu   sync.Mutex
	next int
	subs map[int]chan Event
}

func NewBus() *Bus {
	return &Bus{subs: make(map[int]chan Event)}
}

// Subscribe returns a channel of events and a function that ends the subscription.
func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	ch := make(chan Event, buffer)
	b.subs[id] = ch
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
	}
}

// Publish delivers ev to every subscriber without blocking.
func (b *Bus) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, ch := range b.subs {
		select {
		case ch <- ev:
		default:
			delete(b.subs, id)
			close(ch)
		}
	}
}
