package waiter

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWait(t *testing.T) {
	calls := 0
	if err := Wait(time.Second, time.Millisecond, func() bool { calls++; return calls == 3 }, "service ready"); err != nil || calls != 3 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	if err := Wait(time.Second, time.Second, func() bool { return true }, "ready"); err != nil {
		t.Fatal(err)
	}
}
func TestTimeout(t *testing.T) {
	calls := 0
	err := Wait(10*time.Millisecond, time.Second, func() bool { calls++; return false }, "port 8080")
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "port 8080") || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	err = Wait(time.Millisecond, time.Millisecond, func() bool { time.Sleep(5 * time.Millisecond); return true }, "slow")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("late success: %v", err)
	}
}
func TestInvalid(t *testing.T) {
	for _, tt := range []struct {
		timeout, interval time.Duration
		fn                func() bool
	}{{0, time.Second, func() bool { return true }}, {time.Second, 0, func() bool { return true }}, {time.Second, time.Second, nil}} {
		if err := Wait(tt.timeout, tt.interval, tt.fn, "invalid"); err == nil {
			t.Fatal("expected error")
		}
	}
}
