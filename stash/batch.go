package stash

import (
	"context"

	"golang.org/x/sync/errgroup"
)

// Batch runs fn over every item with bounded concurrency. It is built on
// errgroup.WithContext, so the first non-nil error cancels the derived context
// and Batch returns that error once the in-flight calls observing the
// cancellation have unwound. A limit of n>0 caps the number of concurrent
// calls; a limit of zero or less runs every item concurrently.
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
		g.Go(func() error {
			return fn(ctx, item)
		})
	}

	return g.Wait()
}

// BatchResults runs fn over every item with bounded concurrency and returns the
// results in input order, regardless of completion order. Its concurrency,
// cancellation, and no-retry behaviour match [Batch]: the first error cancels
// the rest and is returned, and on any error the result slice is nil.
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
		g.Go(func() error {
			r, err := fn(ctx, item)
			if err != nil {
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
