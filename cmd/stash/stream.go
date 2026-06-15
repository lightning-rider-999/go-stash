package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"

	"github.com/lightning-rider-999/go-stashapp/stash"
)

// streamForSpec dispatches a subscription leaf to its typed streamer. The three
// subscription operations (jobsSubscribe, loggingSubscribe, scanCompleteSubscribe)
// each map to a generated subscribe function; the dispatch keys on OpName so a
// new subscription added to the schema is a compile error here rather than a
// silent fall-through. Each event is emitted as one NDJSON line to out.
func streamForSpec(ctx context.Context, c *stash.Client, spec commandSpec, out io.Writer) error {
	switch spec.OpName {
	case "JobsSubscribe":
		return streamSubscription(ctx, c, adaptSubscribe(stash.JobsSubscribe), out)
	case "LoggingSubscribe":
		return streamSubscription(ctx, c, adaptSubscribe(stash.LoggingSubscribe), out)
	case "ScanCompleteSubscribe":
		return streamSubscription(ctx, c, adaptSubscribe(stash.ScanCompleteSubscribe), out)
	default:
		return fmt.Errorf("no streamer wired for subscription %q", spec.OpName)
	}
}

// adaptSubscribe wraps a generated subscription function — which returns a
// (chan T, subscriptionID, error) triple — into the subscribe shape
// [stash.Subscribe] expects: (ctx, wc) -> (<-chan T, error). The subscription ID
// is not needed by the streamer (the connection is torn down on ctx cancel), so
// it is dropped.
func adaptSubscribe[T any](
	gen func(context.Context, graphql.WebSocketClient) (chan T, string, error),
) func(context.Context, graphql.WebSocketClient) (<-chan T, error) {
	return func(ctx context.Context, wc graphql.WebSocketClient) (<-chan T, error) {
		ch, _, err := gen(ctx, wc)
		if err != nil {
			return nil, err
		}
		return ch, nil
	}
}

// streamSubscription opens a reconnecting subscription via [stash.Subscribe] and
// writes each received event as one compact JSON line (NDJSON) to out. It is the
// production wiring: [stash.Subscribe] manages dialling, reconnect, and keepalive.
// The event-consuming logic is in streamEvents, which takes the source as two
// channels so a test can drive it with in-memory channels and no socket.
//
// subscribe is the per-connection subscribe function (production passes a
// generated function via adaptSubscribe).
func streamSubscription[T any](
	ctx context.Context,
	c *stash.Client,
	subscribe func(context.Context, graphql.WebSocketClient) (<-chan T, error),
	out io.Writer,
) error {
	events, errCh := stash.Subscribe(ctx, c, subscribe)
	return streamEvents(ctx, events, errCh, out)
}

// streamEvents writes each event from the events channel as one compact JSON
// line (NDJSON) to out, flushing after every line so an agent reading the pipe
// sees events as they arrive. The (events, errCh) pair is exactly the shape
// [stash.Subscribe] returns, and is the injection point: a test supplies
// in-memory channels, so no real socket and no reconnect machinery are needed.
//
// It returns:
//
//   - nil when ctx is cancelled (SIGINT): a clean stop, exit 0. The events
//     channel closing, or the error channel yielding nil/context.Canceled, is the
//     same clean stop.
//   - a transport-classified error when errCh reports a terminal failure
//     (reconnect bound exhausted), so classifyExit maps it to transport.
func streamEvents[T any](
	ctx context.Context,
	events <-chan T,
	errCh <-chan error,
	out io.Writer,
) error {
	enc := json.NewEncoder(out)
	for {
		select {
		case <-ctx.Done():
			// SIGINT or parent cancellation: a clean stop, not a failure.
			return nil
		case ev, ok := <-events:
			if !ok {
				// The events channel closed: the stream ended. Drain the error
				// channel for a terminal cause, else treat it as a clean stop.
				return drainStreamErr(ctx, errCh)
			}
			if err := enc.Encode(ev); err != nil {
				return fmt.Errorf("encoding subscription event: %w", err)
			}
			if f, ok := out.(interface{ Flush() error }); ok {
				if err := f.Flush(); err != nil {
					return fmt.Errorf("flushing subscription event: %w", err)
				}
			}
		case err := <-errCh:
			if err == nil || errors.Is(err, context.Canceled) {
				return nil
			}
			return classifyStreamErr(err)
		}
	}
}

// drainStreamErr reads a terminal cause from a closed stream's error channel. A
// closed-or-nil error, or a context cancellation, is a clean stop; anything else
// is mapped to the transport class.
func drainStreamErr(ctx context.Context, errCh <-chan error) error {
	select {
	case err, ok := <-errCh:
		if !ok || err == nil || errors.Is(err, context.Canceled) {
			return nil
		}
		return classifyStreamErr(err)
	case <-ctx.Done():
		return nil
	}
}

// classifyStreamErr maps a terminal subscription error to the CLI error model so
// classifyExit routes it to the transport exit code. A [*stash.TransportError]
// already classifies; any other terminal cause (reconnect exhaustion) is wrapped
// in the CLI transportError so the same code results.
func classifyStreamErr(err error) error {
	var te *stash.TransportError
	if errors.As(err, &te) {
		return err
	}
	return &transportError{err: err}
}
