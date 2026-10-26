// Copyright 2020 Harbor Lane Software
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

// Package retrykit retries operations with exponential backoff.
package retrykit

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"
)

// ErrPermanent marks an error that retrying won't fix.
var ErrPermanent = errors.New("retrykit: permanent error")

// Backoff returns the wait before attempt n, counting from 0: base doubled
// each time, capped at max, spread by up to jitter either way.
func Backoff(n int, base, max time.Duration, jitter float64, rnd func() float64) time.Duration {
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

// Retryable reports whether an error is worth another attempt.
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrPermanent) || errors.Is(err, context.Canceled) {
		return false
	}
	var t interface{ Temporary() bool }
	if errors.As(err, &t) {
		return t.Temporary()
	}
	return true
}

// Do calls fn until it succeeds, the context ends, or attempts run out.
func Do(ctx context.Context, attempts int, base, max time.Duration, fn func() error) error {
	var err error
	for n := 0; n < attempts; n++ {
		if err = fn(); err == nil {
			return nil
		}
		if !Retryable(err) {
			return err
		}
		wait := Backoff(n, base, max, 0.2, rand.Float64)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return fmt.Errorf("retrykit: gave up after %d attempts: %w", attempts, err)
}
