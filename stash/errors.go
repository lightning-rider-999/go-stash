package stash

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// ErrUnauthorized marks an authentication or authorisation failure. It is set
// in the wrap chain for a 401 or 403 HTTP status and for a GraphQL error whose
// extensions or message indicate an auth problem. Test with errors.Is.
var ErrUnauthorized = errors.New("stash: unauthorized")

// GraphQLError reports that the server executed the request but returned one or
// more GraphQL errors (an HTTP 200 response carrying an "errors" array). It
// wraps the genqlient/gqlparser error list so the original locations, paths,
// and extensions remain reachable via errors.As.
type GraphQLError struct {
	// Errors is the list returned by the server, in order.
	Errors gqlerror.List
}

// Error summarises the contained GraphQL errors by joining their messages.
func (e *GraphQLError) Error() string {
	switch len(e.Errors) {
	case 0:
		return "stash: graphql error"
	case 1:
		return "stash: graphql error: " + e.Errors[0].Message
	default:
		msgs := make([]string, len(e.Errors))
		for i, ge := range e.Errors {
			msgs[i] = ge.Message
		}
		return fmt.Sprintf("stash: %d graphql errors: %s", len(e.Errors), strings.Join(msgs, "; "))
	}
}

// Unwrap exposes the underlying gqlerror.List so its own As and Is methods, and
// the wrapped per-error causes, stay reachable through the chain.
func (e *GraphQLError) Unwrap() error { return e.Errors }

// Messages returns the message string of each contained GraphQL error.
func (e *GraphQLError) Messages() []string {
	msgs := make([]string, len(e.Errors))
	for i, ge := range e.Errors {
		msgs[i] = ge.Message
	}
	return msgs
}

// TransportError reports a failure that is not a well-formed GraphQL error
// response: a network or connection failure, a non-2xx HTTP status, or a body
// that could not be decoded. StatusCode is the HTTP status when one was
// received, or 0 for a failure that occurred before any response (for example a
// refused connection or a cancelled context).
type TransportError struct {
	// StatusCode is the HTTP status code, or 0 when no response was received.
	StatusCode int
	// err is the underlying cause.
	err error
}

// Error describes the transport failure, including the status code when known.
func (e *TransportError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("stash: transport error (status %d): %v", e.StatusCode, e.err)
	}
	return fmt.Sprintf("stash: transport error: %v", e.err)
}

// Unwrap returns the underlying cause.
func (e *TransportError) Unwrap() error { return e.err }

// classify maps a raw error returned by a genqlient operation into the typed
// error model. It distinguishes three shapes that genqlient produces:
//
//   - A gqlerror.List, returned for an HTTP 200 response that carries a GraphQL
//     "errors" array. This becomes a [*GraphQLError]. If any error looks like an
//     auth failure, [ErrUnauthorized] is joined into the chain.
//   - A *graphql.HTTPError, returned for a non-2xx status. This becomes a
//     [*TransportError] carrying the status code, plus [ErrUnauthorized] for 401
//     or 403, and any embedded GraphQL errors as the wrapped cause.
//   - Any other error (a network failure, a cancelled context, a decode error),
//     wrapped in a [*TransportError] with StatusCode 0.
//
// A nil input returns nil.
func classify(err error) error {
	if err == nil {
		return nil
	}

	// HTTP 200 with a GraphQL errors array: concrete type gqlerror.List.
	var list gqlerror.List
	if errors.As(err, &list) {
		gqlErr := &GraphQLError{Errors: list}
		if listLooksUnauthorized(list) {
			return joinUnauthorized(gqlErr)
		}
		return gqlErr
	}

	// Non-2xx status: *graphql.HTTPError with a status code and, often, an
	// embedded GraphQL errors array.
	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) {
		te := &TransportError{StatusCode: httpErr.StatusCode, err: err}
		if httpErr.StatusCode == 401 || httpErr.StatusCode == 403 || listLooksUnauthorized(httpErr.Response.Errors) {
			return joinUnauthorized(te)
		}
		return te
	}

	// Everything else is a transport or network failure with no HTTP status.
	return &TransportError{err: err}
}

// joinUnauthorized wraps base so that both errors.As(base's type) and
// errors.Is(ErrUnauthorized) succeed.
func joinUnauthorized(base error) error {
	return fmt.Errorf("%w: %w", base, ErrUnauthorized)
}

// listLooksUnauthorized reports whether any error in the list signals an
// authentication or authorisation failure, judged by its extensions code or, as
// a fallback, its message text.
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

// ErrorEnvelope is the JSON-marshalable shape the CLI emits for a failed
// command. It is intentionally minimal here: the Code field is a placeholder
// and the exit-code taxonomy is wired by the CLI task. The transport and error
// model in this package populate the rest.
type ErrorEnvelope struct {
	// Code is the stable error code. The CLI task fills the frozen taxonomy;
	// here it is left as a placeholder classification string.
	Code string `json:"code"`
	// Message is the human-readable summary.
	Message string `json:"message"`
	// GraphQLErrors holds the individual server error messages, when the cause
	// was a [*GraphQLError].
	GraphQLErrors []string `json:"graphqlErrors,omitempty"`
	// Field names the offending input field when one can be identified. It is
	// left empty until a caller populates it.
	Field string `json:"field,omitempty"`
	// Retryable indicates whether retrying the operation could plausibly
	// succeed. It is false for client and auth errors and true for transient
	// transport failures that carry no HTTP status.
	Retryable bool `json:"retryable"`
}

// NewErrorEnvelope maps a Go error into an [ErrorEnvelope]. The Code field is a
// coarse placeholder classification; the CLI task replaces it with the frozen
// exit-code taxonomy. A nil error yields a zero envelope.
func NewErrorEnvelope(err error) ErrorEnvelope {
	if err == nil {
		return ErrorEnvelope{}
	}

	env := ErrorEnvelope{
		Code:    "ERROR",
		Message: err.Error(),
	}

	var gqlErr *GraphQLError
	if errors.As(err, &gqlErr) {
		env.Code = "GRAPHQL"
		env.GraphQLErrors = gqlErr.Messages()
	}

	var te *TransportError
	if errors.As(err, &te) {
		env.Code = "TRANSPORT"
		// A failure with no HTTP status is a transient transport problem and
		// may be worth retrying; a non-2xx status is a definite server answer.
		env.Retryable = te.StatusCode == 0
	}

	if errors.Is(err, ErrUnauthorized) {
		env.Code = "UNAUTHORIZED"
		env.Retryable = false
	}

	return env
}
