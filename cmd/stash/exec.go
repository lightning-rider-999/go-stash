package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

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

// classifyError maps a raw error from genqlient's MakeRequest into the SDK error
// model so the exit-code classifier (classifyExit) and the error envelope see a
// stable, typed shape. The raw c.GraphQL() client does not run the SDK's own
// classify (that wraps the typed operation wrappers), so this rebuilds the same
// typing from genqlient's three error shapes using only the SDK's public types:
//
//   - gqlerror.List (HTTP 200 carrying a GraphQL "errors" array) -> a
//     [*stash.GraphQLError]; [stash.ErrUnauthorized] is joined when a message
//     looks like an auth failure, so classifyExit maps it to auth.
//   - *graphql.HTTPError (a non-2xx status) -> a transport failure. A
//     [*stash.TransportError] cannot be built from outside the package (its cause
//     is unexported), so the genqlient error is wrapped in a CLI transportError
//     carrying the status code; classifyExit recognises both. 401/403 also join
//     [stash.ErrUnauthorized].
//   - any other error (network failure, cancelled context, decode error) -> a
//     CLI transportError with no status.
//
// The original error always stays in the chain via %w, so a caller can still
// errors.As/Is down to the genqlient cause.
func classifyError(err error) error {
	if err == nil {
		return nil
	}

	// HTTP 200 with a GraphQL errors array.
	var list gqlerror.List
	if errors.As(err, &list) {
		gqlErr := &stash.GraphQLError{Errors: list}
		if listLooksUnauthorized(list) {
			return fmt.Errorf("%w: %w", gqlErr, stash.ErrUnauthorized)
		}
		return gqlErr
	}

	// Non-2xx HTTP status.
	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) {
		te := &transportError{statusCode: httpErr.StatusCode, err: err}
		if httpErr.StatusCode == 401 || httpErr.StatusCode == 403 || listLooksUnauthorized(httpErr.Response.Errors) {
			return fmt.Errorf("%w: %w", te, stash.ErrUnauthorized)
		}
		return te
	}

	// Network failure, cancelled context, decode error: no HTTP status.
	return &transportError{err: err}
}

// transportError is the CLI's stand-in for a transport failure on the raw
// MakeRequest path. The SDK's [stash.TransportError] cannot be constructed from
// outside its package (its cause field is unexported), so this mirrors the
// public surface — a StatusCode and a wrapped cause — and classifyExit maps it,
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

// listLooksUnauthorized reports whether any GraphQL error in the list signals an
// authentication or authorisation failure, by its extensions code or message
// text. It mirrors the SDK's own heuristic, kept here because that function is
// unexported.
func listLooksUnauthorized(list gqlerror.List) bool {
	for _, ge := range list {
		if ge == nil {
			continue
		}
		if code, ok := ge.Extensions["code"].(string); ok {
			switch strings.ToUpper(code) {
			case "UNAUTHENTICATED", "UNAUTHORIZED", "FORBIDDEN":
				return true
			}
		}
		msg := strings.ToLower(ge.Message)
		if strings.Contains(msg, "not authenticated") ||
			strings.Contains(msg, "unauthorized") ||
			strings.Contains(msg, "unauthenticated") ||
			strings.Contains(msg, "forbidden") {
			return true
		}
	}
	return false
}
