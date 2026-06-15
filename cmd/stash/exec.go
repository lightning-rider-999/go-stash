package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/lightning-rider-999/go-stashapp/stash"
)

// runOperation executes one operation as raw GraphQL and writes its response
// data to out. It deliberately bypasses genqlient's typed operation functions:
// the request carries the operation document (spec.Query) and the caller's raw
// JSON variables, and the response data is captured as json.RawMessage. This
// preserves the present/absent/null three-state of mutation inputs that typed
// Go structs erase — the three-state input binding (Task 18) builds on this.
//
// Variables come from --input/stdin JSON only for now (Task 18 adds typed input
// binding). An operation with no input and no required arguments is sent with
// empty variables. A transport error is mapped into the SDK error model so the
// CLI reports a stable, agent-readable classification.
func runOperation(ctx context.Context, c *stash.Client, spec commandSpec, vars map[string]json.RawMessage, out io.Writer) error {
	var data json.RawMessage
	req := requestFor(spec, vars)
	resp := &graphql.Response{Data: &data}

	if err := c.GraphQL().MakeRequest(ctx, req, resp); err != nil {
		return classifyError(err)
	}
	return writeJSON(out, data)
}

// classifyError maps a raw error from genqlient's MakeRequest into a stable,
// agent-readable line through the SDK error model. The SDK's own classify is
// unexported and a *stash.TransportError cannot be fully reconstructed from
// outside the package (its cause field is unexported), so this handles the one
// shape it can rebuild — an HTTP 200 GraphQL "errors" array surfaces as a
// gqlerror.List, which becomes a [*stash.GraphQLError] — and passes every other
// shape (HTTP errors, network failures) to [stash.NewErrorEnvelope] as-is. The
// original error stays in the chain for callers that inspect it.
//
// TODO(Task 19): replace the formatted message with the frozen exit-code
// taxonomy and a JSON error envelope on stderr.
func classifyError(err error) error {
	if err == nil {
		return nil
	}

	var list gqlerror.List
	if errors.As(err, &list) {
		env := stash.NewErrorEnvelope(&stash.GraphQLError{Errors: list})
		return fmt.Errorf("stash %s: %s", env.Code, env.Message)
	}

	env := stash.NewErrorEnvelope(err)
	return fmt.Errorf("stash %s: %s", env.Code, env.Message)
}
