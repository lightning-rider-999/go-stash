package stash

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// errorClient runs a Version call against an httptest server that responds with
// the given status code and body, then classifies the resulting error.
func errorClient(t *testing.T, status int, body string) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(WithURL(srv.URL), WithAPIKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	_, callErr := Version(context.Background(), c.GraphQL())
	return classify(callErr)
}

func TestErrorsGraphQLList(t *testing.T) {
	err := errorClient(t, 200, `{"data":null,"errors":[{"message":"unknown field","path":["version"]}]}`)
	if err == nil {
		t.Fatal("want error")
	}
	var gqlErr *GraphQLError
	if !errors.As(err, &gqlErr) {
		t.Fatalf("want *GraphQLError via errors.As, got %T", err)
	}
	if len(gqlErr.Errors) != 1 || gqlErr.Errors[0].Message != "unknown field" {
		t.Errorf("unexpected errors payload: %+v", gqlErr.Errors)
	}
	if !strings.Contains(gqlErr.Error(), "unknown field") {
		t.Errorf("Error() = %q, want it to mention the message", gqlErr.Error())
	}
	// The underlying gqlerror.List must remain reachable through the chain.
	if _, ok := errors.AsType[gqlerror.List](err); !ok {
		t.Error("underlying gqlerror.List not reachable via errors.As")
	}
}

func TestErrorsAuthFromGraphQL(t *testing.T) {
	err := errorClient(t, 200, `{"data":null,"errors":[{"message":"not authenticated","extensions":{"code":"UNAUTHENTICATED"}}]}`)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("auth-shaped GraphQL error not classified as ErrUnauthorized: %v", err)
	}
	// It is still a GraphQLError.
	if _, ok := errors.AsType[*GraphQLError](err); !ok {
		t.Errorf("auth GraphQL error should still be *GraphQLError, got %T", err)
	}
}

func TestErrorsAuthFromStatus(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		err := errorClient(t, status, `{"errors":[{"message":"denied"}]}`)
		if !errors.Is(err, ErrUnauthorized) {
			t.Errorf("status %d not classified as ErrUnauthorized: %v", status, err)
		}
		var te *TransportError
		if !errors.As(err, &te) {
			t.Errorf("status %d should surface a *TransportError, got %T", status, err)
		} else if te.StatusCode != status {
			t.Errorf("TransportError.StatusCode = %d, want %d", te.StatusCode, status)
		}
	}
}

func TestErrorsTransportNon2xx(t *testing.T) {
	err := errorClient(t, http.StatusInternalServerError, `{"errors":[{"message":"boom"}]}`)
	var te *TransportError
	if !errors.As(err, &te) {
		t.Fatalf("500 should surface a *TransportError, got %T", err)
	}
	if te.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", te.StatusCode, http.StatusInternalServerError)
	}
	if errors.Is(err, ErrUnauthorized) {
		t.Error("500 must not be classified as auth")
	}
}

func TestErrorsNetworkFailure(t *testing.T) {
	c, err := NewClient(WithURL("http://127.0.0.1:1/graphql"), WithAPIKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	_, callErr := Version(context.Background(), c.GraphQL())
	classified := classify(callErr)
	var te *TransportError
	if !errors.As(classified, &te) {
		t.Fatalf("connection refused should be a *TransportError, got %T", classified)
	}
	if te.StatusCode != 0 {
		t.Errorf("network failure StatusCode = %d, want 0", te.StatusCode)
	}
	// The original error must remain unwrappable.
	if errors.Unwrap(te) == nil {
		t.Error("TransportError must wrap the underlying network error")
	}
}

func TestErrorsClassifyNil(t *testing.T) {
	if got := classify(nil); got != nil {
		t.Errorf("classify(nil) = %v, want nil", got)
	}
}

func TestErrorsWrapChainUnwraps(t *testing.T) {
	base := gqlerror.List{{Message: "x"}}
	wrapped := fmt.Errorf("outer: %w", classify(base))
	if _, ok := errors.AsType[*GraphQLError](wrapped); !ok {
		t.Fatal("wrapped GraphQLError not reachable via errors.As")
	}
}

func TestEnvelopeSurfacesHTTPGraphQLErrors(t *testing.T) {
	// A non-2xx response that still carries a structured GraphQL "errors" array.
	// genqlient surfaces it on *graphql.HTTPError.Response.Errors; classify wraps
	// that in a *TransportError, and NewErrorEnvelope must lift the messages into
	// env.GraphQLErrors rather than dropping them.
	err := errorClient(t, http.StatusInternalServerError, `{"errors":[{"message":"boom"}]}`)
	if err == nil {
		t.Fatal("want error")
	}

	// The classified error must be a transport error carrying the 500 status.
	var te *TransportError
	if !errors.As(err, &te) {
		t.Fatalf("500 should surface a *TransportError, got %T", err)
	}
	if te.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", te.StatusCode)
	}

	// The embedded *graphql.HTTPError must remain reachable through the chain,
	// with its Response.Errors intact.
	var httpErr *graphql.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("embedded *graphql.HTTPError not reachable via errors.As, got %T", err)
	}
	if len(httpErr.Response.Errors) != 1 || httpErr.Response.Errors[0].Message != "boom" {
		t.Errorf("unexpected embedded GraphQL errors: %+v", httpErr.Response.Errors)
	}

	// The envelope must surface those GraphQL messages.
	env := NewErrorEnvelope(err)
	if len(env.GraphQLErrors) != 1 || env.GraphQLErrors[0] != "boom" {
		t.Errorf("env.GraphQLErrors = %v, want [boom]", env.GraphQLErrors)
	}
	if env.Code != "TRANSPORT" {
		t.Errorf("env.Code = %q, want TRANSPORT", env.Code)
	}
}

func TestErrorEnvelopeFromError(t *testing.T) {
	// GraphQL error -> messages populated.
	gqlErr := classify(gqlerror.List{{Message: "a"}, {Message: "b"}})
	env := NewErrorEnvelope(gqlErr)
	if len(env.GraphQLErrors) != 2 {
		t.Errorf("envelope GraphQLErrors = %v, want 2 messages", env.GraphQLErrors)
	}
	if env.Message == "" {
		t.Error("envelope Message empty")
	}

	// Auth error -> retryable false, message present.
	authEnv := NewErrorEnvelope(fmt.Errorf("wrap: %w", ErrUnauthorized))
	if authEnv.Retryable {
		t.Error("auth error should not be retryable")
	}

	// Nil -> zero envelope, no panic.
	if env := NewErrorEnvelope(nil); env.Message != "" {
		t.Errorf("NewErrorEnvelope(nil).Message = %q, want empty", env.Message)
	}
}
