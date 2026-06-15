package stash

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBatchRespectsLimit(t *testing.T) {
	const limit = 3
	const n = 30
	var inFlight, maxSeen atomic.Int64

	items := make([]int, n)
	for i := range items {
		items[i] = i
	}

	err := Batch(context.Background(), limit, items, func(ctx context.Context, _ int) error {
		cur := inFlight.Add(1)
		for {
			old := maxSeen.Load()
			if cur <= old || maxSeen.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		inFlight.Add(-1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := maxSeen.Load(); got > limit {
		t.Errorf("max in-flight = %d, want <= %d", got, limit)
	}
}

func TestBatchFirstErrorCancelsRest(t *testing.T) {
	// errgroup.WithContext cancels the derived context on the first error. The
	// proof that remaining work is short-circuited: every other worker observes
	// ctx.Done() and returns immediately instead of running its full duration.
	// The first item fails fast; without cancellation, the rest would each
	// sleep workDuration.
	const n = 50
	const limit = 4
	const workDuration = 200 * time.Millisecond
	items := make([]int, n)
	for i := range items {
		items[i] = i
	}

	sentinel := errors.New("boom")
	var cancelObserved, fullySlept atomic.Int64

	start := time.Now()
	err := Batch(context.Background(), limit, items, func(ctx context.Context, item int) error {
		if item == 0 {
			return sentinel
		}
		select {
		case <-ctx.Done():
			cancelObserved.Add(1)
			return ctx.Err()
		case <-time.After(workDuration):
			fullySlept.Add(1)
			return nil
		}
	})
	elapsed := time.Since(start)

	if !errors.Is(err, sentinel) {
		t.Fatalf("Batch returned %v, want the sentinel", err)
	}
	if cancelObserved.Load() == 0 {
		t.Error("no worker observed context cancellation after the first error")
	}
	// If cancellation did not short-circuit, every batch of `limit` items would
	// sleep workDuration in turn: roughly (n/limit)*workDuration. Cancellation
	// must finish far sooner.
	if elapsed >= (n/limit)*workDuration {
		t.Errorf("Batch took %v; cancellation did not short-circuit remaining work", elapsed)
	}
}

func TestBatchNoRetry(t *testing.T) {
	var calls sync.Map // item -> count
	items := []int{0, 1, 2, 3, 4}
	failItem := 2

	_ = Batch(context.Background(), 2, items, func(ctx context.Context, item int) error {
		n, _ := calls.LoadOrStore(item, new(atomic.Int64))
		n.(*atomic.Int64).Add(1)
		if item == failItem {
			return fmt.Errorf("fail %d", item)
		}
		return nil
	})

	calls.Range(func(k, v any) bool {
		if got := v.(*atomic.Int64).Load(); got > 1 {
			t.Errorf("item %v invoked %d times; no retries allowed", k, got)
		}
		return true
	})
}

func TestBatchUnbounded(t *testing.T) {
	const n = 20
	var inFlight, maxSeen atomic.Int64
	items := make([]int, n)

	err := Batch(context.Background(), 0, items, func(ctx context.Context, _ int) error {
		cur := inFlight.Add(1)
		for {
			old := maxSeen.Load()
			if cur <= old || maxSeen.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		inFlight.Add(-1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := maxSeen.Load(); got < 2 {
		t.Errorf("unbounded batch ran with max in-flight %d; expected real concurrency", got)
	}
}

func TestBatchEmpty(t *testing.T) {
	called := false
	err := Batch(context.Background(), 4, []int{}, func(ctx context.Context, _ int) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("fn invoked for an empty item slice")
	}
}

func TestBatchResultsPreservesOrder(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6, 7, 8}
	got, err := BatchResults(context.Background(), 3, items, func(ctx context.Context, item int) (int, error) {
		// Sleep inversely to value so completion order differs from input order.
		time.Sleep(time.Duration(10-item) * time.Millisecond)
		return item * item, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range items {
		if want := item * item; got[i] != want {
			t.Errorf("results[%d] = %d, want %d (order not preserved)", i, got[i], want)
		}
	}
}

func TestBatchResultsError(t *testing.T) {
	items := []int{1, 2, 3, 4}
	sentinel := errors.New("nope")
	got, err := BatchResults(context.Background(), 2, items, func(ctx context.Context, item int) (int, error) {
		if item == 3 {
			return 0, sentinel
		}
		return item, nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("BatchResults err = %v, want sentinel", err)
	}
	if got != nil {
		t.Errorf("BatchResults returned %v on error, want nil", got)
	}
}

func TestBatchResultsEmpty(t *testing.T) {
	got, err := BatchResults(context.Background(), 4, []int{}, func(ctx context.Context, item int) (int, error) {
		return item, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("results = %v, want empty", got)
	}
}
