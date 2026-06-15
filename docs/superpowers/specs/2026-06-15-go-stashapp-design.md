# go-stashapp — Design Specification

**Status:** Approved — pre-planning · **Revision: v5** (three adversarial review rounds + two gating spikes run live against the instance)
**Date:** 2026-06-15
**Module path:** `github.com/lightning-rider-999/go-stashapp`
**Target API:** Stash GraphQL. Pinned to the live instance's reported version — **confirmed genuinely v0.31.1** (handshake against `http://192.168.0.46:6970/graphql`, build_time 2026-05-03). The pin is *verified, not assumed*.

> **Revision history.** v1→review(38 findings)→v2→verify(36/38 + seams)→v3→verify(seam reconcile)→v4. **v5 backports the §12 gating-spike outcomes** (both spikes run live): the "generate everything" strategy is **PROVEN feasible end-to-end** (real `genqlient` codegen + typed decode of 1,410 live scenes) and the selection policy is **validated and locked** — with six concrete corrections the spikes surfaced (tagged A1–A6 / B1–B6 below). The implementation plan (`docs/superpowers/plans/2026-06-15-go-stashapp.md`) is derived from this revision.
> **Goal framing (your words):** "enable all functionality."

---

## 1. Purpose & goals

- **SDK** — a *schema-complete*, faithfully-typed Go client for Stash's GraphQL API; importable with **idiomatic, stable** canonical types.
- **CLI** — exposes the SDK *completely*, **agent-first** (primary operator an LLM agent; a partner skill comes later, out of scope). Humans secondary.

## 2. Definition of "schema-complete" (locked)

All of Stash's functionality enabled. Three enforced criteria: (1) every root operation callable (conformance diff); (2) every input/enum/scalar bound (SDL-enumeration + scalar round-trips); (3) every return type representable — bounded by the selection policy (§4), since the schema is cyclic. Surface, computed from the SDL: **74 queries + 134 mutations + 3 subscriptions = 211 operations**, *including* the deprecated aliases (§3).

## 3. Scope

- **In:** the whole schema — read, write, operational/admin, subscriptions.
- **Deprecated/superseded operations** (the `movie` family — `findMovie(s)`, `allMovies`, `movieCreate/Update/Destroy`, `scrapeSingleMovie/MovieURL` — superseded by `group`) are generated for literal completeness + drift-safety, flagged deprecated in the catalog. "All functionality" is the *non-deprecated* set; the aliases ride along in the 211.
- **Auth: ApiKey header only.** Username/password session auth is deliberately out of scope (own instance, issued key).
- **Out:** writing the partner agent skill.

## 4. Core architecture — codegen strategy

**Goal: enable all functionality.** Generation is the *means*: `genops` emits operations for the entire surface from the vendored SDL; we curate by evidence; and a hand-authored fallback for parts of the surface is on-intent if generation proves impractical. `internal/genops` (an SDL-AST→operation compiler over `gqlparser/v2`) is the project core. **Spike A proved this works end-to-end** (genqlient v0.8.1, deterministic output, live typed decode).

### Default selection policy (validated live — Spike B)
- **Scalars/enums:** select all on the target type.
- **Named related objects → REF fragments.** A ref is **`{id, name/title}` — `name`/`title` is MANDATORY, never id-only [B1]** (an id-only "ref" is useless: 461 B/row, forces a follow-up just to get a title).
- **Cyclic/self-referential edges** (Tag, Studio, Group via `GroupDescription`, Folder, Scene↔studio/performer/tag/group): **ref-only by default [B2]**, with explicit depth-parameterized variant operations for deeper reach. *Justification (measured): expanding `tags` one level alone = 134 KB across 25 scenes (~27× the ref cost) and triples per-row size — this is the policy's whole reason to exist.*
- **Cycle termination: path-based** — a type revisited on the current selection path collapses to its `Ref`; breaks mutual cycles (Scene→Studio→Scene), global max-depth backstop.
- **Default `per_page = 25` [B3]** (matches the SDL default; deep scenes ≈ 3 KB/row / 75 KB per page, shallow < 1 KB). `per_page` is a strict linear multiplier (pp250 deep ≈ 344 KB) — large `per_page`, not depth, is the common payload blow-up; keep the `per_page:-1` footgun warning (§6).
- **`Map`/`custom_fields`:** raw JSON.

> **Two recursion axes:** output selection depth is a hand-bounded *policy*; input recursion (`*FilterType` `AND`/`OR`/`NOT`, `HierarchicalMultiCriterionInput.depth`) is verbatim variable-side data through genqlient's recursive input structs (round-trip tested, §8).

### Canonical types (public-API decision — locked, with the flatten mechanism)
One canonical shared Go struct per entity via **mandated named fragments** (`fragment SceneFields on Scene`, `fragment SceneRef on Scene`) + `@genqlient(typename:)` for clean names (`stash.Scene`, `stash.SceneRef`). Public type names are NOT a function of selection depth; depth variants are their own named fragment-backed types (`stash.SceneDeep`).

- **[A1] `flatten` is the load-bearing mechanism.** genqlient reuses a fragment's Go type for a field ONLY if that field's selection is a single fragment-spread carrying `# @genqlient(flatten: true)`. `genops` MUST emit `flatten` on every nested ref/object field, on the line *above the field* — **never above the operation** (it binds to the first variable-definition and errors). Without it, genqlient mints path-named structs for every nested spread and the canonical-type model silently fails. *(Spike A: confirmed — `SceneFields.Studio *StudioRef`, `.Tags []TagRef`, `.Files []VideoFileFields` only materialize with flatten; generated.go dropped 5135→3583 lines with it.)*
- **[A3] Path-named exceptions (the "no path-named struct" invariant is relaxed to an allowlist).** Two cases unavoidably keep a path-named type, recorded in an explicit allowlist: **(a) mixed-selection wrappers** — `SceneGroup {scene_index, group{...}}`, `SceneMovie`, `*FilterType` internals — where the selection isn't a single fragment-spread (the inner object still flattens to a `Ref`); **(b) union-typed fields** (`Image.visual_files`) get a path-named interface. The §8 stability test fails on any path-named type *not* in the allowlist.
- **Fragment-only discipline:** every entity occurrence spreads exactly one named fragment; path-based termination chooses *which* (`SceneFields` vs `SceneRef`).

## 5. SDK architecture & wiring

### Layout (module `github.com/lightning-rider-999/go-stashapp`)
- **`stash/`** — public surface: `Client`, options, typed errors, subscription helpers, the `importObjects` island, genqlient-generated canonical types. The hand-written `Client`/options/errors/subscription layer is the stable contract.
- **`schema/`** — vendored SDL + `version.txt` + generated version constant + the generated, `go:embed`-ed **`catalog.json`** (the build-time machine contract, §6).
- **`internal/genops/`** — the generator (`tool` directive; never `tools.go`).
- **`operations/generated/`**, **`operations/overrides/`**, **`operations/overlay.yaml`** (curated `{destructive, job-returning}`, the one auditable curated input).
- **`cmd/stash/`** — CLI. **`internal/conformance/`** — gates.

### `internal/genops` obligations (one `gqlparser/v2` SDL-AST pass; genqlient + gqlparser are build-only; nothing re-parses SDL at runtime; deterministic output)
1. A complete named operation per root field: collision-free name; a variable per argument typed verbatim from the SDL — **[A6] prefer the `ids:[ID!]` form over the `@deprecated` `scene_ids/image_ids/performer_ids:[Int!]`**; variables forwarded; selection via fragment-only spreads with **`flatten` on every nested field [A1]**; **`__typename` on every interface/union selection [A5]**.
2. The **manifest** (`operations/manifest.json`, thin per-op index incl. overrides/islands, reading `overlay.yaml`) AND the resolved **`schema/catalog.json`** — both from the same pass.
3. Override resolution: parse each override (same AST) to extract its single root field (build error if zero/multiple), emit a generated op only for uncovered fields, still index overrides/islands.
- **[B5] Object edges are enumerated strictly from the SDL AST, never a hand-list.** *(Spike B: `Performer` has no `studios` edge — a hand-list assuming one emits an invalid field. Its real edges are `tags/scenes/groups/stash_ids`.)*
- **[A4] The `BaseFile` interface / `VisualFile` union are NOT reachable from Scene.** `Scene.files: [VideoFile!]!` and `Gallery.files: [GalleryFile!]!` are concrete — Scene needs only a concrete `VideoFile` fragment. The interface lives at `findFile(s)` (`BaseFile!`) and `Folder.zip_file` (`BasicFile`, concrete); the union only at `Image.visual_files`. The interface/union machinery is proven and tested on `findFiles`/`findImages`, not Scene.
- **[A5] Islands need `__typename`.** Interface/union fields decode via a generated custom `UnmarshalJSON` keyed on `__typename`; any hand-authored override/island selecting such a field MUST include `__typename` or decode panics.

### Wiring
- **Transport:** `net/http`, configurable `*http.Client`; `ApiKey` via `RoundTripper`; genqlient consumes the `Do(*http.Request)` shape (outbound only). HTTP client for query/mutation; a separate websocket client for the 3 subscriptions.
- **URL normalize:** base UI URL → `/graphql`; derive `ws(s)://…/graphql`.
- **Timeouts:** default bounded `http.Client.Timeout` on the GraphQL path only — not websocket/`--wait` (ctx-cancel only).
- **Options/config:** functional options + env fallback (`STASHAPP_URL`, `STASHAPP_API_KEY`). Nothing hardcoded (§11).
- **Errors:** typed — transport vs `*GraphQLError` (carries the error list) vs auth; `%w` chains; `errors.As`-friendly.
- **Concurrency:** `errgroup.WithContext` (first error cancels) + `SetLimit`; no hidden retries.
- **Subscriptions:** genqlient's `NewClientUsingWebSocket` owns the protocol/lifecycle; the hand-written piece is a thin `graphql.Dialer`/`WSConn` adapter over `gorilla/websocket` that dials, injects `ApiKey`, **serializes all writes** (single-writer goroutine — gorilla forbids concurrent writers; keepalive coexists with protocol frames), owns client-side keepalive (Stash sends none on `graphql-transport-ws`, §12), and a bounded surfaced reconnect.
- **Diagnostics:** `log/slog`, optional logger; ApiKey never logged (§11).
- **Version handshake:** `Client.Version(ctx)`; mismatch → SDK warns, CLI surfaces a distinct exit code / envelope field.

## 6. CLI — agent-first

Generated from `manifest.json`; framework `spf13/cobra`. Grammar `stash <resource> <verb>`. Every subscription → a streaming command: `stash job watch`, `stash log tail`, `stash scan watch`.

- **Output:** JSON by default always; lists/streams → NDJSON; `-o table|yaml` for humans; no field filtering (pipe to `jq`). NDJSON completeness = stdout EOF + zero exit (errors to stderr only); `job watch` adds a terminal marker.
- **Input:** JSON-first (stdin/`--input`). Convenience flags (`--q/--page/--per-page/--sort/--direction/--id`) are **read/list ergonomics only — they never inject keys into a mutation payload.**
- **Partial-update contract & mechanism:** send exactly the keys present in the input JSON; omit absent; pass explicit `null` as "clear" — three distinct wire states. **Mechanism:** stdlib `encoding/json` can't distinguish "omit" from "explicit null" on genqlient's pointer-typed input structs, so partial-update mutation inputs are bound as **raw JSON** (`json.RawMessage`/`map[string]any`), not the typed structs. Golden-tested (§8).
- **[B4] ApiKey redaction.** The default scene tier selects `paths`, and `paths.stream` embeds the full ApiKey JWT (`?apikey=eyJ…`). `paths` stays in the default tier, but the CLI output layer **redacts the `apikey` query param** from any URL. A §8 golden test asserts no ApiKey JWT appears in default-policy output. *(Spike B: the single most important finding — directly intersects §11.)*
- **`per_page:-1`/`depth:-1`:** passed verbatim; footgun documented in `AGENTS.md`.

### `stash catalog` — build-time embedded artifact
A static `schema/catalog.json` emitted by the `genops` SDL pass and `go:embed`-ed; `stash catalog` prints it verbatim (no runtime parsing). Contents: `catalog_format_version` + the targeted Stash SDL version (live-instance drift is the §5 handshake's job); per command — resource/verb/summary, `destructive` flag, `job-returning` flag, and the derived set of exit codes (base + `not-found` for lookups + `destructive-refused` iff destructive + `job-failed`/`still-running`/`unconfirmed` iff job-returning), emitted as the kebab name; inputs fully transitively resolved via a `$defs` dictionary (nested inputs, `*FilterType` criterion shapes, `AND/OR/NOT`); every reachable enum's value set as **API symbols** (description in a separate field); `@deprecated` reason per field (incl. the `[Int!]` id variants, [A6]).

### Errors & exit codes
Envelope on stderr `{ error: { code, message, graphql_errors[], field, retryable } }` (`retryable` advisory only; `field` best-effort, §12). Taxonomy = a single frozen `(name, integer)` table in `AGENTS.md`; envelope `code` = the name, exit status = the integer. Names: `ok, usage, auth, transport, validation, server-fault, not-found, destructive-refused, job-failed, still-running, unconfirmed`.

### Async / jobs (`--wait`)
Seeds with a `findJob` poll on attach (closes the launch→subscribe race), then tracks `jobsSubscribe`. Terminal success → `ok`; terminal failure → `job-failed`. `--wait-timeout` default **unset = no client bound** (long scans run for hours). **Drop reconcile (three-state):** bounded reconnect → `findJob` poll → **terminal**=exit accordingly / **confirmed still-running**=resume waiting (don't exit) / **indeterminate** (server unreachable or job aged out of the queue)=exit `unconfirmed` + print job id (never `job-failed`, so an agent won't re-run a succeeded op). Destructive gating: `querySQL`, `execSQL`, `metadataImport`, `anonymiseDatabase`, `migrate` (exact root-field names — same verbatim list, §10) behind `--yes-i-understand` → `destructive-refused`. Config env+flags; config file YAGNI. One static binary `cmd/stash`.

## 7. Documentation & DX
godoc on every exported symbol + runnable `Example`s; generated `docs/cli/`; **`docs/AGENTS.md`** (the `(name,integer)` exit-code table, error envelope, the "send the enum *symbol* not the operator-string description" rule, a worked multi-criterion filter, the `--wait` contract incl. `unconfirmed` re-attach, the partial-update contract, the `per_page:-1` footgun, idempotency guidance — re-running a `create` may duplicate, `update` is safe); README quickstart. `--dry-run` YAGNI.

## 8. Testing & gates
- **Conformance:** root-field coverage; SDL input/enum enumeration; scalar round-trips (**`PluginConfigMap` + `Timestamp` called out**); interface/union coverage on **`findFiles`/`findImages`, not Scene [A4]**; recursive filter-input round-trip; partial-update three-state golden; **canonical-type stability with the [A3] exception allowlist**; catalog coverage; exactly-one-`Upload`; **mutation-set drift gate** (new mutation → red build forces triage into `overlay.yaml`); **ApiKey redaction incl. `paths.stream` [B4]**; determinism (regen twice byte-identical — *Spike A confirmed achievable*).
- **`genops` unit tests** over synthetic SDL fixtures (cyclic type, interface, union, `depth:Int` field, deprecated field) asserting fragment-only, depth-bounded, **flatten-correct** selection; determinism test.
- **Agent-contract tests:** error-envelope golden; exit-code golden; ApiKey redaction.
- **`testing/synctest`** for subscription lifecycle / `--wait` / errgroup; two tiers — hermetic (mock/golden, CI-safe) + opt-in `//go:build integration` (env-gated, skipped when absent) covering the 3 subs, the idle-`--wait` keepalive/reconnect/reconcile path, and `importObjects`.
- **`task check`** = gofmt · build · vet · `test -race` · golangci-lint · tidy · codegen-freshness. **`task vuln`** = govulncheck.

## 9. Repo & tooling
- **Module** `github.com/lightning-rider-999/go-stashapp`; layout §5. **Scaffold precondition:** confirm the `github-alt` remote + resolved commit identity before the first commit.
- **Go:** latest stable (`GOTOOLCHAIN=auto`; instance machine on **1.26.4**). **[A2] Toolchain blocker (mandatory):** genqlient v0.8.1 transitively pins `golang.org/x/tools@v0.24.0`, which **does not compile under Go 1.26** (`tokeninternal.go: invalid array length`). Force `golang.org/x/tools ≥ v0.46.0` (explicit `require`/`replace`). `go get -tool` re-pins it to the broken version → re-bump after, in the scaffold and the Taskfile `generate`. Pins observed: genqlient v0.8.1, `gqlparser/v2` v2.5.x (build-only, may pin newer than genqlient's transitive v2.5.19), `google/uuid` (genqlient ws runtime).
- **`Taskfile.yml`:** `check`, `generate`, `build`, `schema`, `vuln`.
- **Versioning:** SemVer; the hand-written layer is the stable contract; generated operations + the enumerated depth-tier set track upstream (rename/remove = major; add a tier = minor). Supported Stash range in package docs + a programmatic constant.
- **CI:** minimal Actions (`task check` + `task vuln`). **License:** MIT.

## 10. Grounding — schema facts (Stash v0.31.1, spike-confirmed)
- **Surface:** Query 74 · Mutation 134 · Subscription 3 (`jobsSubscribe`, `loggingSubscribe`, `scanCompleteSubscribe`).
- **Scalar bindings (all decoded live):** `Time→time.Time`, `Timestamp→string` (plain passthrough; server parses relative forms like `">5m"`), `Int64→int64`, `Map`/`Any→json.RawMessage`, `BoolMap→map[string]bool`, `PluginConfigMap→map[string]any` (SDL declares only an opaque `scalar`; this is looser than its *conventional* server-side shape — round-trip correctness is §8-tested, not SDL-declared), `Upload→stash.Upload` (multipart island). `ID→string`.
- **Files [A4]:** `Scene.files: [VideoFile!]!` and `Gallery.files: [GalleryFile!]!` are CONCRETE. Interface `BaseFile` (`BasicFile`/`VideoFile`/`ImageFile`/`GalleryFile`) only at `findFile(s)` + `Folder.zip_file`; union `VisualFile = VideoFile | ImageFile` only at `Image.visual_files`. Inline fragments + `__typename`.
- **Filter/pagination:** `FindFilterType { q, page, per_page (-1=all, default 25), sort, direction }` + per-entity `*FilterType` (`AND/OR/NOT`); `CriterionModifier` (the **symbol** is the wire value): `EQUALS, NOT_EQUALS, GREATER_THAN, LESS_THAN, IS_NULL, NOT_NULL, INCLUDES, INCLUDES_ALL, EXCLUDES, MATCHES_REGEX, NOT_MATCHES_REGEX, BETWEEN, NOT_BETWEEN`. `HierarchicalMultiCriterionInput.depth` (`-1`=all descendants). `IntCriterionInput{value,value2,modifier}` round-trips (confirmed live).
- **Edges from the SDL only [B5]:** e.g. `Performer` edges are `tags/scenes/groups/stash_ids` — there is NO `Performer.studios`. Recursive/cyclic: Tag, Studio, Group (via `GroupDescription`), Folder, Scene↔performer/studio/tag/group; several counts take `depth: Int`.
- **Deprecated id args [A6]:** `scene_ids/image_ids/performer_ids: [Int!]` → use `ids: [ID!]`.
- **Uploads:** `Upload` in exactly one place — `importObjects(input:{file:Upload!})` (conformance-enforced). Entity images are plain `String`.
- **Version detection:** `query { version { version hash build_time } }`. Vendor with explicit `ref: refs/tags/vX.Y.Z` (not `develop`).
- **Deprecations** (red build on upgrade + surfaced per-field in the catalog): `movie`→`group`; `url`→`urls`; `[Int!]` ids→`[ID!]`; rating `1–5`→`rating100`; `allScenes`/etc.
- **Danger surface (verbatim gated list):** `querySQL`, `execSQL`, `metadataImport`, `anonymiseDatabase`, `migrate`.
- **Async:** `metadata*`/package/migrate return a job `ID!`; progress via `jobsSubscribe` or polling `findJob`/`jobQueue`; **terminal jobs age out of the queue** (retention window verified live, §12).
- **Endpoint/auth:** `<base>/graphql`; subscriptions `ws(s)://<base>/graphql`; header `ApiKey: <key>`.
- **genqlient (v0.8.1, spike-confirmed):** operation-driven; subscriptions via a separate WS client; scalar bindings via `genqlient.yaml`; **canonical types require `flatten` [A1]**; interfaces/unions via inline fragments + `__typename`; no multipart upload; pointer+omitempty can't express omit-vs-explicit-null (→ §6 raw-JSON partial-update).

## 11. Cross-cutting principles
- **No hardcoding** (env/flags/config or detected; integration tests env-gated/skipped; only committed constant is the module path).
- **Secret handling.** ApiKey never logged/echoed/in-catalog; `RoundTripper` + slog redact; secret wrapper placeholder; **and the CLI output layer redacts the `apikey` param leaked by `paths.stream` [B4]** — §8-tested.
- **Generate from the source of truth** (SDL drives SDK + CLI + catalog; drift = red build, incl. the mutation-set drift gate over the curated `destructive` overlay).
- **Verify, don't assume** (schema facts, instance version, live-only behaviours, job retention — confirmed firsthand; this revision is spike-backed).
- **No masking retries** (a bounded, surfaced subscription reconnect is not a masked retry).
- **Reach for the right library, justify deps:** genqlient, `gqlparser/v2` (build-only), `gorilla/websocket`, `x/sync/errgroup`, `cobra`, stdlib `slog`/`net/http`.

## 12. Open items / spikes
- **DONE — genops feasibility spike:** PROVEN end-to-end against the live v0.31.1 instance (fragments+flatten → canonical types, path-based cycle termination, interface/union dispatch on `findFiles`/`findImages`, all scalar bindings, deterministic codegen, typed decode of 1,410 scenes). Corrections A1–A6 folded into §4/§5/§10.
- **DONE — selection-policy payload spike:** policy PASSES as the default (refs `{id,name/title}` 33 B–3 KB/row useful-in-one-call; id-only too thin; one level of cyclic expansion ~27×). Corrections B1–B5 folded into §4/§6/§11.
- **REMAINING [B6] — seeded-instance re-validation:** the live instance had 0 galleries/images/groups, so the Gallery/Image/Group + `GroupDescription` cyclic branches were validated by schema shape only. Re-validate payloads + cycle termination against an instance with non-empty data before locking those branches (Task 22 acceptance criterion).
- **Job retention:** verify how long Stash keeps *terminal* jobs in `findJob`/`jobQueue` so the `--wait` reconcile distinguishes "evicted (likely succeeded)" from "genuinely lost"; seed the wait with the launch timestamp.
- Verify Stash's GraphQL `extensions`/path expose a field-level key before relying on the envelope `field`. Confirm the CLI binary name (`stash`) and the MIT default; whether `per_page:-1`/`depth:-1` emit a stderr warning.
