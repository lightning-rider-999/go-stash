package mockgql_test

import (
	"context"
	"testing"
	"time"

	"github.com/lightning-rider-999/go-stashapp/internal/mockgql"
	"github.com/lightning-rider-999/go-stashapp/stash"
)

// TestHTTPResponseAndRecording drives a real stash client against the mock over
// HTTP: the canned Version response decodes into the canonical type, and the
// mock records the operation name and the ApiKey header the client sent.
func TestHTTPResponseAndRecording(t *testing.T) {
	srv := mockgql.New(t,
		mockgql.WithResponse("Version",
			`{"version":{"version":"v0.31.1","hash":"deadbeef","build_time":"2025-01-02T03:04:05Z"}}`),
	)

	c, err := stash.NewClient(stash.WithURL(srv.URL()), stash.WithAPIKey("secret-key"))
	if err != nil {
		t.Fatal(err)
	}

	info, err := c.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if info.Version != "v0.31.1" {
		t.Errorf("Version = %q, want v0.31.1", info.Version)
	}
	if info.Hash != "deadbeef" {
		t.Errorf("Hash = %q, want deadbeef", info.Hash)
	}

	req, ok := srv.LastRequest()
	if !ok {
		t.Fatal("no request recorded")
	}
	if req.OpName != "Version" {
		t.Errorf("recorded OpName = %q, want Version", req.OpName)
	}
	if req.APIKey != "secret-key" {
		t.Errorf("recorded ApiKey = %q, want secret-key", req.APIKey)
	}
}

// TestRawResponseSurfacesGraphQLError confirms a verbatim error envelope reaches
// the client through the typed error model.
func TestRawResponseSurfacesGraphQLError(t *testing.T) {
	srv := mockgql.New(t,
		mockgql.WithRawResponse("Version", 200, `{"data":null,"errors":[{"message":"boom"}]}`),
	)
	c, err := stash.NewClient(stash.WithURL(srv.URL()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Version(context.Background()); err == nil {
		t.Fatal("want an error from the GraphQL error envelope")
	}
}

// TestSubscriptionStreamsCannedEvents drives the WebSocket half: the mock
// upgrades, acknowledges connection_init (recording the forwarded ApiKey), and
// streams the canned jobsSubscribe events to a real stash subscription client.
func TestSubscriptionStreamsCannedEvents(t *testing.T) {
	srv := mockgql.New(t,
		mockgql.WithSubscription("JobsSubscribe",
			`{"jobsSubscribe":{"type":"ADD","job":{"id":"j1","status":"RUNNING","subTasks":null,"description":"scan","progress":0.5,"startTime":null,"endTime":null,"addTime":"2025-01-01T00:00:00Z","error":null}}}`),
	)

	c, err := stash.NewClient(stash.WithURL(srv.URL()), stash.WithAPIKey("ws-key"))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsClient, errChan, err := c.Subscriptions(ctx)
	if err != nil {
		t.Fatalf("Subscriptions: %v", err)
	}
	defer func() { _ = wsClient.Close() }()

	if params := srv.ConnInitParams(); params == nil || params["ApiKey"] != "ws-key" {
		t.Errorf("connection_init params = %v, want ApiKey=ws-key", params)
	}

	jobsCh, _, err := stash.JobsSubscribe(ctx, wsClient)
	if err != nil {
		t.Fatalf("JobsSubscribe: %v", err)
	}

	gotJob := false
	for resp := range jobsCh {
		if len(resp.Errors) > 0 {
			t.Fatalf("jobs errors: %v", resp.Errors)
		}
		if resp.Data != nil && resp.Data.JobsSubscribe != nil && resp.Data.JobsSubscribe.Job != nil {
			if resp.Data.JobsSubscribe.Job.Id != "j1" {
				t.Errorf("job id = %q, want j1", resp.Data.JobsSubscribe.Job.Id)
			}
			gotJob = true
		}
	}
	if !gotJob {
		t.Error("did not receive a JobsSubscribe event")
	}

	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("unexpected errChan error: %v", err)
		}
	default:
	}
}

// TestIdleSubscriptionHoldsSocketOpen confirms an idle subscription keeps the
// socket open without completing: the stream stays open until the test cancels
// it, which is the hermetic analogue of the live keepalive check.
func TestIdleSubscriptionHoldsSocketOpen(t *testing.T) {
	srv := mockgql.New(t,
		mockgql.WithIdleSubscription("ScanCompleteSubscribe"),
	)
	c, err := stash.NewClient(stash.WithURL(srv.URL()))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsClient, _, err := c.Subscriptions(ctx)
	if err != nil {
		t.Fatalf("Subscriptions: %v", err)
	}
	defer func() { _ = wsClient.Close() }()

	scanCh, _, err := stash.ScanCompleteSubscribe(ctx, wsClient)
	if err != nil {
		t.Fatalf("ScanCompleteSubscribe: %v", err)
	}

	// The stream must stay open (no complete) for a short window; a premature
	// close would deliver a closed channel before the deadline.
	select {
	case _, ok := <-scanCh:
		if !ok {
			t.Fatal("idle subscription closed early; want it held open")
		}
		t.Fatal("idle subscription delivered an event; want none")
	case <-time.After(250 * time.Millisecond):
		// Held open as intended.
	}
}
