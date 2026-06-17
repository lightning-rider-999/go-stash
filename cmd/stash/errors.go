package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lightning-rider-999/go-stashapp/internal/exitcode"
	"github.com/lightning-rider-999/go-stashapp/internal/redact"
	"github.com/lightning-rider-999/go-stashapp/stash"
)

// ExitCode is one entry of the frozen CLI exit-code taxonomy: a stable name and
// the process exit status that pairs with it. The name is also the value of the
// error envelope's "code" field, so a caller reading the JSON on stderr and a
// caller reading $? see the same classification. The (name, integer) pairs are
// FROZEN — never renumber them; agents and schema/catalog.json depend on them.
//
// The values come from the shared taxonomy in internal/exitcode, which the
// catalog generator also reads, so the names the CLI exits with and the names
// the catalog advertises cannot drift.
type ExitCode struct {
	// Name is the stable, machine-readable classification (e.g. "auth").
	Name string
	// Code is the process exit status for this classification.
	Code int
}

// fromShared adapts a shared taxonomy code to cmd/stash's ExitCode shape (Name
// + Code), the form this package's runtime and the error envelope use.
func fromShared(c exitcode.Code) ExitCode {
	return ExitCode{Name: c.Name, Code: c.Status}
}

// The frozen exit-code taxonomy. The integers are a contract:
//
//	0  ok                  the command succeeded
//	1  internal            an unexpected, panic-level failure (reserved)
//	2  usage               bad flags/arguments — a cobra usage error
//	3  auth                authentication or authorisation failure
//	4  transport           network failure or non-2xx HTTP status
//	5  validation          the server rejected the input as invalid
//	6  server-fault        a server-side GraphQL error that is not the caller's fault
//	7  not-found           the requested object does not exist
//	8  destructive-refused a destructive op without the required confirmation
//	9  job-failed          an async job finished in a failed state
//	10 still-running       --wait timed out with the job still running
//	11 unconfirmed         a confirmation prompt was declined or could not be
//	                       shown, or a --wait outcome could not be settled
//
// 1 is reserved for an internal error so a genuine taxonomy code is never
// confused with an unexpected crash. The names mirror schema/catalog.json's
// per-command exitCodes arrays exactly, so the catalog and the runtime agree.
var (
	ExitOK                 = fromShared(exitcode.OK)
	ExitInternal           = fromShared(exitcode.Internal)
	ExitUsage              = fromShared(exitcode.Usage)
	ExitAuth               = fromShared(exitcode.Auth)
	ExitTransport          = fromShared(exitcode.Transport)
	ExitValidation         = fromShared(exitcode.Validation)
	ExitServerFault        = fromShared(exitcode.ServerFault)
	ExitNotFound           = fromShared(exitcode.NotFound)
	ExitDestructiveRefused = fromShared(exitcode.DestructiveRefused)
	ExitJobFailed          = fromShared(exitcode.JobFailed)
	ExitStillRunning       = fromShared(exitcode.StillRunning)
	ExitUnconfirmed        = fromShared(exitcode.Unconfirmed)
)

// usageError marks an error as a CLI usage problem (bad flags, wrong argument
// count, unknown command), so classifyExit maps it to ExitUsage rather than a
// transport/server class. cobra's own usage failures are wrapped in this by the
// root command; the input layer can return one too.
type usageError struct{ err error }

// newUsageError wraps err as a usage error. A nil err yields nil.
func newUsageError(err error) error {
	if err == nil {
		return nil
	}
	return &usageError{err: err}
}

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

// classifyExit maps a command failure to its frozen exit code. The order is
// significant: the most specific classification wins.
//
//   - A usage error (bad flags/args) -> usage. Checked first: a usage problem is
//     never a server or transport condition.
//   - An auth failure (errors.Is(err, stash.ErrUnauthorized)) -> auth.
//   - A *stash.TransportError (network failure or non-2xx status) -> transport.
//   - A *stash.GraphQLError -> not-found when its messages look like a missing
//     object, validation when they look like input validation, else
//     server-fault. The heuristic is deliberately simple and is documented in
//     docs/AGENTS.md; a server that adds an extensions "code" can be matched more
//     precisely later without changing the taxonomy.
//   - Anything else -> internal (exit 1), the reserved unexpected-failure code.
//
// destructive-refused, job-failed, still-running, and unconfirmed are produced
// by their own command paths (destructive gating and --wait, Task 21); they are
// not inferred from an SDK error here. Their constants are exported above so
// those paths can return them.
func classifyExit(err error) ExitCode {
	if err == nil {
		return ExitOK
	}

	// An explicitly carried code wins over any inference: destructive-refused,
	// job-failed, still-running, and unconfirmed are detected by their own
	// command paths and wrapped in an exitCodeError, never guessed from a cause.
	if ece, ok := errors.AsType[*exitCodeError](err); ok {
		return ece.code
	}

	if _, ok := errors.AsType[*usageError](err); ok {
		return ExitUsage
	}

	// A missing-URL configuration error (no STASHAPP_URL and no --url/WithURL) is
	// the caller's setup mistake, not an unexpected internal failure: map it to
	// usage (exit 2) so it does not land on the reserved internal code (exit 1).
	if errors.Is(err, stash.ErrNoURL) {
		return ExitUsage
	}

	if errors.Is(err, stash.ErrUnauthorized) {
		return ExitAuth
	}

	if _, ok := errors.AsType[*stash.TransportError](err); ok {
		return ExitTransport
	}
	// The CLI's own transport fallback type, used on paths that do not flow
	// through stash.Classify (the streaming subscription fallback in stream.go).
	// The genqlient MakeRequest path now normalises to stash.TransportError via
	// stash.Classify, but this branch keeps that fallback mapped to the same
	// transport exit code.
	if _, ok := errors.AsType[*transportError](err); ok {
		return ExitTransport
	}

	if gqlErr, ok := errors.AsType[*stash.GraphQLError](err); ok {
		return classifyGraphQLExit(gqlErr)
	}

	return ExitInternal
}

// classifyGraphQLExit splits a server-executed GraphQL error into not-found,
// validation, or server-fault by inspecting its messages.
//
// HEURISTIC, by necessity: Stash returns plain English messages without a
// machine-readable extensions "code", so there is no structured field to switch
// on. This therefore matches on English substrings of the message text — a
// deliberately last-resort fallback that runs only because the structured signal
// is absent. The buckets are conservative and any unmatched message falls
// through to server-fault. If a future server populates an extensions "code",
// switch on that first and demote this substring pass to a pure fallback,
// without changing the frozen taxonomy.
func classifyGraphQLExit(err *stash.GraphQLError) ExitCode {
	switch {
	case messagesLookNotFound(err.Messages()):
		return ExitNotFound
	case messagesLookValidation(err.Messages()):
		return ExitValidation
	default:
		return ExitServerFault
	}
}

// messagesLookNotFound reports whether any message reads like a missing object.
func messagesLookNotFound(msgs []string) bool {
	for _, m := range msgs {
		l := strings.ToLower(m)
		if strings.Contains(l, "not found") ||
			strings.Contains(l, "does not exist") ||
			strings.Contains(l, "no such") {
			return true
		}
	}
	return false
}

// messagesLookValidation reports whether any message reads like input
// validation. It stays narrow on purpose: an over-eager validation bucket would
// hide genuine server faults, so only clear input-rejection phrasings match.
func messagesLookValidation(msgs []string) bool {
	for _, m := range msgs {
		l := strings.ToLower(m)
		if strings.Contains(l, "validation") ||
			strings.Contains(l, "invalid") ||
			strings.Contains(l, "must be") ||
			strings.Contains(l, "is required") ||
			strings.Contains(l, "cannot be null") {
			return true
		}
	}
	return false
}

// writeErrorEnvelope marshals the classified error as a single-line JSON object
// to w (stderr in main). Compact single-line is deliberate: an agent reads one
// newline-terminated JSON value, no multi-line parsing. The envelope reuses
// stash.ErrorEnvelope for its fields (message, graphqlErrors, field, retryable)
// and overrides Code with the taxonomy NAME, so the "code" string and the
// process exit status name the same classification.
//
// The message and every GraphQL error message are run through [redact.Message]
// first: a non-2xx server body or a GraphQL error that echoes a pre-signed
// `?apikey=<JWT>` URL must not leak the credential to stderr, the same invariant
// the success path holds on stdout. [redact.Message] also scrubs a bare JSON
// secret field (`"apiKey"`, `"api_key"`, `"password"`, `"handyKey"`, …) that a
// server error body can carry inline as text, which the URL-only pass would miss.
func writeErrorEnvelope(w io.Writer, code ExitCode, err error) {
	env := stash.NewErrorEnvelope(err)
	env.Code = code.Name
	env.Message = redact.Message(env.Message)
	for i := range env.GraphQLErrors {
		env.GraphQLErrors[i] = redact.Message(env.GraphQLErrors[i])
	}

	b, marshalErr := json.Marshal(env)
	if marshalErr != nil {
		// A marshal failure must still produce a line an agent can read. A write
		// error to stderr has no useful recovery, so it is deliberately ignored.
		_, _ = fmt.Fprintf(w, `{"code":%q,"message":%q}`+"\n", code.Name, redact.Message(err.Error()))
		return
	}
	b = append(b, '\n')
	_, _ = w.Write(b)
}

// wrapCobraUsageErrors arranges for cobra's flag-parsing and argument errors to
// be classified as usage errors. cobra reports these through its FlagErrorFunc
// and by returning errors from argument validation; tagging them here lets
// classifyExit map them to ExitUsage without string-matching cobra's wording.
func wrapCobraUsageErrors(root *cobra.Command) {
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return newUsageError(err)
	})
}
