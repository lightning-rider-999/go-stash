# AGENTS.md — the machine-facing contract for the `stash` CLI

This file is the stable contract an agent can rely on when driving the `stash`
binary. It grows as the CLI does; today it covers the **exit-code taxonomy** and
the **error-envelope shape**. (The enum-value rules, `--wait` job semantics, and
partial-update input contract are added by a later task.)

The CLI is agent-first: stdout carries the operation's JSON result; stderr
carries a single-line JSON error envelope on failure; the process exit status is
the integer paired with the failure's code name. The code name in the envelope
and the exit integer always agree.

## Exit-code taxonomy

The (name, integer) pairs are **frozen**. The name is the envelope's `code`
field; the integer is the process exit status. `schema/catalog.json` lists, per
command, the subset of these names it can produce in its `exitCodes` array, so
the catalog and the runtime use the same vocabulary.

| Code name             | Exit | When it occurs                                                                                  |
|-----------------------|-----:|-------------------------------------------------------------------------------------------------|
| `ok`                  |    0 | The command succeeded. No envelope is written.                                                  |
| `internal`            |    1 | An unexpected, internal failure that does not fit any class below. Reserved; treat as a bug.    |
| `usage`               |    2 | Bad invocation: an unknown flag, a malformed flag value, the wrong argument count.              |
| `auth`                |    3 | Authentication or authorisation failed (missing/invalid API key; HTTP 401/403; an auth-shaped GraphQL error). |
| `transport`           |    4 | The request did not get a well-formed GraphQL answer: a network failure, a cancelled context, or a non-2xx HTTP status. |
| `validation`          |    5 | The server executed the request but rejected the input as invalid (a GraphQL error whose message reads like input validation). |
| `server-fault`        |    6 | The server returned a GraphQL error that is not the caller's fault and not one of the more specific classes. |
| `not-found`           |    7 | The requested object does not exist (a GraphQL error whose message reads like a missing object).|
| `destructive-refused` |    8 | A destructive operation was invoked without the required confirmation. *(Produced by the destructive-gating path; reserved here.)* |
| `job-failed`          |    9 | An async (job-returning) operation finished in a failed state. *(Produced by the `--wait` path; reserved here.)* |
| `still-running`       |   10 | `--wait` timed out with the job still running. *(Produced by the `--wait` path; reserved here.)* |
| `unconfirmed`         |   11 | A required confirmation prompt was declined or could not be shown. *(Reserved here.)*           |

Notes:

- **`1` is reserved** for an internal/unexpected failure so a genuine taxonomy
  code is never confused with a crash. Map a generic internal error to `internal`
  (exit 1), not to any class above.
- The `destructive-refused`, `job-failed`, `still-running`, and `unconfirmed`
  codes are **returned by their own command paths** (destructive gating and
  `--wait`). They are not inferred from a server error. Their constants exist now
  so those paths can produce them; some are not yet emitted.

### How a server error is classified

Stash returns GraphQL errors as plain messages without a machine code, so the
CLI buckets them by message text. The split, in priority order:

1. `auth` — the error (or the HTTP status) signals an authentication/authorisation
   failure. Wins over any GraphQL-message bucket.
2. `not-found` — a message containing "not found", "does not exist", or "no such".
3. `validation` — a message containing "validation", "invalid", "must be",
   "is required", or "cannot be null". Kept narrow so a real server fault is not
   mislabelled.
4. `server-fault` — any other server-executed GraphQL error.

A non-2xx HTTP status or a network/decode failure is `transport`, regardless of
body. This is a heuristic; if a future Stash version adds an `extensions.code`,
the classifier can match it exactly without changing the taxonomy.

## Error-envelope shape

On any failure the CLI writes **one compact, single-line, newline-terminated
JSON object to stderr** and exits with the code's integer. Single-line is
deliberate: read one line, parse one JSON value.

```json
{"code":"not-found","message":"stash: graphql error: scene not found","graphqlErrors":["scene not found"],"retryable":false}
```

Fields:

| Field           | Type       | Always present | Meaning                                                                                 |
|-----------------|------------|----------------|-----------------------------------------------------------------------------------------|
| `code`          | string     | yes            | The taxonomy code **name** (the table above). Equals the name of the exit integer.      |
| `message`       | string     | yes            | Human-readable summary of the failure.                                                  |
| `graphqlErrors` | string[]   | when applicable| The individual server GraphQL error messages, present only when the cause was a GraphQL error. |
| `field`         | string     | when applicable| The offending input field, when one can be identified.                                  |
| `retryable`     | bool       | yes            | Whether retrying could plausibly succeed: `true` for a transient transport failure with no HTTP status, `false` for client, auth, and definite-server-answer errors. |

`graphqlErrors` and `field` are omitted when empty. `code`, `message`, and
`retryable` are always present.

## Input: variables and convenience flags

Operation variables come from `--input` (a JSON file path, or `-` for stdin):
a JSON object whose keys are the operation's own GraphQL variable names — for a
mutation, typically `{"input": { ... }}`; for a list query, keys like `ids` and
`filter`. The CLI forwards these values as **raw JSON, never decoded through a
typed Go struct**, so a partial-update mutation preserves the three states a
field can be in: present with a value, explicitly `null`, or absent. (`null`
means "clear this field"; absent means "leave it unchanged".)

A small set of **read/list-only convenience flags** is offered on query commands
whose schema actually declares the matching argument, and never on mutations, so
they can never inject a mutation input key:

- `--id <id>` — selects one object: sets `ids: ["<id>"]` for an op declaring an
  `ids` argument, or `id: "<id>"` for one declaring a scalar `id`.
- `--query`, `--page`, `--per-page`, `--sort`, `--direction` — merge into the
  `filter` (`FindFilterType`) object, but only for ops declaring a `filter`
  argument. `--direction` accepts `ASC` or `DESC`.

Convenience flags are additive over `--input`: any key or filter field that
`--input` already supplies wins.
