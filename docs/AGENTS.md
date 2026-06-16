# AGENTS.md — the machine-facing contract for the `stash` CLI

This file is the stable contract an agent can rely on when driving the `stash`
binary. It covers the **exit-code taxonomy**, the **error-envelope shape**, the
**input model** (raw-JSON variables and convenience flags), **enum symbols**,
**multi-criterion filters**, the **`--wait` job contract** including the
re-attach flow, the **partial-update three-state contract**, and the
**`per_page` footgun**.

The CLI is agent-first: stdout carries the operation's JSON result; stderr
carries a single-line JSON error envelope on failure; the process exit status is
the integer paired with the failure's code name. The code name in the envelope
and the exit integer always agree.

## Exit-code taxonomy

The (name, integer) pairs are **frozen**. The name is the envelope's `code`
field; the integer is the process exit status. `schema/catalog.json` lists, per
command, the subset of these names it can produce in its `exitCodes` array, so
the catalog and the runtime use the same vocabulary (the running CLI serves a
byte-identical `cmd/stash/catalog.json` it `//go:embed`s; `schema/catalog.json`
is the canonical source that embedded copy is generated from).

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
| `destructive-refused` |    8 | A destructive operation was invoked without the required confirmation. *(Produced by the destructive-gating path, `gate.go`.)* |
| `job-failed`          |    9 | An async (job-returning) operation finished in a failed state. *(Produced by the `--wait` path, `wait.go`.)* |
| `still-running`       |   10 | `--wait` timed out with the job still running. *(Produced by the `--wait` path, `wait.go`.)* |
| `unconfirmed`         |   11 | A required confirmation prompt was declined or could not be shown. *(Produced by the `--wait` path, `wait.go`.)* |

Notes:

- **`1` is reserved** for an internal/unexpected failure so a genuine taxonomy
  code is never confused with a crash. Map a generic internal error to `internal`
  (exit 1), not to any class above.
- The `destructive-refused`, `job-failed`, `still-running`, and `unconfirmed`
  codes are **returned by their own command paths**, not inferred from a server
  error: `destructive-refused` by the destructive-gating path (`gate.go`), and
  `job-failed`, `still-running`, and `unconfirmed` by the `--wait` path
  (`wait.go`).

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

### `per_page` and result-set size

`per_page` lives on the `FindFilterType` (`filter`) object, settable through
`--per-page` or directly in `--input`. Two values to know:

- **Default `per_page` is `25`.** When `--input` omits `per_page` and no
  `--per-page` flag is given, the CLI sends no `per_page`, and the server applies
  its default of 25 rows. A list query is paginated unless you say otherwise.
- **`per_page: -1` returns _every_ row.** This is the server's "all results"
  sentinel, not a bug. On a large library it produces a very large response in a
  single payload, with no streaming and no back-pressure. Reach for it only when
  you genuinely need the whole set; otherwise page with `page`/`per_page`. The
  CLI does not cap or warn, so the size is yours to manage.

The result-type envelope a list query returns carries a `count` field (the total
matching rows, independent of the page size), so an agent can size its paging
from the first page rather than pulling everything to learn the total.

## Enum values are SDL symbols

Every GraphQL enum is sent and received as its **SDL symbol**, in upper
snake-case, never a display label or an integer. `GenderEnum`, for instance, is
one of `MALE`, `FEMALE`, `TRANSGENDER_MALE`, `TRANSGENDER_FEMALE`, `INTERSEX`,
`NON_BINARY`. A filter modifier (`CriterionModifier`) is one of `EQUALS`,
`NOT_EQUALS`, `GREATER_THAN`, `LESS_THAN`, `IS_NULL`, `NOT_NULL`,
`INCLUDES_ALL`, `INCLUDES`, `EXCLUDES`, `MATCHES_REGEX`, `NOT_MATCHES_REGEX`,
`BETWEEN`, `NOT_BETWEEN`.

The authoritative list for any enum is the embedded catalog. Run
`stash catalog` for the whole document, or `stash catalog <OperationName>` for
one operation; the `$defs` section lists every enum as

```json
"GenderEnum": {"kind": "enum", "values": [{"value": "MALE"}, {"value": "FEMALE"}, ...]}
```

and every input type's fields with their GraphQL types, so an agent can build a
valid `--input` from the catalog alone without a live server.

## Multi-criterion filters

A list query's `filter` argument is the page/sort envelope (`FindFilterType`);
the *content* of the search is a second argument, the resource's own
`<Resource>FilterType` (for scenes, `SceneFilterType`). Pass it through `--input`
under the operation's scene-filter variable name. These filter types compose:

- They carry boolean **`AND`**, **`OR`**, and **`NOT`** fields, each itself a
  `SceneFilterType`, so criteria nest arbitrarily.
- A scalar criterion is a typed criterion input: `title` is a
  `StringCriterionInput`, `rating100` an `IntCriterionInput`, each with a
  `value` and a `modifier` (a `CriterionModifier` symbol).
- A relationship criterion that spans a hierarchy (tags, studios) is a
  `HierarchicalMultiCriterionInput`: `value` (a list of IDs), a `modifier`, an
  optional **`depth`** (how many levels of descendants to include; `-1` for all
  descendants, `0` for the named items only), and an optional `excludes`.

Example: scenes that are organised, rated at least 80, tagged with tag `5` or any
of its descendants, and **not** by performer count below two. Save as
`scene-filter.json`:

```json
{
  "scene_filter": {
    "AND": {
      "organized": true,
      "rating100": { "value": 80, "modifier": "GREATER_THAN" },
      "tags": { "value": ["5"], "modifier": "INCLUDES_ALL", "depth": -1 }
    },
    "NOT": {
      "performer_count": { "value": 2, "modifier": "LESS_THAN" }
    }
  },
  "filter": { "per_page": 50, "sort": "rating100", "direction": "DESC" }
}
```

```sh
stash scene list --input scene-filter.json -o ndjson
```

The variable names (`scene_filter`, `filter`) are the operation's own GraphQL
argument names; `stash catalog FindScenes` lists them. Because `--input` is
forwarded as raw JSON, the nested shape reaches the server verbatim.

## The `--wait` job contract

Job-returning mutations (the ones whose catalog entry carries
`"jobReturning": true`, such as `stash metadata scan`, `stash metadata generate`,
`stash metadata import`) return a **job ID** immediately and run asynchronously
on the server. Two flags let a command block on the outcome:

- `--wait` — block until the enqueued job reaches a terminal state.
- `--wait-timeout <duration>` — with `--wait`, give up after this long. The
  default (`0`) waits indefinitely.

Without `--wait`, the command prints the job ID and exits `0` as soon as the job
is enqueued; tracking it is then up to the caller (see re-attach, below).

With `--wait`, the exit code reports the **job's** outcome, not merely the
enqueue:

| Outcome                                   | Code            | Exit |
|-------------------------------------------|-----------------|-----:|
| Job reached `FINISHED`                     | `ok`            |    0 |
| Job reached `FAILED` or `CANCELLED`        | `job-failed`    |    9 |
| `--wait-timeout` elapsed, job still running| `still-running` |   10 |
| Outcome could not be confirmed (see below) | `unconfirmed`   |   11 |

How `--wait` tracks the job: it seeds with a `findJob` query (if the job is
already terminal, it finishes at once), then follows the `jobsSubscribe`
subscription for status updates until a terminal state, reconciling with a fresh
`findJob` query if the stream drops or reports the job removed.

### The `unconfirmed` (exit 11) re-attach flow

If the subscription drops and the reconciling `findJob` cannot settle the
outcome — the query errors, or returns a null job (the job may have finished and
been evicted from the queue, or may never have existed) — the result is
**indeterminate**, not a decided failure. The command exits `11` (`unconfirmed`)
and the error envelope's `message` carries the **job ID**, so an agent can
re-attach rather than guess:

- `stash job watch` streams the live job feed (`jobsSubscribe`); watch for the
  ID's terminal update.
- Or re-query the job directly (`findJob` for that ID): a terminal status
  confirms the outcome; a persistent null after a completed run means the job
  finished and was evicted.

`unconfirmed` is deliberately distinct from `job-failed`: exit 9 means the server
told us the job failed; exit 11 means we could not get a trustworthy answer. Do
not treat 11 as success or as failure without re-attaching.

## Partial updates: the present / absent / null three-state

Update mutations take a single `input` object, and the field you **omit** is
left unchanged while the field you set to **`null`** is cleared. The CLI
preserves this distinction end to end because `--input` is forwarded as raw JSON
and never decoded through a typed Go struct (a struct would erase the difference
between an absent field and a zero value). The three states:

- **present with a value** — set the field to that value.
- **explicit `null`** — clear the field (set it to no value).
- **absent** — leave the field exactly as it is on the server.

Example: rename scene `42` and clear its director, while leaving everything else
untouched. Note `director` is present-and-`null` (clear it) and, say, `rating100`
is simply absent (unchanged). Save as `scene-update.json`:

```json
{
  "input": {
    "id": "42",
    "title": "New Title",
    "director": null
  }
}
```

```sh
stash scene update --input scene-update.json
```

`title` is set, `director` is cleared, and every field not named (rating,
organised flag, tags, performers) keeps its current value. Sending
`"director": ""` would instead set the director to an empty string, which is a
different operation from clearing it.

## Idempotency

- Read queries (`stash scene list`, `stash scene get`, `stash catalog`) have no
  side effects and are safe to repeat.
- Setter mutations are idempotent on their inputs: `stash scene update` with the
  same `input` converges to the same state; re-running it is safe.
- Enqueue mutations are **not** idempotent. Re-running `stash metadata scan`
  starts another job. After an `unconfirmed` (exit 11), re-attach to the existing
  job ID rather than re-enqueueing, or you may start duplicate work.
- Destructive mutations are gated behind `--yes-i-understand` and exit `8`
  (`destructive-refused`) without it. The gate is checked before any server
  request, so a refused destructive command has no effect at all.
