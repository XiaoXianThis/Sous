package cloudcontroller

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/cloudwork"
)

func TestBootstrapBindsWorkspaceGenerationAndReservation(t *testing.T) {
	c := Controller{origin: "https://fixture.test", seed: bytes.Repeat([]byte{1}, 32)}
	same := c
	first := c.bootstrap(1, "reservation-a")
	if !cloudwork.ValidToken(first) || same.bootstrap(1, "reservation-a") != first {
		t.Fatal("bootstrap isn't reproducible")
	}
	if c.bootstrap(2, "reservation-a") == first || c.bootstrap(1, "reservation-b") == first {
		t.Fatal("bootstrap reused across identities")
	}
	same.origin = "https://another-fixture.test"
	if same.bootstrap(1, "reservation-a") == first {
		t.Fatal("bootstrap reused across workspaces")
	}
}

type planScript struct {
	states       []cloudwork.Capacity
	pos, retired int
}

func (s *planScript) Plan(context.Context, cloudwork.ReconcileCapacity) (cloudwork.Capacity, error) {
	if s.pos >= len(s.states) {
		return cloudwork.Capacity{}, errors.New("unexpected Plan")
	}
	state := s.states[s.pos]
	s.pos++
	return state, nil
}
func (s *planScript) Retired(_ context.Context, gen int64, res string) (cloudwork.Capacity, error) {
	s.retired++
	return cloudwork.Capacity{Generation: gen, Reservation: res, Phase: "off"}, nil
}

type driverFunc func(context.Context, Request) (string, error)

func (f driverFunc) Reconcile(ctx context.Context, r Request) (string, error) { return f(ctx, r) }

func TestControllerDoesNotAcknowledgeAmbiguousProviderRetirement(t *testing.T) {
	state := cloudwork.Capacity{Generation: 7, Phase: "retiring", Reservation: "reservation", Queued: 1}
	h := &planScript{states: []cloudwork.Capacity{state, state, state, state}}
	attempt := 0
	c := Controller{hub: h, origin: "https://fixture.test", provider: driverFunc(func(_ context.Context, r Request) (string, error) {
		if r.Action != retire || r.Generation != 7 || r.Reservation != "reservation" || r.Bootstrap != "" {
			t.Fatalf("provider received invalid retirement: %#v", r)
		}
		attempt++
		switch attempt {
		case 1:
			return "", context.DeadlineExceeded
		case 2:
			return pending, nil
		default:
			return absent, nil
		}
	})}
	if state, err := c.Step(context.Background()); err == nil || state.Phase != "retiring" || h.retired != 0 {
		t.Fatal("unknown provider result was counted as absence", state, err)
	}
	if state, err := c.Step(context.Background()); err != nil || state.Phase != "retiring" || h.retired != 0 {
		t.Fatal("pending provider operation was counted as absence", state, err)
	}
	if state, err := c.Step(context.Background()); err != nil || state.Phase != "off" || h.retired != 1 {
		t.Fatal("settled retirement was not acknowledged", state, err)
	}
}
