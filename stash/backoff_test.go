package stash

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// TestSleepBackoffSchedule pins the exponential backoff schedule in virtual
// time. The reconnect tests in subscriptions_test.go prove that a reconnect
// happens, but they run on the wall clock with tiny delays and never assert the
// delay each attempt actually waits — so a broken shift or a broken cap would
// pass them. This drives sleepBackoff directly under synctest and measures the
// elapsed virtual time per attempt, which is exact and deterministic.
//
// The schedule is delay = base << (attempt-1), capped at max, with a
// non-positive delay (from overflow on a large attempt) also clamped to max.
func TestSleepBackoffSchedule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := subOptions{
			backoffBase: 500 * time.Millisecond,
			backoffMax:  4 * time.Second,
		}

		cases := []struct {
			attempt int
			want    time.Duration
		}{
			{1, 500 * time.Millisecond}, // base << 0
			{2, 1 * time.Second},        // base << 1
			{3, 2 * time.Second},        // base << 2
			{4, 4 * time.Second},        // base << 3 == max
			{5, 4 * time.Second},        // base << 4 would exceed max -> capped
			{6, 4 * time.Second},        // still capped
		}

		for _, tc := range cases {
			start := time.Now()
			if !sleepBackoff(context.Background(), o, tc.attempt) {
				t.Fatalf("attempt %d: sleepBackoff returned false without cancellation", tc.attempt)
			}
			synctest.Wait()
			if got := time.Since(start); got != tc.want {
				t.Errorf("attempt %d: waited %s, want %s", tc.attempt, got, tc.want)
			}
		}
	})
}

// TestSleepBackoffOverflowClampsToMax confirms the guard for a delay that has
// shifted past the type's range: a large attempt makes base << (attempt-1)
// non-positive, and the function must clamp it to max rather than return
// instantly or wait a nonsense duration.
func TestSleepBackoffOverflowClampsToMax(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := subOptions{
			backoffBase: 500 * time.Millisecond,
			backoffMax:  30 * time.Second,
		}
		// attempt 64 shifts a non-zero base entirely out of an int64, yielding a
		// non-positive delay that the guard clamps to max.
		start := time.Now()
		if !sleepBackoff(context.Background(), o, 64) {
			t.Fatal("sleepBackoff returned false without cancellation")
		}
		synctest.Wait()
		if got := time.Since(start); got != o.backoffMax {
			t.Errorf("overflowed delay waited %s, want the max %s", got, o.backoffMax)
		}
	})
}

// TestSleepBackoffHonoursContextCancel confirms a cancelled context cuts the
// wait short and reports false, so a reconnect loop stops promptly on shutdown
// instead of sleeping out a long backoff.
func TestSleepBackoffHonoursContextCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := subOptions{
			backoffBase: 10 * time.Second,
			backoffMax:  30 * time.Second,
		}

		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan bool, 1)
		go func() {
			done <- sleepBackoff(ctx, o, 1)
		}()

		// Let the backoff timer arm, then cancel well before its 10s delay.
		time.Sleep(time.Second)
		synctest.Wait()
		cancel()

		select {
		case ok := <-done:
			if ok {
				t.Error("sleepBackoff returned true despite cancellation")
			}
		case <-time.After(time.Second):
			t.Fatal("sleepBackoff did not return promptly after cancel")
		}
	})
}
