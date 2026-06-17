//go:build integration

package stash_test

import (
	"context"
	"testing"
	"time"

	"github.com/lightning-rider-999/go-stash/stash"
)

// TestLiveJobsSubscribeKeepalive opens jobsSubscribe over the live socket and
// asserts two things against the real server: the connection establishes (the
// connection_init/connection_ack handshake completes inside Subscriptions), and
// the client-side keepalive holds an idle socket open for a bounded window. With
// no job running the subscription is silent, so a drop would surface as the
// error channel firing or the stream channel closing; neither must happen before
// the deadline.
//
// The window is short and bounded by a context deadline so the test cannot hang
// CI. It does not wait for a real event — enqueuing a job to drive one is out of
// scope — it validates that an idle live subscription stays up.
func TestLiveJobsSubscribeKeepalive(t *testing.T) {
	c := liveClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	wsClient, errChan, err := c.Subscriptions(ctx)
	if err != nil {
		t.Fatalf("Subscriptions handshake against live instance: %v", err)
	}
	defer func() { _ = wsClient.Close() }()

	jobsCh, subID, err := stash.JobsSubscribe(ctx, wsClient)
	if err != nil {
		t.Fatalf("JobsSubscribe against live instance: %v", err)
	}
	t.Logf("jobsSubscribe established (subscription id %q); holding idle socket open", subID)

	// Hold for a few seconds; a healthy idle socket neither errors nor closes.
	// Forcing a short keepalive interval here would need an internal hook, so we
	// rely on the default interval keeping the connection up across the window.
	const hold = 5 * time.Second
	deadline := time.NewTimer(hold)
	defer deadline.Stop()

	for {
		select {
		case <-deadline.C:
			t.Logf("idle jobsSubscribe held open for %s without a drop", hold)
			return
		case err := <-errChan:
			if err != nil {
				t.Fatalf("live subscription errored while idle: %v", err)
			}
		case resp, ok := <-jobsCh:
			if !ok {
				t.Fatal("live jobsSubscribe stream closed while idle; expected it to stay open")
			}
			// A job event during the window is fine and not an error; log it and
			// keep holding to confirm the socket survives delivery.
			if len(resp.Errors) > 0 {
				t.Fatalf("live jobsSubscribe returned GraphQL errors: %v", resp.Errors)
			}
			t.Logf("received a job event while holding: %+v", resp.Data)
		case <-ctx.Done():
			t.Fatalf("context expired before the hold window elapsed: %v", ctx.Err())
		}
	}
}

// TestLiveLoggingAndScanSubscribeConnect is a connect-only check for the other
// two subscriptions: each must establish over the live socket without an
// immediate error. It does not wait for events (logging is sparse and a scan may
// never complete during the test), only that the subscribe frame is accepted.
func TestLiveLoggingAndScanSubscribeConnect(t *testing.T) {
	c := liveClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	wsClient, errChan, err := c.Subscriptions(ctx)
	if err != nil {
		t.Fatalf("Subscriptions handshake against live instance: %v", err)
	}
	defer func() { _ = wsClient.Close() }()

	if _, _, err := stash.LoggingSubscribe(ctx, wsClient); err != nil {
		t.Fatalf("LoggingSubscribe against live instance: %v", err)
	}
	if _, _, err := stash.ScanCompleteSubscribe(ctx, wsClient); err != nil {
		t.Fatalf("ScanCompleteSubscribe against live instance: %v", err)
	}

	// A brief settle window: an immediate connection-level error would surface
	// here. Silence means both subscribe frames were accepted.
	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("connection errored right after subscribing: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
	}
	t.Log("loggingSubscribe and scanCompleteSubscribe both connected over the live socket")
}
