package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFirstEnrollmentAtomicAndBoundToLatestSetupToken(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	issue := func(hash string, expires time.Time) {
		t.Helper()
		if err := st.ReplacePasskeySetup(ctx, hash, expires); err != nil {
			t.Fatal(err)
		}
	}
	issue("old", time.Now().Add(time.Minute))
	issue("expired", time.Now().Add(-time.Minute))
	if err := st.RegisterWithSetup(ctx, "expired", "one", "{}", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired registration accepted: %v", err)
	}
	issue("current", time.Now().Add(time.Minute))
	if err := st.RegisterWithSetup(ctx, "old", "one", "{}", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replaced registration accepted: %v", err)
	}
	var winners atomic.Int32
	var race sync.WaitGroup
	for _, id := range []string{"one", "two"} {
		race.Go(func() {
			err := st.RegisterWithSetup(ctx, "current", id, "{}", true)
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, ErrNotFound) {
				t.Errorf("unexpected storage error: %v", err)
			}
		})
	}
	race.Wait()
	if winners.Load() != 1 {
		t.Fatalf("%d enrollment winners", winners.Load())
	}
	issue("later", time.Now().Add(time.Minute))
	if err := st.RegisterWithSetup(ctx, "later", "three", "{}", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second owner enrollment succeeded: %v", err)
	}
	creds, err := st.Credentials(ctx)
	if err != nil || len(creds) != 1 {
		t.Fatalf("got %v %v", creds, err)
	}
}
