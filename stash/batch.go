package stash

import (
	"context"
	"fmt"
	"runtime/debug"

	"golang.org/x/sync/errgroup"
)

// Batch runs fn over every item with bounded concurrency. It is built on
// errgroup.WithContext, so the first non-nil error cancels the derived context
// and Batch returns that error once the in-flight calls observing the
// cancellation have unwound.
//
// A limit of n>0 caps the number of concurrent calls. A limit of zero or less
// is treated as unbounded: every item runs concurrently. That is intentional —
// callers pass <=0 to opt out of throttling — so guard the limit yourself if an
// unbounded fan-out would be a bug for your input sizes.
//
// Cancellation: when the parent ctx is cancelled (or the first error cancels the
// derived ctx), Batch stops submitting further items promptly. Work already
// in flight is only short-circuited if fn observes ctx — fn must select on or
// otherwise honour ctx.Done() for a cancelled batch to unwind quickly; a fn
// that ignores ctx runs to completion regardless.
//
// A panic in fn is recovered and returned as an error (carrying the recovered
// value and the stack of the panicking goroutine) rather than crashing the
// process; like any error, it cancels the derived ctx.
//
// There is deliberately no retry. A well-behaved client must not mask a
// failure: when an operation fails, the caller learns about it rather than
// having the library silently try again.
func Batch[T any](ctx context.Context, limit int, items []T, fn func(ctx context.Context, item T) error) error {
	if len(items) == 0 {
		return nil
	}

	g, ctx := errgroup.WithContext(ctx)
	if limit > 0 {
		g.SetLimit(limit)
	}

	for _, item := range items {
		// Stop enqueuing once ctx is cancelled (by the parent or by the first
		// error). With a concurrency limit, g.Go blocks until a slot frees, so
		// without this guard a cancelled batch would keep submitting every
		// remaining item before Wait could return.
		if err := ctx.Err(); err != nil {
			break
		}
		g.Go(func() error {
			return recoverWorker(func() error { return fn(ctx, item) })
		})
	}

	return g.Wait()
}

// BatchResults runs fn over every item with bounded concurrency and returns the
// results in input order, regardless of completion order. Its concurrency,
// cancellation, panic-recovery, and no-retry behaviour match [Batch]: a limit
// of <=0 is unbounded, the first error (or a recovered panic) cancels the rest
// and is returned, cancellation only short-circuits in-flight work that fn lets
// observe ctx, and on any error the result slice is nil.
//
// Each result is written to its own index, so no synchronisation around the
// slice is needed.
func BatchResults[T, R any](ctx context.Context, limit int, items []T, fn func(ctx context.Context, item T) (R, error)) ([]R, error) {
	if len(items) == 0 {
		return nil, nil
	}

	results := make([]R, len(items))
	g, ctx := errgroup.WithContext(ctx)
	if limit > 0 {
		g.SetLimit(limit)
	}

	for i, item := range items {
		// See Batch: stop enqueuing as soon as ctx is cancelled so a cancelled
		// batch does not block submitting every remaining item.
		if err := ctx.Err(); err != nil {
			break
		}
		g.Go(func() error {
			var r R
			if err := recoverWorker(func() (err error) {
				r, err = fn(ctx, item)
				return err
			}); err != nil {
				return err
			}
			results[i] = r
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}

// recoverWorker runs fn and converts a panic into an error so a single bad
// worker cannot crash the whole process. errgroup does not recover panics in
// the goroutines it launches, so without this a panic in fn would propagate up
// the goroutine's stack and terminate the program. The returned error carries
// the recovered value and the stack of the panicking goroutine for diagnosis.
func recoverWorker(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("stash: batch worker panic: %v\n%s", r, debug.Stack())
		}
	}()
	return fn()
}
