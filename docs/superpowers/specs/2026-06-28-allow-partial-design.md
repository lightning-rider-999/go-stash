# `--allow-partial` — opt-in partial GraphQL results (CLI)

Status: approved design, pre-implementation
Date: 2026-06-28
Branch: `feat/allow-partial`

## Problem

Some Stash server responses violate their own schema contract: a field the SDL marks non-null resolves to null server-side, and gqlgen bubbles the null up while still returning the surrounding data. The live, reproduced case is `Query.plugins`: the SDL declares `PluginTask.plugin: Plugin!` and `PluginHook.plugin: Plugin!` (non-null), but Stash's resolvers return null for those parent back-references. Because `Plugin.tasks: [PluginTask!]` and `Plugin.hooks: [PluginHook!]` are *nullable lists*, the null bubbles only to `tasks`/`hooks` and stops there — verified against a live instance, the HTTP-200 response carries all 21 plugins with every core field intact (`id`, `name`, `description`, `version`, `settings`, `paths`), only `tasks`/`hooks` nulled, alongside a 27-entry `errors` array.

go-stash currently treats *any* GraphQL `errors` as a total failure: `runOperation` discards the decoded `data` and exits with the classified non-zero code (`server-fault`, 6). So `stash misc plugins` and `stash misc plugin-tasks` return no usable data on a real instance with plugins installed, even though the server sent usable data. This is faithful behaviour (the project deliberately fails loud rather than masking a server fault), but it leaves the user no way to retrieve the data the server actually returned.

This is fundamentally an upstream Stash bug. The correct long-term fix is upstream relaxing the field to nullable (or populating the resolver); once that lands, a re-vendor of the SDL makes the problem disappear with no go-stash change. `--allow-partial` is a client-side escape hatch for the interim — and for the general class of "HTTP 200 with data + errors" responses, not just plugins.

## Goals

- Let a user opt in to receiving the partial `data` from an HTTP-200-with-errors response, on stdout, in the normal output format.
- Never silence the error: the error envelope still goes to stderr and the exit code still reflects the fault. The flag stops the data being *discarded*; it does not make the command *succeed*.
- Be a general mechanism (any operation, any non-null-bubble or other data+errors response), not a plugin-specific patch.
- Be forward-safe: when upstream fixes the field, the flag becomes a no-op with no behaviour cliff.

## Non-goals

- No library (`stash/`) partial-results API. The SDK's typed methods structurally drop partial data (`if err != nil { return nil, classify(err) }`) and carrying it would require a new error-with-data type threaded through every method. Out of scope; a clean future extension if a library consumer ever needs it.
- No partial handling for subscriptions. Subscription events already stream to stdout as they arrive; there is no single data+errors payload to recover. The flag has no effect on the streaming path.
- No new exit code. The taxonomy (`internal/exitcode`, codes 0–11) is frozen; partial-success does not get its own code (see "Exit disposition").
- No change to default behaviour. Without the flag, every current outcome is byte-for-byte unchanged.

## Design

### Single intervention point

Generic query/mutation execution (the default, non-`--wait` path) funnels through `runOperation` in `cmd/stash/exec.go`. It decodes the response generically into a raw `json.RawMessage` (no typed struct, no reflection), which is the most favourable possible shape for partial results:

```go
func runOperation(ctx, c, spec, vars, format, out) error {
    var data json.RawMessage
    req := requestFor(spec, vars)
    resp := &graphql.Response{Data: &data}
    if err := c.GraphQL().MakeRequest(ctx, req, resp); err != nil {
        return classifyError(err)        // <- discards the already-populated `data`
    }
    return writeOutput(out, format, spec, data)
}
```

Per genqlient's driver (`graphql/client.go`), on an HTTP-200 response the full body is decoded into `resp` (populating `data`) *before* the `errors` array is checked; the returned error is the `gqlerror.List` but `data` already holds the partial payload. So at the `if err != nil` branch, `data` is populated and then thrown away. This is the primary location where the partial `data` and the GraphQL error coexist in scope.

There is one structurally identical second discard site: `runJobMutation` in `cmd/stash/wait.go` (same `var data json.RawMessage` → `MakeRequest` → discard-on-error), taken when a job-returning mutation is invoked **with `--wait`**. It is deliberately *not* an insertion point for this change — see "Edge cases", where it is scoped out with rationale. Keeping the branch in `runOperation` alone preserves a single intervention point.

### The branch

`runOperation` gains an `allowPartial bool` parameter (threaded from the resolved flag). The error branch first **classifies** the raw error: genqlient returns a bare `gqlerror.List`, which `classifyError`/`stash.Classify` wraps into a typed `*stash.GraphQLError` (HTTP-200 errors), or into `*stash.TransportError` on a non-2xx. It then branches: if `allowPartial` is set, the *classified* error is a `*stash.GraphQLError`, and `len(data) > 0`, then render the partial data to stdout via the normal `writeOutput`, and still return the classified error so the caller emits the envelope and exits non-zero. Otherwise behave exactly as today.

Classifying **before** the guard is load-bearing: the raw `MakeRequest` error is a `gqlerror.List`, whose `As`/`Unwrap` only ever expose its own `*gqlerror.Error` elements — it never unwraps to a foreign type like `*stash.GraphQLError`. Guarding the raw error would therefore be permanently false and the feature would never fire. The guard must run against the classified error.

```go
if err := c.GraphQL().MakeRequest(ctx, req, resp); err != nil {
    classified := classifyError(err)     // gqlerror.List -> *stash.GraphQLError, or *stash.TransportError on non-2xx
    var gqlErr *stash.GraphQLError
    if allowPartial && errors.As(classified, &gqlErr) && len(data) > 0 {
        if werr := writeOutput(out, format, spec, data); werr != nil {
            return werr                  // a render failure is a real failure
        }
    }
    return classified                    // envelope to stderr + classified non-zero exit, unchanged
}
```

`errors.As` requires the standard library `errors` package; `exec.go` does not currently import it, so the change adds that import.

The data-to-stdout / error-to-stderr / non-zero-exit split is achieved with no change to `main.go`'s top-level error wiring: `runOperation` writes the partial data to `out` (stdout) itself, then returns the same error it returns today, which flows up through cobra `RunE` to `run` → `classifyExit` + `writeErrorEnvelope` (stderr) → non-zero exit, untouched.

### Flag

A persistent flag `--allow-partial` (bool, default false), registered alongside the existing persistent flags (`--output`, `--url`, `--api-key`, `--input`) so it is available on every operation command. It is inert unless an HTTP-200 data+errors response with non-empty data actually occurs, so registering it broadly costs nothing.

### Data flow

1. Server returns HTTP 200, body `{ "data": {…partial…}, "errors": [27×non-null violation] }`.
2. genqlient decodes body into `data` (populated) and returns `resp.Errors` as the error.
3. `runOperation`: classifies the error to `*stash.GraphQLError`; with `allowPartial` true and `len(data) > 0` → `writeOutput(stdout, format, spec, data)` renders the partial payload (e.g. 21 plugins with `tasks`/`hooks` null).
4. `runOperation` returns `classifyError(err)` → `*stash.GraphQLError`.
5. `run` (main.go) → `classifyExit` buckets it (here: `server-fault`) → `writeErrorEnvelope` writes the redacted envelope to stderr → process exits 6.

Net: stdout = usable data, stderr = the error envelope, `$?` = 6. A consumer reads stdout for data and/or inspects stderr/`$?` for the caveat. `-o json | jq` stays clean (data only on stdout).

## Exit disposition (decision: option A)

The frozen taxonomy has no "succeeded with partial data" slot, and we will not mint one (renumbering is forbidden) nor fake `ok` (0). Under `--allow-partial`, the exit code remains the **same classified non-zero** the command returns today for that response (`server-fault` for the plugins case). The flag's sole effect is that the partial data is additionally emitted on stdout; the error reporting (stderr envelope) and exit code are unchanged. This is the loudest, most honest option and the most forward-safe: when upstream fixes the field, the response carries no `errors`, so execution takes the normal success path (`writeOutput` + exit 0) and the flag silently goes inert — no behaviour cliff, nothing to revert.

Rejected: exit 0 when data is present. Friendlier for `cmd && next`, but it reports success on a response that genuinely errored, cutting against the project's fail-loud contract.

## Edge cases and limits (all explicit, none silent)

- **Non-2xx HTTP (`*stash.TransportError` / genqlient `HTTPError`)**: genqlient never decodes into the caller's `data` on a non-200, so there is no partial data to recover. `--allow-partial` is inherently a no-op here; behaviour is unchanged. Documented as a known limit.
- **HTTP 200 errors but empty/null data** (whole selection bubbled to `data: null`): `len(data) == 0` (or `data` is JSON `null`) → guard fails → unchanged failure. Nothing spurious is printed.
- **Subscriptions** (`stream.go`): separate typed-event path, flag has no effect. Documented.
- **Job-returning mutations with `--wait`**: these route through `runJobMutation` (`cmd/stash/wait.go`), not `runOperation`, so `--allow-partial` does not apply to the initial mutation response on the `--wait` path. This is deliberate: that initial response is a single non-null job-id scalar, which cannot carry a partial non-null bubble (an error there nulls the whole scalar → `data` empty → the `len(data) > 0` guard fails regardless). The same mutation *without* `--wait` goes through `runOperation` and honours the flag, but since the payload is a scalar id the difference is theoretical. Scoped out rather than duplicating the branch, to keep one intervention point. Acknowledged as a known `--wait`-dependent inconsistency.
- **Auth-shaped HTTP-200 errors**: `classify` routes an auth-shaped `errors` response through `joinUnauthorized`, whose chain still contains `*stash.GraphQLError`, so the guard fires and partial data (if any) is emitted with exit `auth` (3), not `server-fault` (6). This is intended — `--allow-partial` is a general mechanism keyed on "HTTP-200 data + GraphQL errors", not on a specific exit class.
- **`writeOutput` failure** while rendering partial data: returned as a real error (render failure is not partial success).
- **Redaction**: `writeOutput` already runs API-key redaction on rendered output, so partial data is redacted on the same path as normal output — no new redaction surface.

## Testing

All via the existing hermetic `internal/mockgql` server. A canned HTTP-200 body carrying both `data` and `errors` is served with **`WithRawResponse`** (the full-envelope option whose doc states exactly this; `WithResponse` wraps a *bare* data object and cannot add an `errors` array). No live server required.

1. **Partial recovered, loud**: 200 body with `data` + `errors`, `--allow-partial` set → assert the partial data is written to the stdout writer in the requested format AND the command returns a `*stash.GraphQLError` classified to the expected non-zero exit (envelope to stderr). The core regression test.
2. **Default unchanged**: same response, flag absent → assert no data on stdout, error envelope on stderr, same non-zero exit as today.
3. **Empty data guard**: 200 with `errors` and `data: null` (or absent), flag set → assert unchanged failure, nothing on stdout.
4. **Success unaffected**: 200 with `data`, no `errors`, flag set → assert normal output + exit 0 (flag inert on the happy path).
5. **Non-200 no-op**: a non-2xx response, flag set → assert unchanged transport failure, nothing on stdout.
6. **Format fidelity**: partial data renders through each `-o` format (json/ndjson/table/yaml) the same as full data.

Existing tests that assert "errors ⇒ no output" must be checked to confirm they run without the flag (default path) and remain green.

## Docs and generated artifacts

- The flag is a persistent flag, so `--allow-partial` appears in `--help` for every command. The generated CLI reference under `docs/cli/` will change accordingly — regenerate (`task gen-docs`) so the docs-freshness gate stays green.
- `docs/AGENTS.md`: add a short note on `--allow-partial` semantics (stdout data + stderr envelope + non-zero exit on a partial response) and its limits (HTTP-200 only, not subscriptions), so an agent reader understands a non-zero exit can still accompany usable stdout when the flag is set.
- No change to the catalog, exit-code taxonomy, or codegen inputs — this is a runtime-only flag, so `schema/catalog.json` and `internal/gen` are untouched.

## Out-of-scope follow-ups (noted, not in this change)

- Library-level partial results in `stash/` (would need an error type that carries data).
- Pruning the redundant `PluginTask.plugin` / `PluginHook.plugin` back-edge from the generated selection set (would need an edge-exclude capability in `github.com/trackness/graphql-opgen`; would make `plugins` succeed cleanly without the flag, but is a generator feature, not a go-stash change).
- Filing the upstream Stash issue (the true root fix): `PluginTask.plugin` / `PluginHook.plugin` are `Plugin!` in the SDL but resolve null.
