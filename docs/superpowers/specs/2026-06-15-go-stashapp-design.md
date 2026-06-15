# go-stashapp — Design Specification

**Status:** Approved (brainstorm complete) — pre-planning · **Revision: v4** (three adversarial review rounds; v4 hardens the catalog/`--wait`/partial-update mechanisms)
**Date:** 2026-06-15
**Module path:** `github.com/lightning-rider-999/go-stashapp`
**Target API:** Stash GraphQL. Schema is pinned to the release the **live instance reports** via the `version{}` handshake; researched against **v0.31.1** (latest at 2026-06-15). The pin is *verified, never assumed*.

> **Revision history.** v1 → review (38 confirmed). v2 folded them in. Verify-round confirmed 36/38 + surfaced rewrite seams → v3 reconciled them (canonical types ↔ depth variants; manifest vs SDL-AST; `--wait`; unified `destructive`). A third round found that v3's terse new clauses papered over mechanism questions → **v4** pins them: catalog is a **build-time embedded artifact**; the `--wait` drop contract is a **three-state machine**; partial-update binds **raw JSON** (not typed structs); WSConn **serializes writes**; the curated `destructive` overlay gets a **drift gate**; every subscription gets a command; ApiKey-only auth recorded.
> **Goal framing (your words):** "enable all functionality." See §2/§4.

---

## 1. Purpose & goals

Two deliverables, both held to "extremely well crafted":

- **SDK** — a *schema-complete*, faithfully-typed Go client for Stash's GraphQL API; importable by other repos with **idiomatic, stable** types.
- **CLI** — exposes the SDK *completely*, and is **agent-first**: the primary operator is an LLM agent. A partner skill will be written later (out of scope here), so command structure, output, the `catalog` contract, docs, and the built artifact are designed so an agent drives it cleanly. Humans are the secondary path.

## 2. Definition of "schema-complete" (locked)

Complete — i.e. **all of Stash's functionality enabled** (your phrase) — means three things, each with a stated enforcement:

1. **Every root operation is callable** — every Query, Mutation, and Subscription field gets a generated operation. *Enforced* by the conformance test diffing the SDL root-field set against the shipped operation set (§8).
2. **Every input object, enum, and scalar is bound** — *enforced* by an SDL-enumeration conformance check plus the custom-scalar round-trip tests (§8).
3. **Every return type is representable** — bounded by policy. The schema is cyclic, so **selection depth is a policy, not "fetch the whole graph"** (§4). Representability is guaranteed at the *type* level and spot-checked for the tricky cases (`BaseFile`/`VisualFile`).

The surface, **computed from the vendored SDL at build time**, is currently **74 queries + 134 mutations + 3 subscriptions = 211 operations** — a figure that **includes deprecated aliases** (the `movie` family etc.); "all functionality" is the *non-deprecated* subset (§3), and the build computes the exact number so the prose can't drift.

## 3. Scope

- **In:** the whole schema — read, write, operational/admin, and subscriptions.
- **Deprecated/superseded operations** (e.g. the `movie` family — `findMovie(s)`, `allMovies`, `movieCreate/Update/Destroy`, `scrapeSingleMovie`, etc. — replaced by `group`) are still generated for literal completeness and drift-safety, but flagged deprecated in the catalog (§6) so an agent uses the live equivalent. They ride along for free and are counted in the 211.
- **Auth:** **ApiKey header only.** Stash also supports username/password *session* auth; that is **deliberately out of scope** (this is the user's own instance with an issued key). Recorded decision, not a silent omission.
- **Out:** writing the partner agent skill (a separate future effort; this repo produces the artifact and docs it will build on).

## 4. Core architecture decision — codegen strategy

**Goal: enable all functionality.** Every Stash capability must be reachable through the SDK and CLI (§2) — that is the end. **Generation is the *means*, not the end**: the preferred default because it reaches the whole surface cheaply and turns schema drift into a red build, but subordinate to the functional goal. A generator (`genops`) emits operations for the entire root surface under a defined selection policy ("generate everything"); we **curate by evidence** where the default is wrong; and where generation proves impractical (the §12 spike), **falling back to hand-authored operations is on-intent** — what matters is that all functionality is enabled, not that every operation was machine-produced.

**`internal/genops` is the project's true core.** genqlient is strictly operation-driven (no whole-API mode), so `genops` is an SDL-AST→operation *compiler*. Its obligations (§5).

### Default selection policy (the linchpin — validated by spike §12)
Candidate policy, confirmed by the validation spike before locking at scale:
- **Scalars/enums:** select all on the target type.
- **Named related objects:** emit as a **ref** — `{id, name/title}` — via the entity's ref fragment.
- **Cyclic/self-referential edges** (Tag, Studio, Group via `GroupDescription`, Folder, Scene↔studio/performer/tag/group): **ref-only by default**, with explicit **depth-parameterized variant operations** for deeper reach.
- **Cycle termination:** **path-based** — on revisiting a type already on the current selection path, emit only its ref set. Breaks *mutual* cycles (Scene→Studio→Scene), not just self-edges. A global max-depth backstops it.
- **Interfaces/unions** (`BaseFile`/`VisualFile`): expand all implementations' scalars via inline fragments + `__typename`.
- **`Map`/`custom_fields`:** raw JSON.

> **Two recursion axes (don't conflate).** The policy above governs *output* selection depth. *Input* recursion (a `*FilterType`'s `AND`/`OR`/`NOT` self-refs, `HierarchicalMultiCriterionInput.depth`) is **variable-side data** carried verbatim by genqlient's recursive input structs through `encoding/json`; no depth policy applies (round-trip tested, §8).

### Canonical types (public-API decision — locked)
Object types bind to **one canonical, shared Go struct per entity** via **mandated named fragments** (`fragment SceneFields on Scene`, `fragment SceneRef on Scene`) that `genops` emits and every operation spreads; `@genqlient(typename:)` gives clean names (`stash.Scene`, `stash.SceneRef`).
- Every operation returning a Scene *at a given depth tier* hands back the **same** `stash.Scene`.
- **Public type names are NOT a function of selection depth** — names come from the enumerated fragment set, not genqlient's path concatenation; tuning the policy never renames a public type.
- **Fragment-only discipline:** at every entity occurrence `genops` spreads **exactly one named fragment** — never an ad-hoc inline subset. Path-based termination is "which fragment to spread" (`SceneFields` first-seen, `SceneRef` on revisit/backstop). **Depth variants are themselves named, fragment-backed types** (`stash.SceneDeep`); each tier is its own stable type. The only Go types an entity yields are its enumerated fragments; no occurrence mints a path-named anonymous struct (asserted §8).
- **The enumerated depth-tier set is part of the public surface** (§9 SemVer): adding a tier is *minor*; removing/renaming one is *major*. §8 asserts the declared tier set is present.

### Other locked elements
- **One forced island:** `importObjects` (multipart `Upload`) is hand-wired; conformance asserts the SDL has **exactly one** `Upload` field.
- **Safety drift is a red build:** the curated `destructive` overlay (§5) is backstopped by a mutation-set drift gate (§8) — a new Stash mutation can't ship ungated.
- **Completeness is enforced, not asserted** — §2 / §8.

## 5. SDK architecture & wiring

### Layout (module `github.com/lightning-rider-999/go-stashapp`)
- **`stash/`** (root) — public surface: `Client`, options, typed errors, subscription helpers, the `importObjects` island, the generated operations + **canonical fragment-backed types**. The hand-written `Client`/options/errors/subscription layer is the **stable** contract; generated operations track upstream.
- **`schema/`** — vendored SDL + version stamp + generated `version` constant + the generated, **`go:embed`-ed `catalog.json`** (the build-time machine contract, §6).
- **`internal/genops/`** — the generator (`tool` directive; never a `tools.go`).
- **`operations/generated/`** — generated `.graphql`; **`operations/overrides/`** — hand-authored overrides; **`operations/overlay.yaml`** — the committed, reviewable curated overlay keyed by operation → `{destructive, job-returning}` (read-only to genops, stable across regeneration — the one curated input, so it's auditable in VCS).
- **`cmd/stash/`** — the CLI binary. **`internal/conformance/`** — completeness + drift tests.

### `internal/genops` obligations
One `gqlparser/v2` SDL-AST pass (genqlient and `gqlparser` are **build-only**; nothing re-parses SDL at runtime), emitting **deterministically** (sorted iteration; no timestamps/host info):
1. A complete, named, genqlient-valid operation per root field: collision-free name; a **variable declaration per argument** typed verbatim from the SDL (preserving `!`/nested inputs); variables forwarded; the selection policy via **fragment-only spreads** (§4); inline fragments + `__typename` for interfaces/unions; required `@genqlient` directives.
2. A **manifest** (`operations/manifest.json`) — the thin per-operation **index**: root-field name, operation name, kind (query/mutation/**subscription**), input-type *name*, plus the `destructive`/`job-returning` flags read from `operations/overlay.yaml`. It indexes **every shipped operation including overrides and hand-islands**. AND the **`schema/catalog.json`** artifact (§6) — the *resolved* model: transitively-resolved `$defs` input dictionary, enum value sets, deprecation reasons, per-command derived exit-code sets. Both are products of the *same* SDL pass (so "one source of truth" = one pass, not one file), serialized at build time; `stash catalog` later just prints the embedded artifact (no runtime parsing).
3. **Override resolution:** `genops` **parses** each file in `operations/overrides/` (same gqlparser AST) to extract its root field, validating **exactly one root field per override** (a malformed or multi-/zero-root override is a build error); it then emits a generated operation only for fields no override covers (genqlient never sees a duplicate name), and still emits manifest/catalog entries for overrides and islands. A field covered by both a generated op and an override is a build error caught by conformance.

### Wiring
- **Transport:** `net/http`, configurable `*http.Client`; `ApiKey` via a `RoundTripper`. genqlient consumes the `Do(*http.Request)` shape (purely outbound; no server framework). Two genqlient clients: HTTP for query/mutation, websocket for the 3 subscriptions (the WS client rejects query/mutation by design).
- **URL normalization:** base UI URL → append `/graphql`; derive `ws(s)://…/graphql`.
- **Timeouts:** default bounded `http.Client.Timeout` (documented) on the GraphQL path only — **not** the websocket or `--wait` paths (ctx-cancel only).
- **Options/config:** functional options + env fallback (`STASHAPP_URL`, `STASHAPP_API_KEY`). Nothing hardcoded (§11).
- **Errors:** typed — transport vs GraphQL-level (`*GraphQLError`) vs auth; `%w` chains; `errors.As`-friendly.
- **Concurrency:** bounded fan-out via `errgroup.WithContext` (first error cancels siblings, per §11) + `SetLimit` for the configurable bound; **no hidden retries**.
- **Subscriptions:** genqlient's `NewClientUsingWebSocket` owns the protocol + `Start`/`Subscribe`/`Unsubscribe`/`Close` lifecycle. The hand-written piece is a thin `graphql.Dialer`/`WSConn` adapter over `gorilla/websocket` that dials `ws(s)://…/graphql`, injects `ApiKey`, and **owns client-side keepalive** (Stash sends none on `graphql-transport-ws`, §12) and a **bounded reconnect-with-resubscribe**. **Concurrency discipline:** the adapter **serializes all writes** to the underlying conn (a single-writer goroutine / write mutex) — `gorilla/websocket` forbids concurrent writers, and the keepalive ping coexists with protocol frames; this makes the §8 `-race` gate meaningful for the WS path.
- **Diagnostics:** `log/slog`, optional injected logger; the `ApiKey` is never logged (§11).
- **Version handshake:** `Client.Version(ctx)` via `query { version { version hash build_time } }`; on mismatch the SDK warns and the CLI surfaces a distinct exit code / error-envelope field.

## 6. CLI — agent-first

Generated from the `manifest.json` (one source of truth → SDK ops *and* CLI commands); hand-islands get hand-written commands (still indexed). Framework: **`spf13/cobra`**.

- **Command grammar:** `stash <resource> <verb> [flags]`; operational groups `stash job …`, `stash config …`, `stash scan/generate …`.
- **Subscriptions → commands:** *every* subscription root field surfaces as a streaming command (manifest carries the `subscription` kind; conformance covers all three): `stash job watch` (`jobsSubscribe`), `stash log tail` (`loggingSubscribe`), `stash scan watch` (`scanCompleteSubscribe`).
- **Output:** **JSON by default, always** (no TTY detection). Lists/streams → NDJSON. Humans use `-o table|yaml`. No built-in field filtering (pipe to `jq`).
- **Input:** **JSON-first** via stdin or `--input @file.json`; convenience flags (`--q`, `--page`, `--per-page`, `--sort`, `--direction`, `--id`) are **read/list ergonomics only** — they do **not** inject keys into mutation payloads. For partial-update mutations the **input JSON body is the authoritative key set**.
- **Partial-update contract & mechanism:** the CLI sends exactly the keys present in the input JSON, omits absent ones (no zero-value injection), and passes explicit `null` through as "clear" — three distinct wire states. **Mechanism:** because stdlib `encoding/json` cannot distinguish "omit" from "explicit null" on genqlient's pointer-typed input structs, partial-update mutation inputs are **not** round-tripped through the typed structs — the operation's input variable is bound as the **raw JSON** the agent supplied (`json.RawMessage`/`map[string]any`), preserving present/absent/null verbatim. A §8 golden test proves all three states survive marshalling.
- **`per_page: -1` / `depth: -1`:** passed through verbatim; footgun documented in `AGENTS.md`; optional stderr warning is a §12 decision.

### `stash catalog` — the machine-facing contract (build-time artifact)
A **static `schema/catalog.json`** emitted by the `genops` SDL pass at build time and `go:embed`-ed; `stash catalog` prints it **verbatim** (no runtime SDL parsing). Conformance (§8) diffs it against the SDL. Contents:
- **Top-level — three version axes, stated explicitly:** `catalog_format_version`; the **vendored/targeted Stash SDL version** (what the binary was built against); (the **live-instance** version is *not* in the catalog — it comes from the §5 handshake and is the agent's session-start drift check). Catalog drift detection is catalog-vs-binary only.
- **Per command:** resource, verb, summary; `destructive` flag; `job-returning` flag; and the **set of exit codes it can return**, *derived* from the manifest flags — a base set (`ok`, `usage`, `auth`, `transport`, `validation`, `server-fault`) + `not-found` for lookups + `destructive-refused` iff `destructive` + `job-failed`/`still-running`/`unconfirmed` iff `job-returning`. Exit codes are emitted as the kebab **name** (matching the envelope `code`).
- **Inputs:** the **fully, transitively resolved** input type via a `$defs` dictionary (nested inputs, `*FilterType` criterion shapes with `{value, modifier, depth, excludes}`, `AND`/`OR`/`NOT` self-refs). Per field: name, type, required/optional, list-ness, default, **deprecation flag + verbatim `@deprecated` reason**, SDL description.
- **Enums:** every reachable enum with its complete value set as **API symbols** (e.g. `INCLUDES_ALL`), the operator-string description in a **separate** field.

### Errors & exit codes
- **Error envelope** (on stderr): `{ "error": { "code", "message", "graphql_errors":[{message,path,extensions}], "field", "retryable" } }`. `retryable` is advisory only (never triggers client retries, §11). `field` is best-effort (verify Stash exposes it — §12).
- **Taxonomy as a single frozen table of `(name, integer)` pairs** in `AGENTS.md` (never renumbered): the envelope `code` is exactly the **name** half; the process exit status is exactly the **integer** half of the same row; the catalog's per-command set uses the name. Names: `ok`, `usage`, `auth`, `transport`, **`validation`** (input rejected — fix-the-input) vs **`server-fault`** (server execution — retry/give-up), `not-found`, **`destructive-refused`**, **`job-failed`** (job ran and failed), **`still-running`** (`--wait-timeout` fired), **`unconfirmed`** (a `--wait` drop where terminal status couldn't be confirmed — re-attach via the printed job ID).
- **NDJSON completeness:** errors go to stderr only (never interleaved). Stream completeness is **stdout EOF + a zero exit** — stdout alone is not a completeness signal (documented in `AGENTS.md`); `stash job watch` additionally emits a terminal marker.

### Async / jobs (`--wait` contract)
Job-launchers return the job ID immediately and accept **`--wait`**, which seeds state with a `findJob` poll on attach (closing the launch→subscribe race), then tracks via `jobsSubscribe`:
- **Terminal success →** exit `ok`. **Terminal failure →** `job-failed` + the job's error in the envelope.
- **`--wait-timeout`:** defaults **unset = no client-side bound** (a bare `--wait` blocks until terminal or ctx cancel — long scans run for hours). When set, on timeout the job continues server-side and the CLI exits `still-running` with the job ID.
- **Involuntary socket drop — three-state reconcile** (not "any non-terminal = failure"):
  1. bounded reconnect-with-resubscribe (§5) first;
  2. if exhausted, poll `findJob`: **terminal** → exit accordingly; **confirmed still-running (non-terminal)** → **resume waiting** (re-subscribe / bounded poll-with-backoff) — do **not** exit (this is the healthy long-job case);
  3. **indeterminate** — server unreachable, or job *absent* from `findJob`/`jobQueue` (terminal jobs age out — §12 verifies the retention window) — exit `unconfirmed`, print the job ID, do **not** report `job-failed` (avoids telling an agent a succeeded job failed, which could trigger a duplicating re-run).
- **Destructive-op gating:** `querySQL`, `execSQL`, `metadataImport`, `anonymiseDatabase`, `migrate` (the exact root-field names — same verbatim list in §10) behind a `--yes-i-understand`-style flag; flagged `destructive` in the catalog; refusal → `destructive-refused`.
- **Config:** env + flags (`--url`, `--api-key`); config file is YAGNI. **Artifact:** one static binary, `cmd/stash` (name TBC).

## 7. Documentation & DX

- godoc on every package + exported symbol, with runnable `Example` functions.
- A **generated command reference** (`docs/cli/`) from cobra.
- **`docs/AGENTS.md`**: the `(name, integer)` exit-code table (stable contract); the error-envelope shape; the **three version axes** and that live-instance drift is checked once at session start via the handshake; the **"send the enum *symbol*, not the operator-string"** rule (CriterionModifier example); a worked **multi-criterion filter** example; the `--wait` contract (incl. the `unconfirmed` re-attach path); the **partial-update contract** (present/absent/`null`; convenience flags don't inject mutation keys) with a worked update example; the `per_page:-1`/`depth:-1` footgun; NDJSON completeness = EOF + exit code; **idempotency guidance** (no client retries; re-running a `create` may duplicate, an `update` is safe to repeat). `--dry-run` is **YAGNI** (recorded).
- README with quickstart.

## 8. Testing & gates

- **Conformance:** root-field coverage; SDL input/enum enumeration; custom-scalar round-trips (**`PluginConfigMap` and `Timestamp` called out explicitly** — see §10); recursive filter-input round-trip; **partial-update three-state golden** (present/absent/`null` survive raw-JSON binding); interface/union coverage; **canonical-type stability** (same `stash.Scene` across operations of a tier; the declared depth-tier set is present; no path-named struct outside the fragment set); **catalog coverage** (the embedded artifact covers every command incl. overrides/islands; every referenced enum/input resolves; enum value sets match the SDL); **exactly-one-`Upload`**; **mutation-set drift gate** (the set of mutations is diffed against a committed baseline → red build forces triage of any new/changed mutation into or out of `operations/overlay.yaml`, so a new destructive op can't ship ungated).
- **`genops` unit tests** over synthetic SDL fixtures (cyclic type, interface, union, `depth:Int` field, deprecated field) asserting fragment-only, depth-bounded selection; **determinism test** (`task generate` twice → byte-identical).
- **Agent-contract tests:** error-envelope golden; exit-code golden per path; **`ApiKey` redaction**.
- **`testing/synctest`** for timing/concurrency units (subscription lifecycle, `--wait`, errgroup); the live tier exercises the WS write-serialization under `-race`.
- **Two tiers:** hermetic (mock/golden; CI-safe default) + opt-in `//go:build integration` (env-gated, skipped when absent) covering the 3 subscriptions, the long-idle `--wait` keepalive/reconnect/reconcile path, and the `importObjects` island.
- **`task check`** = gofmt · build · vet · `test -race` · golangci-lint · tidy · codegen-freshness. **`task vuln`** = govulncheck.

## 9. Repo & tooling

- **Module:** `github.com/lightning-rider-999/go-stashapp`; layout per §5.
- **Scaffold precondition:** confirm the `github-alt` remote and resolved commit identity **before the first commit** (per the CLAUDE.md source-control contract) so the identity gate is checked at scaffold, not discovered at push time.
- **Go:** latest stable, `GOTOOLCHAIN=auto`; exact version pinned at scaffold time (verified).
- **`Taskfile.yml`:** `check`, `generate`, `build`, `schema`, `vuln`.
- **Versioning / stability:** SemVer. The hand-written `Client`/options/errors/subscription layer is the stable contract; generated operations + **the enumerated depth-tier type set** track upstream — a regeneration that renames/removes a generated type, or removes a depth tier, moves the **major**; adding a tier is minor. The supported Stash version range is in package docs + a programmatic constant; a runtime handshake mismatch is surfaced (§5).
- **CI:** minimal GitHub Actions (`task check` + `task vuln`); trimmable. **License:** MIT (default).

## 10. Grounding — key facts (Stash v0.31.1)

- **Surface (SDL-computed, incl. deprecated aliases):** Query **74** · Mutation **134** · Subscription **3** (`jobsSubscribe`, `loggingSubscribe`, `scanCompleteSubscribe`).
- **Custom scalars → bindings:** `Time`→`time.Time`; `Timestamp`→`string` (plain passthrough — server parses the relative forms like `">5m"`; no Go-side validation implied); `Int64`→`int64`; `Map`/`Any`→`json.RawMessage`; `BoolMap`→`map[string]bool`; `PluginConfigMap`→`map[string]any` (the SDL declares only an opaque `scalar`; `map[string]any` is looser than its *conventional* server-side `map[string]map[string]any` usage — **round-trip correctness is asserted by the §8 test, not by the SDL**); `Upload`→custom (multipart island). `ID`→`string`.
- **Interfaces/unions:** `BaseFile` (+`BasicFile`/`VideoFile`/`ImageFile`/`GalleryFile`); union `VisualFile = VideoFile | ImageFile`. `Scene.files` is concrete `VideoFile`. Inline fragments + `__typename`.
- **Filter/pagination:** `FindFilterType { q, page, per_page (-1=all, def 25), sort, direction }` + per-entity `*FilterType` (`AND`/`OR`/`NOT`) with criterion inputs sharing a `modifier`. `CriterionModifier`: `EQUALS, NOT_EQUALS, GREATER_THAN, LESS_THAN, IS_NULL, NOT_NULL, INCLUDES, INCLUDES_ALL, EXCLUDES, MATCHES_REGEX, NOT_MATCHES_REGEX, BETWEEN, NOT_BETWEEN` — the **symbol** is the wire value. `HierarchicalMultiCriterionInput.depth` (`-1`=all descendants).
- **Recursive/cyclic types:** Tag, Studio, Group (via `GroupDescription`), Folder, Scene↔performer/studio/tag/group. Several counts take `depth: Int`.
- **Uploads:** `Upload` in **exactly one** place — `importObjects(input:{file:Upload!})` (conformance-enforced). Entity images are plain `String` (URL/base64).
- **Version detection:** `query { version { version hash build_time } }`. Vendor with explicit `ref: refs/tags/vX.Y.Z` (not `develop`).
- **Deprecations** (red-build on upgrade *and* surfaced per-field in the catalog): the `movie` family → `group`; `url`→`urls`; `[Int!]` ids → `[ID!]`; rating `1–5`→`rating100`; `allScenes`/etc.
- **Danger surface (exact root-field names — the verbatim gated list, §6):** `querySQL`, `execSQL`, `metadataImport` (wipes the DB), `anonymiseDatabase`, `migrate`.
- **Async model:** `metadata*`/package/migrate mutations return a job `ID!`; progress via `jobsSubscribe` or polling `findJob`/`jobQueue`. Fire-then-track; **terminal jobs age out of the queue** (retention window verified live, §12).
- **Endpoint/auth:** GraphQL `<base>/graphql`; subscriptions `ws(s)://<base>/graphql`; header `ApiKey: <key>`.
- **genqlient:** operation-driven; supports subscriptions via a separate WS client; binds scalars via `genqlient.yaml` (custom marshaler/unmarshaler optional, round-trip correctness is the author's responsibility → §8); shared types via named fragments + `@genqlient(typename:)`; interfaces/unions via inline fragments + `__typename`; **no** multipart upload support; pointer+omitempty can't express "omit vs explicit-null" (→ §6 raw-JSON partial-update mechanism).

## 11. Cross-cutting principles

- **No hardcoding.** Instance details, paths, schema version come from env/flags/config or are *detected*. Integration tests read the instance from env and skip when absent. Only committed constant: the project identity (module path).
- **Secret handling.** The `ApiKey` is never logged, echoed in errors, or emitted in the catalog; the `RoundTripper` and slog path redact it; a secret wrapper returns a placeholder from `String()`/`LogValue()`/`MarshalJSON()`. Tested (§8).
- **Generate from the source of truth.** The vendored SDL drives the SDK, CLI tree, and catalog; drift = red build — including the **mutation-set drift gate** over the curated `destructive` overlay (§8).
- **Verify, don't assume.** Schema facts, instance version, and live-only behaviours (subscriptions, keepalive, multipart, the `field` error key, job retention) are confirmed firsthand.
- **No masking retries.** A failure surfaces; the client never silently retries. A *bounded, surfaced* subscription reconnect (the `--wait` drop contract) is not a masked retry.
- **Reach for the right library, justify deps.** genqlient, `gqlparser/v2` (build-only), `gorilla/websocket`, `x/sync/errgroup`, `cobra`, stdlib `slog`/`net/http`.

## 12. Open items / gating spikes (before or during build)

- **GATING — `genops` feasibility spike (1–2 days):** on `findScenes` → `Scene` (cyclic + `VideoFile`/`BaseFile`). Prove path-based cycle termination; inline-fragment/`__typename` emission; genqlient acceptance + compile; and that fragment-backed canonical types, depth variants, and cycle termination **coexist** (every occurrence reuses its expected fragment type, never a path-named struct). **Fallback** if impractical: curated hand-authored ops for the high-value ~20% + generated stubs — where a "stub" is still a complete, compiling, fragment-backed op meeting §2/§8 (narrows authorship effort, not the completeness bar).
- **GATING — selection-policy payload validation:** generate ~10 representative ops (shallow/medium/deep-cyclic), fetch live, record payload size + "useful in one call". Pass/fail bar. **Failure branch (mirrors the genops fallback):** a defined escalation rule — which entities graduate from ref-only to a deeper default tier, and a **cap on how many depth tiers** may be minted before it counts as a policy failure requiring redesign (rather than an open-ended explosion of the SemVer-governed tier set).
- **Subscription long-idle / keepalive (live):** run a multi-minute scan/generate; confirm the completion event arrives over an idle socket; verify the idle-reaper behaviour and which subprotocol Stash expects; and confirm the **three-state drop contract** (reconnect → resume-if-still-running → `unconfirmed` only if indeterminate, §6) holds over a real long-idle socket.
- **Job retention:** verify how long Stash retains *terminal* jobs in `findJob`/`jobQueue`, so the `--wait` reconcile can distinguish "job absent because evicted (likely succeeded)" from "job genuinely lost" — seed the wait with the launch timestamp.
- **Partial-update marshalling:** verify the raw-JSON variable-binding path (§6) actually preserves present/absent/`null` end-to-end against the live instance (not just the golden test).
- Verify Stash's GraphQL `extensions`/path expose a field-level key before relying on the envelope's `field`.
- Confirm the exact latest stable Go version at scaffold time; the CLI binary name (`stash`); the MIT default; whether `per_page:-1`/`depth:-1` emit a stderr warning.
