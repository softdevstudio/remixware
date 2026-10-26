// Package net talks to the payment and stock services.
package net

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"
)

// backoff returns the wait before attempt n, counting from 0.
func backoff(n int, base, max time.Duration, jitter float64, rnd func() float64) time.Duration {
	d := base
	for i := 0; i < n && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	if jitter > 0 {
		spread := float64(d) * jitter
		d = time.Duration(float64(d) - spread + 2*spread*rnd())
	}
	return d
}

// retry calls fn until it succeeds, the context ends, or attempts run out.
func retry(ctx context.Context, attempts int, base, max time.Duration, fn func() error) error {
	var err error
	for n := 0; n < attempts; n++ {
		if err = fn(); err == nil {
			return nil
		}
		if errors.Is(err, context.Canceled) {
			return err
		}
		wait := backoff(n, base, max, 0.2, rand.Float64)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return fmt.Errorf("shopfront: service unavailable after %d attempts: %w", attempts, err)
}
