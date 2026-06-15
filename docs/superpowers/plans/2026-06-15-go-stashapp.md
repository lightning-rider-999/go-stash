# go-stashapp Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a schema-complete Go SDK + agent-first CLI for Stash's GraphQL API, generated from the vendored SDL, hardened by a conformance suite.

**Architecture:** A build-time generator (`genops`) walks the vendored SDL (gqlparser/v2) and emits one genqlient operation per root field plus a manifest and a machine-facing catalog; genqlient turns those into a typed Go client exposed as canonical fragment-backed types; a cobra CLI is generated from the manifest. Completeness, canonical-type stability, scalar round-trips, and ApiKey redaction are all conformance-gated (drift = red build).

**Tech Stack:** Go 1.26.x (`GOTOOLCHAIN=auto`); genqlient v0.8.1 (`tool` directive) with a forced `golang.org/x/tools ≥ v0.46.0`; `vektah/gqlparser/v2` (build-only, latest); `gorilla/websocket`; `golang.org/x/sync/errgroup`; `spf13/cobra`; stdlib `log/slog`/`net/http`. Module `github.com/lightning-rider-999/go-stashapp`, branch `feat/plan`.

**User decisions (already made):** "schema-complete SDK + agent-first CLI"; "generate everything, then curate by evidence" reframed to "enable all functionality, generation is the means"; "Entity fragments → canonical types"; CLI framework Cobra; "lock v4, commit on feat/plan, push"; "do any spikes you need to"; ApiKey-only auth (session auth out of scope); deprecated aliases generated but flagged.

---

## Spike grounding & corrections to the spec (v4)

The two §12 gating spikes ran live against the instance (genuinely **v0.31.1**, `http://192.168.0.46:6970/graphql`). The strategy is **feasible-with-caveats**. The following corrections are authoritative over spec v4 where they conflict; back-port them to a spec v5 erratum (offered separately):

- **A1 — `flatten` is mandatory.** Canonical types only materialize if every nested single-fragment-spread field carries `# @genqlient(flatten: true)`. `genops` MUST emit it on every ref/object field; NEVER on the line before an operation (it binds to the first variable-definition and errors). Without it: path-named structs everywhere, canonical-type test fails.
- **A2 — `x/tools` bump is a hard prerequisite.** genqlient v0.8.1 pins `x/tools@v0.24.0` which fails to compile under Go 1.26 (`tokeninternal.go: invalid array length`). Force `x/tools ≥ v0.46.0`. `go get -tool` re-pins it to the broken version → re-bump after, in the scaffold and Taskfile.
- **A3 — path-named exceptions exist.** Relax the §4/§8 "no path-named struct" invariant to carve out (a) mixed-selection wrappers (`SceneGroup`, `SceneMovie`, `*FilterType` internals) and (b) union-typed fields (`Image.visual_files`). The conformance test encodes an explicit exception allowlist; everything else must be a canonical fragment type.
- **A4 — Scene uses the concrete `VideoFile`, not the interface.** `Scene.files: [VideoFile!]!`, `Gallery.files: [GalleryFile!]!`. The `BaseFile` interface is reachable only at `findFile(s)` and `Folder.zip_file`; the `VisualFile` union only at `Image.visual_files`. Interface/union machinery is proven and tested on `findFiles`/`findImages`, not Scene.
- **A5 — islands need `__typename`.** Interface/union fields decode via a generated custom `UnmarshalJSON` keyed on `__typename`; any hand-authored override/island selecting such a field MUST include `__typename` or decode panics.
- **A6 — prefer `ids:[ID!]`.** `scene_ids/image_ids/performer_ids:[Int!]` are `@deprecated`; generate the `ids:[ID!]` form by default, flag the `[Int!]` variants deprecated in the catalog.
- **B1 — refs are `{id, name/title}`, never id-only.** id-only "refs" are useless (461 B/row, force a follow-up). Make name/title mandatory in every ref fragment.
- **B2 — cyclic edges stay ref-only.** Quantified: expanding `tags` one level = 134 KB/25 scenes (~27× the ref cost). This is the policy's core justification.
- **B3 — default `per_page = 25`.** Linear multiplier (pp250 deep ≈ 344 KB); large `per_page`, not depth, is the common payload blow-up. Keep the `per_page:-1` footgun warning.
- **B4 — `Scene.paths.stream` leaks the ApiKey JWT** (`?apikey=eyJ...`). Decision: **keep `paths` in the default scene tier but redact the `apikey` query param in the CLI output layer**, and add a conformance/golden test asserting no ApiKey JWT appears in default-policy output. Intersects §11.
- **B5 — enumerate edges from the SDL AST, never a hand-list.** `Performer.studios` does not exist (spec implied it); a hand-list drifts and emits invalid fields.
- **B6 — coverage gap.** The instance has 0 galleries/images/groups, so Gallery/Image/Group + `GroupDescription` cyclic branches were validated by schema-shape only. Re-validate against a seeded instance before locking those branches (Task 22 acceptance criterion + Task 5 note).

Confirmed-good (no change): the §10 scalar bindings all decode; enum symbols are the wire values; determinism/gofmt/`tool`-directive are achievable; `IntCriterionInput` round-trips; surface = 74 Q / 134 M / 3 S.

---

## File structure

- `go.mod`, `go.sum` — module + the genqlient `tool` directive + forced `x/tools` (A2).
- `Taskfile.yml` — `check`, `generate`, `build`, `schema`, `vuln`.
- `schema/` — vendored SDL (`*.graphql`), `version.txt` stamp, generated `version_gen.go` (programmatic constant), `catalog.json` (generated, `go:embed`).
- `internal/genops/` — the SDL→operations/manifest/catalog compiler: `parse.go` (AST + edge enumeration), `fragments.go` (ref + full fragments, flatten, typename), `cycle.go` (path-based termination + exception allowlist), `operations.go` (operation assembly, variables, `__typename`), `manifest.go`, `catalog.go`, `genops_test.go`.
- `operations/generated/`, `operations/overrides/`, `operations/overlay.yaml` (curated `{destructive,job-returning}`).
- `genqlient.yaml` — schema + operations globs + scalar bindings.
- `stash/` — public SDK: `client.go`, `options.go`, `errors.go`, `subscriptions.go`, `batch.go`, `version.go`, `importobjects.go` (island), plus genqlient-generated `*_gen.go` (canonical types).
- `cmd/stash/` — `main.go`, `gen_commands.go` (from manifest), `output.go`, `input.go`, `errors.go`, `catalog.go`, `wait.go`, `stream.go`.
- `internal/conformance/` — completeness, scalar, interface/union, canonical-stability (+exceptions), catalog, drift, redaction tests.
- `docs/cli/` (generated), `docs/AGENTS.md`, `README.md`.

---

## Phase 0 — Spikes (DONE)

Both gating spikes are complete (see "Spike grounding" above). genops feasibility: **proven**. Selection policy: **validated, locked** (refs `{id,name/title}`, cyclic edges ref-only, `per_page=25`). Residual: re-validate empty branches against a seeded instance (Task 22 AC; Task 5 note). No task to execute here — outcomes are baked into the tasks below.

---

## Phase 1 — Scaffold & tooling

### Task 1: Module scaffold, go.mod with the x/tools bump, Taskfile skeleton

**Goal:** A compiling, gofmt-clean module on `feat/plan` with the genqlient toolchain wired and the x/tools blocker resolved.

**Files:** Create `go.mod`, `Taskfile.yml`, `tools.go`?(NO — use `tool` directives), `.golangci.yml`, `doc.go`.

**Acceptance Criteria:**
- [ ] `go build ./...` and `go vet ./...` exit 0 on an empty scaffold.
- [ ] `go.mod` has `tool github.com/Khan/genqlient` AND forces `golang.org/x/tools v0.46.0` (or newer) via an explicit `require`.
- [ ] `task --list` shows `check generate build schema vuln`.

**Verify:** `go build ./... && go vet ./... && gofmt -l . | tee /dev/stderr | wc -l` → builds clean, `0` unformatted files.

**Steps:**
- [ ] Init module (already `git init`'d on `feat/plan`): `go mod init github.com/lightning-rider-999/go-stashapp` (skip if present).
- [ ] Add deps: `go get github.com/spf13/cobra@latest github.com/gorilla/websocket@latest golang.org/x/sync@latest github.com/vektah/gqlparser/v2@latest`.
- [ ] Add genqlient as a tool: `go get -tool github.com/Khan/genqlient@v0.8.1` (this records generate-only sums in go.sum), **then immediately** `go get golang.org/x/tools@v0.46.0` to undo the broken re-pin (A2). Add a comment in `go.mod` explaining the bump.
- [ ] Write `Taskfile.yml` with: `generate` = `go generate ./...` (which runs genops then genqlient, ordered); `check` = gofmt -l, `go build ./...`, `go vet ./...`, `go test -race ./...`, `golangci-lint run`, `go mod tidy -diff`, codegen-freshness (regenerate + `git diff --exit-code`); `schema` = the SDL refresh script; `vuln` = `govulncheck ./...`.
- [ ] Commit: `git commit -m "chore: scaffold module + genqlient toolchain (x/tools bumped for Go 1.26)"`.

### Task 2: Vendor the SDL + version stamp + `task schema`

**Goal:** `schema/` holds the v0.31.1 SDL pinned to the tag, stamped, with a refresh task and a generated Go version constant.

**Files:** Create `schema/*.graphql` (30 files), `schema/version.txt`, `schema/gen.go` (`//go:generate` for the stamp), `schema/version_gen.go`, `scripts/refresh-schema.sh`.

**Acceptance Criteria:**
- [ ] `schema/` contains all 30 `graphql/schema/**/*.graphql` files from `refs/tags/v0.31.1`.
- [ ] `schema/version.txt` = `v0.31.1`; `stash.SchemaVersion` constant equals it.
- [ ] `task schema` re-pulls at the pinned tag and re-stamps idempotently.

**Verify:** `task schema && git diff --exit-code schema/` → no diff (idempotent).

**Steps:**
- [ ] `scripts/refresh-schema.sh`: use `gh api` to list+fetch `graphql/schema` tree at `refs/tags/$(cat schema/version.txt)` into `schema/` (verified working in the spike via the gh API). Pin the ref explicitly — never `develop`.
- [ ] Generate `schema/version_gen.go` exposing `const SchemaVersion = "v0.31.1"`.
- [ ] Commit: `git commit -m "feat(schema): vendor Stash v0.31.1 SDL + version stamp"`.

---

## Phase 2 — genops compiler (project core)

> genops is decomposed into focused sub-tasks. Each emits to `operations/generated/` + the manifest/catalog. The spike validated the exact mechanisms; the code below is grounded in it.

### Task 3: SDL parse + edge enumeration from the AST

**Goal:** Load the vendored SDL into a gqlparser AST and enumerate, per object type, its scalar/enum fields and its object/list edges — strictly from the schema (B5).

**Files:** Create `internal/genops/parse.go`, `internal/genops/parse_test.go`.

**Acceptance Criteria:**
- [ ] `LoadSchema(dir)` returns a `*ast.Schema` over all 30 files without error.
- [ ] `Edges(t)` returns object/list-of-object fields; `Scalars(t)` returns scalar/enum fields; neither invents fields (a `Performer.studios` lookup returns not-found, per B5).
- [ ] Root operation fields enumerated: 74 Query, 134 Mutation, 3 Subscription.

**Verify:** `go test ./internal/genops/ -run TestParse -v` → counts match; `Performer` edges = `{tags, scenes, groups, stash_ids}` (NOT `studios`).

**Steps (TDD):** failing test asserting the root-field counts + Performer edge set off the vendored SDL → implement with `gqlparser/v2` `LoadSchema` → green → commit.

### Task 4: Ref + full fragments with flatten and canonical typenames

**Goal:** Emit one `fragment <Entity>Ref` (`{id, name/title}` — B1) and one `fragment <Entity>Fields` per entity, with `@genqlient(typename: <Entity>Ref/...)`, and emit `# @genqlient(flatten: true)` on every nested single-fragment-spread field (A1).

**Files:** Create `internal/genops/fragments.go`, `fragments_test.go`.

**Acceptance Criteria:**
- [ ] Every entity gets a `Ref` fragment selecting exactly `{id, <displayField>}` where `<displayField>` is `name` or `title` resolved from the SDL (error if neither exists and the entity is used as a ref).
- [ ] Every nested object/ref field in a full fragment is rendered as a single fragment-spread carrying `# @genqlient(flatten: true)` on the line ABOVE the field (never above the operation — A1).
- [ ] Generated `.graphql` for `findScenes` produces, after genqlient, `SceneFields.Studio *StudioRef`, `.Tags []TagRef`, `.Performers []PerformerRef`, `.Files []VideoFileFields` (canonical types, verified in spike).

**Verify:** `task generate && go build ./...` → compiles; `grep -c 'flatten: true' operations/generated/*.graphql` > 0; `go test ./internal/conformance -run TestCanonicalTypes` (Task 22) green.

**Steps:** failing test on the emitted fragment text for `Scene` (asserts `flatten` placement + ref shape) → implement emission → run genqlient → assert canonical field types via reflection in the conformance test → commit.

### Task 5: Path-based cycle termination + path-named exception allowlist

**Goal:** Terminate selection on revisiting a type already on the path (emit its `Ref`), and explicitly handle the unavoidable exceptions (A3): mixed-selection wrappers (`SceneGroup`/`SceneMovie`) and union-typed fields (`Image.visual_files`).

**Files:** Create `internal/genops/cycle.go`, `cycle_test.go`; `internal/genops/exceptions.go` (the allowlist).

**Acceptance Criteria:**
- [ ] A type revisited on the current selection path is emitted as `<Type>Ref`, never recursed (Scene→Studio→Scene terminates).
- [ ] Mixed-selection wrappers emit `{scalar fields + nested ref-spread}`; their wrapper struct is path-named and recorded in the exception allowlist; the inner object still flattens to a `Ref`.
- [ ] Union fields emit a named selection with `__typename` + per-member inline fragments; the resulting type is recorded in the allowlist.
- [ ] `exceptions.go` lists exactly the allowlisted path-named types; the conformance test (Task 22) treats anything path-named-and-not-listed as a failure.

**Verify:** `go test ./internal/genops -run TestCycle -v`; conformance canonical-stability test passes WITH the allowlist (Task 22). **Note (B6):** Group/Folder/GroupDescription cycles are syntactically validated but unmeasured on the empty instance — Task 22 AC requires a seeded re-check before this is considered locked.

**Steps:** failing test asserting termination on a synthetic cyclic fixture + the two exception shapes → implement path-stack walk → green → commit.

### Task 6: Operation assembly (variables, ids:[ID!], __typename for interfaces/unions)

**Goal:** Assemble a complete, named, genqlient-valid operation per root field: a deterministic operation name, a variable declaration per argument typed verbatim from the SDL (preferring `ids:[ID!]` over deprecated `[Int!]` — A6), variables forwarded, the selection from Tasks 4–5, `__typename` where interfaces/unions appear (A5).

**Files:** Create `internal/genops/operations.go`, `operations_test.go`.

**Acceptance Criteria:**
- [ ] One operation per root field; names collision-free and stable (sorted).
- [ ] Args typed from the SDL preserving `!`/nested inputs; `findScenes` uses `ids:[ID!]` form.
- [ ] Interface/union selections include `__typename` (decode-safe).
- [ ] Output is deterministic: running twice yields byte-identical files.

**Verify:** `task generate && task generate && git diff --exit-code operations/generated/` → byte-identical (determinism gate; verified achievable in spike).

### Task 7: manifest.json + catalog.json + overlay.yaml

**Goal:** From the same AST pass, emit `operations/manifest.json` (thin per-op index incl. overrides/islands, reading `operations/overlay.yaml` for `{destructive, job-returning}`) and the resolved `schema/catalog.json` (transitive `$defs` inputs, enum value sets as symbols + separate descriptions, `@deprecated` reasons, per-command derived exit-code sets).

**Files:** Create `internal/genops/manifest.go`, `catalog.go`, `manifest_test.go`; `operations/overlay.yaml` (seed with the §10 danger set + the metadata-job-returning mutations).

**Acceptance Criteria:**
- [ ] `manifest.json` indexes all 211 root ops + any overrides/islands; carries `kind`, `inputType`, `destructive`, `job-returning`.
- [ ] `catalog.json` resolves nested inputs + `*FilterType` criterion shapes + `AND/OR/NOT`; enumerates every reachable enum's symbols; marks deprecated fields with the verbatim reason.
- [ ] `overlay.yaml` seeds `destructive: [querySQL, execSQL, metadataImport, anonymiseDatabase, migrate]` and the `metadata*`/package/migrate `job-returning` set.

**Verify:** `go test ./internal/genops -run TestManifestCatalog -v`; `jq '.["$defs"].SceneFilterType' schema/catalog.json` resolves; conformance catalog-coverage test (Task 22) green.

### Task 8: genqlient.yaml + generate wiring + determinism

**Goal:** Wire genqlient over the vendored schema + generated/override operations with the §10 scalar bindings; ensure `go generate` runs genops then genqlient in order; ensure the x/tools bump survives.

**Files:** Create `genqlient.yaml`, `generate.go` (`//go:generate` directives).

**Acceptance Criteria:**
- [ ] `genqlient.yaml` binds `Time→time.Time`, `Int64→int64`, `Map`/`Any→encoding/json.RawMessage`, `BoolMap→map[string]bool`, `Timestamp→string`, `Upload→github.com/lightning-rider-999/go-stashapp/stash.Upload`, `PluginConfigMap→map[string]any`.
- [ ] `task generate` runs clean from a fresh checkout (after `task` resolves the toolchain), `go build ./...` green.
- [ ] codegen-freshness: regenerate + `git diff --exit-code` clean.

**Verify:** `task generate && go build ./... && task generate && git diff --exit-code` → all green.

### Task 9: ApiKey redaction for `paths.stream` (B4) + golden test

**Goal:** Keep `paths` in the default scene tier but redact the `apikey` query param wherever pre-signed URLs surface in CLI output; prove no ApiKey JWT ever appears in default-policy output.

**Files:** Create `cmd/stash/redact.go`, `redact_test.go` (golden).

**Acceptance Criteria:**
- [ ] A redactor strips/`REDACTED`s the `apikey` query param from any URL string in CLI output.
- [ ] Golden test: a sample `findScenes` default-policy payload run through the output layer contains no `eyJ`/`apikey=` token.

**Verify:** `go test ./cmd/stash -run TestRedact -v` → green; `task generate` output of a default scene query piped through the CLI contains no key.

---

## Phase 3 — SDK runtime

### Task 10: `stash.Client` — transport, ApiKey, URL normalize, options, env
**Goal:** Construct a Client wrapping genqlient's HTTP client; `ApiKey` via RoundTripper; URL normalize (append `/graphql`; derive `ws(s)://…/graphql`); functional options + env fallback; default bounded `http.Client.Timeout` on the HTTP path only.
**Files:** `stash/client.go`, `stash/options.go`, `client_test.go`.
**Acceptance Criteria:** builds from env or options; ApiKey header set on every request; URL normalized; ws URL derived; secret never logged (redacting `LogValue`).
**Verify:** `go test ./stash -run TestClient -v` (httptest server asserts `ApiKey` header + normalized path).

### Task 11: Typed error model + envelope
**Goal:** `*GraphQLError` (carrying the GraphQL error list), transport/auth categories, `%w` chains, `errors.As`-friendly; the CLI error envelope shape (`code`, `message`, `graphql_errors`, `field`, `retryable`).
**Files:** `stash/errors.go`, `errors_test.go`.
**Verify:** `go test ./stash -run TestErrors -v` → GraphQL errors surface as `*GraphQLError`; categories distinguishable via `errors.As`.

### Task 12: Version handshake + compat
**Goal:** `Client.Version(ctx)` via `version{version hash build_time}`; compat check vs `stash.SchemaVersion` (warn; CLI surfaces a distinct exit code/field).
**Files:** `stash/version.go`, `version_test.go`.
**Verify:** `go test ./stash -run TestVersion -v` (mock returns a version; mismatch flagged). Optional integration: live handshake returns `v0.31.1`.

### Task 13: Bounded fan-out
**Goal:** `errgroup.WithContext` (first error cancels) + `SetLimit` configurable bound; no retries.
**Files:** `stash/batch.go`, `batch_test.go`.
**Verify:** `go test -race ./stash -run TestBatch -v` → bound respected; first error cancels; no retry.

### Task 14: Subscription transport (gorilla WSConn) — write-serialized, keepalive, reconnect
**Goal:** A `graphql.WSConn` adapter over `gorilla/websocket` that injects `ApiKey`, **serializes all writes** (single-writer goroutine — gorilla forbids concurrent writers), owns client-side keepalive (Stash sends none on `graphql-transport-ws`), and bounded reconnect-with-resubscribe.
**Files:** `stash/subscriptions.go`, `subscriptions_test.go`.
**Acceptance Criteria:** all 3 subscriptions stream typed events; concurrent keepalive + protocol writes are serialized (`-race` clean); drop surfaces per the `--wait` contract.
**Verify:** `go test -race ./stash -run TestSubscription` (synctest, fake clock); integration: live `jobsSubscribe` over an idle socket (Task 23).

### Task 15: `importObjects` multipart island (+ __typename rule)
**Goal:** Hand-wired GraphQL-multipart sender for `importObjects(input:{file:Upload!})`, reusing the Client transport/auth; any island selecting interface/union fields includes `__typename` (A5).
**Files:** `stash/importobjects.go`, `importobjects_test.go`.
**Verify:** `go test ./stash -run TestImportObjects` (httptest asserts multipart `operations`/`map`/file parts); integration with a real small file.

---

## Phase 4 — CLI (agent-first)

### Task 16: Generate the cobra command tree from the manifest
**Goal:** Emit `cmd/stash/gen_commands.go` from `manifest.json` under `stash <resource> <verb>`; subscriptions → `stash job watch`/`log tail`/`scan watch`; islands hand-wired.
**Files:** `internal/genops/cli.go` (or a sibling), `cmd/stash/gen_commands.go` (generated), tests.
**Verify:** `task generate && go build ./cmd/stash && ./stash --help` lists resource groups; a command exists for every manifest op (conformance, Task 22).

### Task 17: Output — JSON default, NDJSON, `-o table|yaml`
**Files:** `cmd/stash/output.go`, `output_test.go`. **Verify:** default is JSON regardless of TTY; lists stream NDJSON; `-o table|yaml` work.

### Task 18: Input — JSON-first + raw-JSON partial-update binding + convenience flags
**Goal:** stdin/`--input` JSON; **partial-update mutation inputs bound as raw JSON** (`json.RawMessage`/`map[string]any`) — NOT genqlient's typed structs — to preserve present/absent/`null` (the three-state contract stdlib JSON can't otherwise express); convenience flags are read/list-only (never inject mutation keys).
**Files:** `cmd/stash/input.go`, `input_test.go`. **Verify:** `go test ./cmd/stash -run TestPartialUpdate` golden proves present/absent/null all survive (Task 22 cross-check).

### Task 19: Errors + exit codes — the (name, integer) table
**Goal:** Structured JSON envelope on stderr; the frozen `(name, integer)` taxonomy (`ok, usage, auth, transport, validation, server-fault, not-found, destructive-refused, job-failed, still-running, unconfirmed`) in `docs/AGENTS.md`; envelope `code` = name half, exit status = integer half.
**Files:** `cmd/stash/errors.go`, `errors_test.go`, `docs/AGENTS.md` (table). **Verify:** golden per exit-code path.

### Task 20: `stash catalog` — embedded build-time artifact
**Goal:** `go:embed schema/catalog.json`; `stash catalog` prints it verbatim (no runtime SDL parsing).
**Files:** `cmd/stash/catalog.go`, test. **Verify:** `./stash catalog | jq '.["$defs"], .commands' ` resolves; conformance catalog-coverage green.

### Task 21: `--wait` three-state drop contract + streaming + destructive gating + entrypoint
**Goal:** `--wait` seeds with `findJob` then tracks `jobsSubscribe`; on drop: bounded reconnect → `findJob` reconcile → (terminal→exit; still-running→resume; indeterminate→`unconfirmed`+job id). `--wait-timeout` default unset = no client bound. Streamers emit NDJSON. Destructive ops gated by `--yes-i-understand` → `destructive-refused`. `main.go` entrypoint + env/flag config.
**Files:** `cmd/stash/wait.go`, `stream.go`, `main.go`, tests. **Verify:** `go test -race ./cmd/stash -run TestWait` (synctest); integration: real multi-minute scan over an idle socket (Task 23).

---

## Phase 5 — Conformance & quality

### Task 22: Conformance suite
**Goal:** The full gate set. **Acceptance Criteria (each a sub-test):**
- [ ] Completeness: every SDL root field has a manifest op (red on uncovered).
- [ ] SDL input/enum enumeration: every input object + enum resolves to a generated symbol / catalog entry.
- [ ] Scalar round-trips incl. **`PluginConfigMap` and `Timestamp`** explicitly.
- [ ] Interface/union coverage on **`findFiles` (BaseFile) + `findImages` (VisualFile)** (A4) — NOT Scene.
- [ ] Recursive filter-input round-trip (`SceneFilterType` AND/OR/NOT + `HierarchicalMultiCriterionInput.depth`).
- [ ] Partial-update three-state golden (present/absent/null).
- [ ] **Canonical-type stability WITH the A3 exception allowlist** (anything path-named and not allowlisted fails).
- [ ] Catalog coverage (every command; every referenced enum/input resolves; enum symbols match SDL).
- [ ] Exactly-one-`Upload` field in the SDL.
- [ ] Mutation-set drift gate (mutations diffed vs a committed baseline → forces triage into `overlay.yaml`).
- [ ] **ApiKey redaction** incl. `paths.stream` (B4).
- [ ] Determinism (regenerate twice → byte-identical).
- [ ] **Seeded-instance re-validation (B6):** Gallery/Image/Group/GroupDescription branches validated against an instance with non-empty data before lock.
**Files:** `internal/conformance/*_test.go`. **Verify:** `go test ./internal/conformance/... -v` → all green; `task check` green.

### Task 23: synctest timing tests + two-tier harness
**Goal:** `testing/synctest` for subscription lifecycle, `--wait`, errgroup; hermetic mock-server/golden default tier + opt-in `//go:build integration` tier (env-gated, skipped when absent) covering the 3 subs, the idle-`--wait` keepalive/reconnect/reconcile path, and `importObjects`.
**Files:** `stash/*_synctest_test.go`, `stash/integration_test.go`, `internal/mockgql/`. **Verify:** `go test -race ./...` (hermetic) green; `STASHAPP_URL=… STASHAPP_API_KEY=… go test -tags integration ./...` green against the live instance.

### Task 24: `task check` / `vuln` / golangci-lint / CI
**Goal:** Wire all gates; minimal GitHub Actions (`task check` + `task vuln`).
**Files:** `.golangci.yml`, `.github/workflows/ci.yml`. **Verify:** `task check && task vuln` green.

---

## Phase 6 — Docs

### Task 25: godoc + generated CLI reference + AGENTS.md + README
**Goal:** Doc comments on every exported symbol + runnable `Example`s; `docs/cli/` from cobra; `docs/AGENTS.md` (exit-code table, error envelope, enum-symbol rule, multi-criterion example, `--wait` incl. `unconfirmed` re-attach, partial-update contract, `per_page:-1` footgun, idempotency); README quickstart.
**Files:** `docs/AGENTS.md`, `docs/cli/*`, `README.md`, `doc.go` per package. **Verify:** `go test ./... -run Example` green; `golangci-lint run` (godoc lint) green.

---

## Self-review notes

- **Spec coverage:** every §2–§12 requirement maps to a task (codegen→T3-8; canonical types/flatten→T4 + the A1/A3 amendments; client/wiring→T10-15; CLI→T16-21; catalog→T7,T20; conformance→T22; docs→T25; tooling→T1,T24). The §12 spikes are Phase 0 (done).
- **Ordering/deps:** T1→T2→(T3→T4→T5→T6→T7→T8)→generated client; T9 depends on T17; CLI gen (T16) depends on the manifest (T7); conformance (T22) depends on the generated surface; integration (T23) depends on the runtime + CLI.
- **Open decisions folded from spikes:** ApiKey-in-paths → redact (B4, T9/T22); path-named exceptions → allowlist (A3, T5/T22); Scene uses concrete VideoFile (A4, T22); seeded-instance re-validation (B6, T22).
