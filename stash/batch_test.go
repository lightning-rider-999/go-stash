package stash

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// TestBatchPanicRecovered proves a panic in fn is turned into a returned error
// rather than crashing the process. errgroup does not recover panics in the
// goroutines it launches, so without the recover wrapper this test would abort
// the whole test binary. Reaching the assertions at all proves the process
// survived; the error must carry the recovered value.
func TestBatchPanicRecovered(t *testing.T) {
	items := []int{0, 1, 2, 3, 4}
	err := Batch(context.Background(), 2, items, func(ctx context.Context, item int) error {
		if item == 2 {
			panic("worker exploded")
		}
		return nil
	})
	if err == nil {
		t.Fatal("Batch returned nil; want an error from the recovered panic")
	}
	if !strings.Contains(err.Error(), "worker exploded") {
		t.Errorf("error %v does not carry the recovered panic value", err)
	}
	if !strings.Contains(err.Error(), "batch worker panic") {
		t.Errorf("error %v not tagged as a batch worker panic", err)
	}
}

// TestBatchResultsPanicRecovered proves BatchResults shares the recovery: a
// panic becomes an error and a nil result slice, not a process crash.
func TestBatchResultsPanicRecovered(t *testing.T) {
	items := []int{0, 1, 2, 3}
	got, err := BatchResults(context.Background(), 2, items, func(ctx context.Context, item int) (int, error) {
		if item == 1 {
			panic("results worker exploded")
		}
		return item, nil
	})
	if err == nil {
		t.Fatal("BatchResults returned nil error; want the recovered panic")
	}
	if !strings.Contains(err.Error(), "results worker exploded") {
		t.Errorf("error %v does not carry the recovered panic value", err)
	}
	if got != nil {
		t.Errorf("BatchResults returned %v on panic, want nil", got)
	}
}

// TestBatchCancelEarlyExit proves the submission loop stops enqueuing once ctx
// is cancelled. With a small concurrency limit, g.Go blocks until a slot frees,
// so without the early ctx.Done() check a cancelled batch would still submit
// (and at least attempt) every remaining item. The first slot of workers blocks
// on a gate; ctx is cancelled while they are parked; only the at-most-`limit`
// already-running workers should ever observe an invocation.
func TestBatchCancelEarlyExit(t *testing.T) {
	const n = 200
	const limit = 2
	items := make([]int, n)
	for i := range items {
		items[i] = i
	}

	ctx, cancel := context.WithCancel(context.Background())
	var started atomic.Int64
	release := make(chan struct{})

	go func() {
		// Give the first `limit` workers time to start and park, then cancel and
		// release them so the batch can unwind.
		time.Sleep(20 * time.Millisecond)
		cancel()
		close(release)
	}()

	err := Batch(ctx, limit, items, func(ctx context.Context, item int) error {
		started.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return ctx.Err()
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Batch err = %v, want context.Canceled", err)
	}
	// Without the early ctx.Done() check the loop submits — and so starts — every
	// one of the n items (each just observes ctx and returns). The guard stops it
	// after the at-most-`limit` workers in flight when cancel fired, plus a small
	// slack for items that slip through as those slots free before the next
	// ctx.Err() check. A handful proves the short-circuit; n proves it failed.
	const slack = limit + 4
	if got := started.Load(); got > slack {
		t.Errorf("started %d of %d workers; cancellation did not stop the submission loop (want <= %d)", got, n, slack)
	}
}
