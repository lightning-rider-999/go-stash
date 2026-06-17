package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"

	"github.com/lightning-rider-999/go-stashapp/stash"
)

// runOperation executes one operation as raw GraphQL and writes its response
// data to out. It deliberately bypasses genqlient's typed operation functions:
// the request carries the operation document (spec.Query) and the caller's raw
// JSON variables, and the response data is captured as json.RawMessage. This
// preserves the present/absent/null three-state of mutation inputs that typed
// Go structs erase.
//
// Variables come from the resolved --input/stdin JSON plus the convenience flags
// (see input.go's resolveVariables) and are bound as raw JSON, so the
// present/absent/null three-state survives to the wire. An operation with no
// input and no required arguments is sent with empty variables. A transport
// error is mapped into the SDK error model so the CLI reports a stable,
// agent-readable classification.
//
// format selects the output rendering (--output); writeOutput redacts API keys
// and renders the response data. An empty format defaults to json.
func runOperation(ctx context.Context, c *stash.Client, spec commandSpec, vars map[string]json.RawMessage, format string, out io.Writer) error {
	var data json.RawMessage
	req := requestFor(spec, vars)
	resp := &graphql.Response{Data: &data}

	if err := c.GraphQL().MakeRequest(ctx, req, resp); err != nil {
		return classifyError(err)
	}
	return writeOutput(out, format, spec, data)
}

// classifyError normalises a raw error from the genqlient MakeRequest path into
// the SDK's typed error model so the exit-code classifier (classifyExit) and the
// error envelope see a stable, typed shape. The raw c.GraphQL() client does not
// run the SDK's classifier (that wraps the typed operation wrappers), so this
// hands the genqlient error straight to [stash.Classify], the package's exported
// entry point: a gqlerror.List becomes a [*stash.GraphQLError], a non-2xx
// *graphql.HTTPError becomes a [*stash.TransportError] (with [stash.ErrUnauthorized]
// joined for 401/403 or an auth-looking body), and any other cause becomes a
// [*stash.TransportError] with no status. The CLI no longer reimplements that
// mapping or the auth heuristic; a server upgrade that changes the shapes is the
// SDK's to absorb, not the CLI's to drift against.
//
// The original error stays in the chain via %w (inside Classify), so a caller
// can still errors.As/Is down to the genqlient cause. A nil error returns nil.
func classifyError(err error) error {
	return stash.Classify(err)
}

// transportError is the CLI's stand-in for a transport failure on a path that
// does not flow through [stash.Classify] — notably the streaming subscription
// fallback (see stream.go) — where the originating cause is not one of
// genqlient's request-shaped errors. The SDK's [stash.TransportError] is the
// preferred type (construct it with [stash.NewTransportError]); this mirrors its
// public surface — a StatusCode and a wrapped cause — so classifyExit maps it,
// like [stash.TransportError], to the transport exit code.
type transportError struct {
	statusCode int
	err        error
}

// Error describes the transport failure, including the status code when known.
func (e *transportError) Error() string {
	if e.statusCode != 0 {
		return fmt.Sprintf("transport error (status %d): %v", e.statusCode, e.err)
	}
	return fmt.Sprintf("transport error: %v", e.err)
}

// Unwrap returns the underlying cause so the genqlient error stays reachable.
func (e *transportError) Unwrap() error { return e.err }
