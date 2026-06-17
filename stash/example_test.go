package stash_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Khan/genqlient/graphql"

	"github.com/lightning-rider-999/go-stash/stash"
)

// ExampleNewClient builds a client from explicit options. The URL is normalised
// to address the GraphQL endpoint, so passing the base UI URL is enough.
func ExampleNewClient() {
	c, err := stash.NewClient(
		stash.WithURL("http://stash.local:9999"),
		stash.WithAPIKey("your-api-key"),
		stash.WithTimeout(30*time.Second),
	)
	if err != nil {
		fmt.Println("config error:", err)
		return
	}
	fmt.Println(c.Endpoint())
	// Output: http://stash.local:9999/graphql
}

// ExampleNewClient_environment shows that the URL and API key fall back to the
// STASHAPP_URL and STASHAPP_API_KEY environment variables when no option sets
// them. Here an explicit option is still used so the example is deterministic.
func ExampleNewClient_environment() {
	c, err := stash.NewClient(stash.WithURL("https://stash.example.com/"))
	if err != nil {
		fmt.Println("config error:", err)
		return
	}
	// The subscription endpoint is derived from the same URL, mapping the
	// scheme to wss and preserving the path.
	fmt.Println(c.WebSocketURL())
	// Output: wss://stash.example.com/graphql
}

// ExampleClient_GraphQL runs a generated operation. Client.GraphQL returns the
// genqlient client that every generated function takes as its second argument.
// The call is wired exactly as it would run against a live server; it is not
// executed here, so no Output line asserts a server response.
func ExampleClient_GraphQL() {
	c, err := stash.NewClient(stash.WithURL("http://stash.local:9999"))
	if err != nil {
		return
	}

	ctx := context.Background()

	// Find the first page of scenes matching a free-text query. The filter and
	// scene-filter arguments are the operation's own typed inputs.
	perPage := 25
	resp, err := stash.FindScenes(ctx, c.GraphQL(), nil, nil, &stash.FindFilterType{
		Q:        new("sunset"),
		Per_page: new(perPage),
	})
	if err != nil {
		// See ExampleNewErrorEnvelope for classifying the error.
		return
	}

	for _, scene := range resp.FindScenes.Scenes {
		title := "(untitled)"
		if scene.Title != nil { // Title is an optional *string.
			title = *scene.Title
		}
		fmt.Println(scene.Id, title)
	}
}

// ExampleClient_Version performs the version handshake: it asks the server for
// its build identity. The call is shown without execution against a live
// server, so it carries no Output line.
func ExampleClient_Version() {
	c, err := stash.NewClient(stash.WithURL("http://stash.local:9999"))
	if err != nil {
		return
	}

	info, err := c.Version(context.Background())
	if err != nil {
		return
	}
	fmt.Printf("Stash %s (%s)\n", info.Version, info.Hash)
}

// ExampleClient_CheckCompatibility reports whether the server's release matches
// the schema version this library was generated against. A mismatch is not an
// error: it returns compatible=false with the server's reported version so the
// caller decides how to proceed.
func ExampleClient_CheckCompatibility() {
	c, err := stash.NewClient(stash.WithURL("http://stash.local:9999"))
	if err != nil {
		return
	}

	compatible, server, err := c.CheckCompatibility(context.Background())
	if err != nil {
		return
	}
	if !compatible {
		fmt.Printf("server %s differs from the generated schema\n", server.Version)
	}
}

// ExampleBatch runs work over a slice of items with bounded concurrency. At
// most three calls run at once; the first error cancels the rest and is
// returned. There is no retry, by design.
func ExampleBatch() {
	c, err := stash.NewClient(stash.WithURL("http://stash.local:9999"))
	if err != nil {
		return
	}

	ids := []string{"1", "2", "3", "4", "5"}

	err = stash.Batch(context.Background(), 3, ids, func(ctx context.Context, id string) error {
		_, opErr := stash.FindScene(ctx, c.GraphQL(), new(id), nil)
		return opErr
	})
	if err != nil {
		fmt.Println("batch failed:", err)
	}
}

// ExampleBatchResults collects a result per item, returned in input order
// regardless of completion order. The concurrency, cancellation, and no-retry
// behaviour match Batch.
func ExampleBatchResults() {
	doubled, err := stash.BatchResults(
		context.Background(),
		4,
		[]int{1, 2, 3, 4},
		func(_ context.Context, n int) (int, error) {
			return n * 2, nil
		},
	)
	if err != nil {
		return
	}
	fmt.Println(doubled)
	// Output: [2 4 6 8]
}

// ExampleSubscribe streams typed events from a subscription and reconnects on a
// dropped connection. The subscribe callback adapts a generated subscription
// (here JobsSubscribe) into the channel shape Subscribe expects, discarding the
// subscription id the generated function also returns. The stream is not run
// against a live server, so the example carries no Output line.
func ExampleSubscribe() {
	c, err := stash.NewClient(stash.WithURL("http://stash.local:9999"))
	if err != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, errs := stash.Subscribe(ctx, c,
		func(ctx context.Context, wc graphql.WebSocketClient) (<-chan stash.JobsSubscribeWsResponse, error) {
			ch, _, subErr := stash.JobsSubscribe(ctx, wc)
			return ch, subErr
		},
		stash.WithMaxReconnects(5),
		stash.WithBackoff(time.Second, 30*time.Second),
	)

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Data != nil && ev.Data.JobsSubscribe.Job != nil {
				// Progress is a nullable Float in the Job SDL; a job that has not
				// reported it yet leaves the pointer nil, shown here as 0.
				var progress float64
				if p := ev.Data.JobsSubscribe.Job.Progress; p != nil {
					progress = *p
				}
				fmt.Printf("job %s: %s (%.0f%%)\n",
					ev.Data.JobsSubscribe.Job.Id,
					ev.Data.JobsSubscribe.Type,
					progress)
			}
		case err := <-errs:
			if err != nil {
				fmt.Println("subscription ended:", err)
			}
			return
		}
	}
}

// ExampleNewErrorEnvelope classifies a returned error and inspects the error
// types the SDK produces. A *GraphQLError carries the server's message list; a
// *TransportError carries the HTTP status; ErrUnauthorized is matched with
// errors.Is.
func ExampleNewErrorEnvelope() {
	c, err := stash.NewClient(stash.WithURL("http://stash.local:9999"))
	if err != nil {
		return
	}

	_, err = c.Version(context.Background())
	if err == nil {
		return
	}

	switch {
	case errors.Is(err, stash.ErrUnauthorized):
		fmt.Println("check the API key")
	default:
		var gqlErr *stash.GraphQLError
		var transportErr *stash.TransportError
		switch {
		case errors.As(err, &gqlErr):
			fmt.Println("server rejected the query:", gqlErr.Messages())
		case errors.As(err, &transportErr):
			fmt.Println("transport failure, HTTP status:", transportErr.StatusCode)
		}
	}

	// NewErrorEnvelope maps any error to the JSON shape the CLI emits.
	env := stash.NewErrorEnvelope(err)
	fmt.Println("retryable:", env.Retryable)
}

// ExampleWithHTTPClient supplies a custom *http.Client. Its own timeout and
// settings are kept; only the transport is wrapped so the ApiKey header is
// still injected.
func ExampleWithHTTPClient() {
	hc := &http.Client{Timeout: 10 * time.Second}

	c, err := stash.NewClient(
		stash.WithURL("http://stash.local:9999"),
		stash.WithAPIKey("your-api-key"),
		stash.WithHTTPClient(hc),
		stash.WithLogger(slog.Default()),
	)
	if err != nil {
		return
	}
	_ = c.HTTPClient()
}
