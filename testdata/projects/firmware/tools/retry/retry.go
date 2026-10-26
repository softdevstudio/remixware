// Package retry retries flashing a board when the link drops.
package retry

import (
	"context"
	"errors"
	"time"
)

var errBricked = errors.New("board does not answer")

func wait(n int, base, max time.Duration, jitter float64, rnd func() float64) time.Duration {
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

func retryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errBricked) || errors.Is(err, context.Canceled) {
		return false
	}
	var t interface{ Temporary() bool }
	if errors.As(err, &t) {
		return t.Temporary()
	}
	return true
}
