// Package exitcode is the single source of truth for the CLI's frozen exit-code
// taxonomy: a stable name paired with the process exit status it carries. Both
// sides of the codebase derive from it — the CLI runtime (cmd/stash, which exits
// with these integers and stamps the name into the error envelope's "code"
// field) and the catalog generator (internal/genops, which emits the names into
// schema/catalog.json for an agent to read). Sourcing both from here means the
// catalog's vocabulary and the runtime's exit codes cannot drift: a rename or
// renumber is one edit, locked by TestTaxonomyFrozen.
//
// The (name, integer) pairs are FROZEN. Never renumber an existing code; agents,
// schema/catalog.json, and docs/AGENTS.md depend on the exact values.
//
// This package deliberately imports nothing from the rest of the module, so it
// can sit underneath both consumers without an import cycle.
package exitcode

// Code is one entry of the taxonomy: a machine-readable name and the process
// exit status that pairs with it.
type Code struct {
	// Name is the stable, machine-readable classification (e.g. "auth"). It is
	// also the value of the error envelope's "code" field and the symbol the
	// catalog lists in a command's exitCodes array.
	Name string
	// Status is the process exit status for this classification.
	Status int
}

// The frozen taxonomy. The integers are a contract; the descriptions mirror
// docs/AGENTS.md.
var (
	// OK: the command succeeded. No error envelope is written.
	OK = Code{Name: "ok", Status: 0}
	// Internal: an unexpected, internal failure that fits no class below.
	// Reserved as the catch-all so a genuine taxonomy code is never confused
	// with an unexpected crash — which is why it is deliberately absent from
	// Base (it is never an advertised, expected outcome of a command).
	Internal = Code{Name: "internal", Status: 1}
	// Usage: bad invocation — an unknown flag, a malformed flag value, or the
	// wrong argument count (a cobra usage error).
	Usage = Code{Name: "usage", Status: 2}
	// Auth: authentication or authorisation failed (missing/invalid API key,
	// HTTP 401/403, or an auth-shaped GraphQL error).
	Auth = Code{Name: "auth", Status: 3}
	// Transport: the request did not get a well-formed GraphQL answer — a
	// network failure, a cancelled context, or a non-2xx HTTP status.
	Transport = Code{Name: "transport", Status: 4}
	// Validation: the server executed the request but rejected the input as
	// invalid (a GraphQL error whose message reads like input validation).
	Validation = Code{Name: "validation", Status: 5}
	// ServerFault: the server returned a GraphQL error that is not the caller's
	// fault and not one of the more specific classes.
	ServerFault = Code{Name: "server-fault", Status: 6}
	// NotFound: the requested object does not exist (a GraphQL error whose
	// message reads like a missing object).
	NotFound = Code{Name: "not-found", Status: 7}
	// DestructiveRefused: a destructive operation was invoked without the
	// required confirmation. Produced by the destructive-gating path.
	DestructiveRefused = Code{Name: "destructive-refused", Status: 8}
	// JobFailed: an async (job-returning) operation finished in a failed state.
	// Produced by the --wait path.
	JobFailed = Code{Name: "job-failed", Status: 9}
	// StillRunning: --wait timed out with the job still running. Produced by the
	// --wait path.
	StillRunning = Code{Name: "still-running", Status: 10}
	// Unconfirmed: a required confirmation prompt was declined or could not be
	// shown, or a --wait outcome could not be trustworthily settled.
	Unconfirmed = Code{Name: "unconfirmed", Status: 11}
)

// Base is the set of exit codes every command can return, in frozen order: the
// outcomes a command may produce regardless of its shape. It is the catalog's
// base exitCodes set, extended per command with not-found (a nullable
// single-entity lookup) and the destructive / job-returning codes.
//
// Internal (exit 1) is intentionally NOT in Base: it is the unexpected-failure
// catch-all, not an advertised outcome, so the catalog never lists it.
var Base = []Code{OK, Usage, Auth, Transport, Validation, ServerFault}
