// Package waiter polls synchronous conditions with a timeout.
package waiter

import (
	"context"
	"fmt"
	"time"
)

// Wait checks condition immediately, then waits interval between failed checks.
// Durations must be positive and condition must not be nil. Timeout errors wrap
// context.DeadlineExceeded and include message. Calls never overlap; a blocking
// condition cannot be interrupted, and success after the deadline is rejected.
func Wait(timeout, interval time.Duration, condition func() bool, message string) error {
	if timeout <= 0 || interval <= 0 || condition == nil {
		return fmt.Errorf("%s: waiter requires positive timeout and interval and a non-nil condition", message)
	}
	deadline := time.Now().Add(timeout)
	expired := func() error {
		return fmt.Errorf("%s: timed out after %s: %w", message, timeout, context.DeadlineExceeded)
	}
	for {
		if !time.Now().Before(deadline) {
			return expired()
		}
		ok := condition()
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return expired()
		}
		if ok {
			return nil
		}
		time.Sleep(min(interval, remaining))
	}
}
