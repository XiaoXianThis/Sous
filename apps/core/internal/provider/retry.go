package provider

import (
	"context"
	"errors"
	"net"
	"syscall"
	"time"
)

// Transient reports whether err is a passing failure worth retrying: HTTP 408,
// 429 or 5xx, or a refused, reset or timed-out connection.
func Transient(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status == 408 || apiErr.Status == 429 || apiErr.Status >= 500
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// WithRetry wraps p so transient failures are retried up to attempts-1 times
// with a growing delay. A request is never retried once deltas were streamed,
// since the caller already showed them.
func WithRetry(p Provider, attempts int, firstDelay time.Duration) Provider {
	return &retrying{Provider: p, attempts: attempts, delay: firstDelay}
}

type retrying struct {
	Provider
	attempts int
	delay    time.Duration
}

func (r *retrying) Stream(ctx context.Context, req Request, onDelta func(Delta)) (Response, error) {
	delay := r.delay
	var lastErr error
	for i := 0; i < r.attempts; i++ {
		streamed := false
		resp, err := r.Provider.Stream(ctx, req, func(d Delta) {
			streamed = true
			if onDelta != nil {
				onDelta(d)
			}
		})
		if err == nil || streamed || !Transient(err) || i == r.attempts-1 {
			return resp, err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return Response{}, ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}
	return Response{}, lastErr
}
