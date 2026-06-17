package stash

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// ErrUnauthorized marks an authentication or authorisation failure. It is set
// in the wrap chain for a 401 or 403 HTTP status and for a GraphQL error whose
// extensions or message indicate an auth problem. Test with errors.Is.
var ErrUnauthorized = errors.New("stash: unauthorized")

// ErrNoURL is returned by [NewClient] when no Stash URL is configured (neither a
// [WithURL] option nor the STASHAPP_URL environment variable). It is a
// configuration mistake the caller can fix, distinct from a transport or server
// failure, so the CLI maps it to a usage exit code. Test with errors.Is.
var ErrNoURL = errors.New("stash: no URL configured (set STASHAPP_URL or use WithURL)")

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
	// retryable records, at classification time, whether the failure is
	// genuinely transient (a network/timeout/connection problem, or a 5xx/429
	// server status) as opposed to a deterministic decode or protocol error that
	// would recur on every attempt. It is read by [NewErrorEnvelope].
	retryable bool
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

// Retryable reports whether retrying the operation could plausibly succeed: it
// is true for genuinely transient failures (network, timeout, or connection
// errors, and 5xx/429 server responses) and false for deterministic ones (a
// decode or protocol error, or a non-retryable 4xx).
func (e *TransportError) Retryable() bool { return e.retryable }

// NewTransportError builds a [*TransportError] for the given HTTP status code
// (0 when no response was received) and underlying cause. The retryable flag is
// derived from the inputs: a 5xx or 429 status, or a transient network/timeout
// cause, is retryable; everything else is not. It lets code outside this
// package construct the typed error consistently with the internal classifier.
func NewTransportError(statusCode int, cause error) *TransportError {
	return &TransportError{
		StatusCode: statusCode,
		retryable:  transportRetryable(statusCode, cause),
		err:        cause,
	}
}

// transportRetryable decides whether a transport failure is worth retrying.
// With an HTTP status it keys off the status class; without one (StatusCode 0)
// it inspects the cause: a network or timeout error is transient and retryable,
// while a decode/protocol error or a cancelled context is not.
func transportRetryable(statusCode int, cause error) bool {
	if statusCode != 0 {
		return statusCode == http.StatusTooManyRequests || statusCode >= 500
	}
	// A user cancellation is not a failure to retry; a deadline may be.
	if errors.Is(cause, context.Canceled) {
		return false
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return true
	}
	// Network-level failures (refused connection, reset, timeout) are transient.
	var netErr net.Error
	return errors.As(cause, &netErr)
}

// Classify maps a raw error returned by a genqlient operation (or an
// equivalent hand-built request path) into this package's typed error model.
// It is the exported entry point so callers such as the CLI need not
// reimplement the mapping; the behaviour is identical to the package-internal
// [classify].
func Classify(err error) error { return classify(err) }

// classify maps a raw error returned by a genqlient operation into the typed
// error model. It distinguishes the shapes that genqlient produces:
//
//   - A gqlerror.List, returned for an HTTP 200 response that carries a GraphQL
//     "errors" array. This becomes a [*GraphQLError]. If any error looks like an
//     auth failure, [ErrUnauthorized] is joined into the chain.
//   - A *graphql.HTTPError, returned for a non-2xx status. This becomes a
//     [*TransportError] carrying the status code, marked retryable for a 5xx or
//     429, plus [ErrUnauthorized] for 401 or 403, and any embedded GraphQL
//     errors stay reachable through the wrapped cause.
//   - context.Canceled and context.DeadlineExceeded, special-cased: a cancel is
//     a non-retryable [*TransportError] (a user cancel is not a failure to
//     retry), a deadline a retryable one.
//   - Any other error (a network failure or a decode error), wrapped in a
//     [*TransportError] with StatusCode 0, retryable only when it is a transient
//     network/timeout failure rather than a deterministic decode error.
//
// A nil input returns nil.
func classify(err error) error {
	if err == nil {
		return nil
	}

	// HTTP 200 with a GraphQL errors array: concrete type gqlerror.List.
	if list, ok := errors.AsType[gqlerror.List](err); ok {
		gqlErr := &GraphQLError{Errors: list}
		if listLooksUnauthorized(list) {
			return joinUnauthorized(gqlErr)
		}
		return gqlErr
	}

	// Non-2xx status: *graphql.HTTPError with a status code and, often, an
	// embedded GraphQL errors array.
	if httpErr, ok := errors.AsType[*graphql.HTTPError](err); ok {
		te := &TransportError{
			StatusCode: httpErr.StatusCode,
			retryable:  transportRetryable(httpErr.StatusCode, err),
			err:        err,
		}
		if httpErr.StatusCode == http.StatusUnauthorized ||
			httpErr.StatusCode == http.StatusForbidden ||
			listLooksUnauthorized(httpErr.Response.Errors) {
			return joinUnauthorized(te)
		}
		return te
	}

	// Everything else is a transport or network failure with no HTTP status;
	// transportRetryable special-cases context cancel/deadline and distinguishes
	// transient network failures from deterministic decode errors.
	return &TransportError{retryable: transportRetryable(0, err), err: err}
}

// joinUnauthorized wraps base so that both errors.As(base's type) and
// errors.Is(ErrUnauthorized) succeed.
func joinUnauthorized(base error) error {
	return fmt.Errorf("%w: %w", base, ErrUnauthorized)
}

// listLooksUnauthorized reports whether any error in the list signals an
// authentication or authorisation failure. It prefers the structured
// extensions "code": when an error carries one, that code is authoritative and
// the message text is not consulted, so an arbitrary message containing
// "forbidden" cannot override a non-auth code. Only when an error has no code
// does it fall back to matching the message text, to catch servers that report
// auth failures without a code.
func listLooksUnauthorized(list gqlerror.List) bool {
	for _, ge := range list {
		if ge == nil {
			continue
		}
		if code, ok := ge.Extensions["code"].(string); ok {
			switch strings.ToUpper(code) {
			case "UNAUTHENTICATED", "UNAUTHORIZED", "FORBIDDEN":
				return true
			default:
				// A code is present but not an auth code: trust it and do not
				// second-guess via the message text for this error.
				continue
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
	// succeed. It is true for genuinely transient transport failures (network,
	// timeout, or connection errors, and 5xx/429 server responses) and false for
	// deterministic ones: decode/protocol errors, non-retryable 4xx, GraphQL
	// errors, auth failures, and a cancelled context.
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

	if gqlErr, ok := errors.AsType[*GraphQLError](err); ok {
		env.Code = "GRAPHQL"
		env.GraphQLErrors = gqlErr.Messages()
	}

	if te, ok := errors.AsType[*TransportError](err); ok {
		env.Code = "TRANSPORT"
		env.Retryable = te.retryable
		// A non-2xx response can still carry a structured GraphQL "errors"
		// array (genqlient surfaces it on graphql.HTTPError.Response.Errors).
		// Surface those messages too rather than dropping them, but only when a
		// *GraphQLError did not already populate them above.
		if len(env.GraphQLErrors) == 0 {
			if httpErr, ok := errors.AsType[*graphql.HTTPError](te.err); ok && len(httpErr.Response.Errors) > 0 {
				msgs := make([]string, len(httpErr.Response.Errors))
				for i, ge := range httpErr.Response.Errors {
					msgs[i] = ge.Message
				}
				env.GraphQLErrors = msgs
			}
		}
	}

	if errors.Is(err, ErrUnauthorized) {
		env.Code = "UNAUTHORIZED"
		env.Retryable = false
	}

	return env
}
