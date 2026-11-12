package node

import "testing"

// A lease change wakes every waiter once; a waiter that looks again blocks
// until the next change instead of seeing the same news forever.
func TestLeaseMovedWakesOnce(t *testing.T) {
	nt := &nodeTrust{leaseCh: make(chan struct{})}
	a, b := nt.leaseChanged(), nt.leaseChanged()
	nt.leaseMoved()
	for _, ch := range []<-chan struct{}{a, b} {
		select {
		case <-ch:
		default:
			t.Fatal("a waiter missed the change")
		}
	}
	select {
	case <-nt.leaseChanged():
		t.Fatal("the same change woke a waiter twice")
	default:
	}
}
