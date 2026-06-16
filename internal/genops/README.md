# genops — design rationale

This document records *why* `genops` is built the way it is. For *what* each
function does, read the source and its doc comments; this is the layer above
that. It is written for a maintainer who has to change the generator and needs
to know which decisions are load-bearing.

All numbers below are for the vendored SDL at Stash `v0.31.1`
(`schema/version_gen.go`).

---

## 1. What genops is

`genops` is a build-time compiler from Stash's vendored GraphQL SDL to a typed
Go client surface. From one schema load it produces:

- **genqlient operations + fragments** (`operations/generated/operations.graphql`,
  `fragments.graphql`): one operation per root field, plus a deterministic,
  acyclic set of canonical fragments. genqlient then turns these into the typed
  `stash` package (`stash/operations_gen.go`).
- **An operation manifest** (`operations/manifest.json`): a thin per-operation
  index.
- **A machine catalog** (`schema/catalog.json`): the agent-facing description of
  the call surface (input `$defs`, enum symbols, deprecations, exit-code sets).
- **A cobra command table** (`cmd/stash/gen_commands.go`): the CLI tree derived
  from the manifest.

Everything is derived strictly from the AST (`gqlparser/v2`), never from a
hand-maintained list of fields. The pipeline runs from `generate.go`'s two
`//go:generate` directives, genops first then genqlient. genops is not imported
at runtime (`doc.go`).

Because the surface is generated from the vendored SDL, a server upgrade that
drifts a field is a red build (the freshness gate, §3), not a silent nil.

---

## 2. Why it exists — the decision

The problem: get a complete, typed Go client plus an agent-drivable surface over
the Stash GraphQL API, which is large (211 root fields) and deeply cyclic on the
output side (scene ⇄ performer ⇄ studio ⇄ tag, file/folder trees). Three options
were weighed.

**Option A — hand-write the operations (the genqlient / gqlgenc path).**
genqlient and gqlgenc both generate Go types *from hand-written `.graphql`
documents*; neither has a schema → all-operations mode. This is the path Stash
itself uses for its own stash-box client, which has ~12 hand-written operations.
At 211 operations it is a perpetual human burden, and worse, schema drift in any
*unselected* field is invisible: a hand-written document keeps compiling against
a changed schema until a field you happen to select disappears. Drift becomes a
runtime break, not a red build. gqlgenc would *replace* genqlient as the
type-from-documents generator; it would not replace genops, because something
still has to write the 211 documents.

**Option B — an off-the-shelf schema → operations generator.**
Tools exist that auto-emit one operation per root field
(`timqian/gql-generator`, `GavinRay97/graphql-operation-generator`,
`@mvr-studio`'s codegen plugin). They all handle cyclic *output* types with a
flat global depth cap that **truncates**: `gql-generator` returns `''` for a
re-encountered type, which yields a dropped edge or an empty (illegal) selection
set; the others default to depth 1. They emit `.graphql`, not Go, and stamp no
flatten directives, so the result is not canonical typed Go. None of them emits
a `{id, title}` Ref to keep a cyclic edge alive while staying finite.

**Option C — an off-the-shelf complete Go client.**
None exists for Stash or stash-box. The one importable third-party client is
hand-written, read-mostly, and types its filters as untyped `map[string]any`,
which throws away exactly the typed-input safety we want.

**Conclusion.** No off-the-shelf tool does *"schema → cycle-safe,
ref-canonical, typed-Go operations + a static catalog + CLI."* The closest
(Option B) gets the enumeration right and the cycle handling wrong. genops
exists to fill that gap: it is Option B done with a semantic selection policy
instead of a depth cap, emitting canonical-Go-shaped documents, and reusing the
same AST pass to build the manifest, catalog, and CLI.

---

## 3. Design decisions

### 3.1 Generate from the vendored SDL — drift is a red build

The SDL is vendored under `schema/` and stamped with the release tag it came
from (`schema/version_gen.go`: `SchemaVersion = "v0.31.1"`). That version threads
through `genops.Compile(schemaDir, overlayPath, schema.SchemaVersion)` into the
manifest and catalog. The `schema` Taskfile task refreshes the SDL at the pinned
tag and re-stamps.

The freshness gate is in `Taskfile.yml`'s `check` task: it runs `task generate`
and then `git diff --exit-code` over the generated artifacts
(`operations/generated`, `operations/manifest.json`, `schema/catalog.json`,
`schema/version_gen.go`, `stash/operations_gen.go`, `cmd/stash/gen_commands.go`).
If regeneration would change any committed artifact, the build fails. So a schema
upgrade that alters a field cannot land silently — it shows up as a generated
diff that a human has to acknowledge.

### 3.2 Semantic selection policy, not a depth limit

This is the core of why genops beats Option B. The selection policy is by type
*role*, not by depth:

- **Ref-able entities** (`IsRefable`, `fragments.go`: object + `id: ID!` +
  a `name` or `title` display field) expand to a `{id, name|title}` Ref at every
  nested edge.
- **Cyclic entity edges stay ref-only.** Inside a fragment, an object-typed edge
  to a ref-able entity terminates at `<T>Ref`, never the full `<T>Fields`
  (`writeObjectEdge`, the `IsRefable(t)` branch, with `full == false`). This
  breaks every cycle through a ref-able type at depth one.
- **Value types expand fully** until the DFS revisits a type already on the
  build path, at which point the edge terminates scalars-only
  (`writeObjectEdge`, the `fs.building[t.Name] || fs.onPath[t.Name]` branch →
  `writeScalarsOnly`). This is how the file/folder cycle (Folder ⇄ BasicFile)
  terminates without a hand-maintained stop-list (`cycle.go`).
- **The operation layer spreads the full `<E>Fields` only at the payload root.**
  When a root field returns a ref-able entity, `renderRootField`
  (`operations.go`, the `IsRefable(def)` branch) flattens the single spread of
  `<E>Fields`. `writeSelection`'s `full` flag is true *only* there, so the full
  expansion happens once, at the top of each operation, and never recursively.

**Why not a depth cap.** A flat cap is wrong at both ends. Low caps truncate real
data the caller asked for (a depth-1 cap on `findScenes` drops the studio name).
High caps blow up payload size on the cyclic graph: expanding one cyclic entity
edge a single level instead of to a Ref is roughly an order of magnitude more
payload (a full `PerformerFields` versus `{id, name}`), and that multiplies along
every edge. The semantic policy yields a finite, complete, and useful surface:
every scalar the entity owns, plus a navigable Ref for every relationship.

### 3.3 The cyclic crux is the OUTPUT graph, not the input filters

Worth stating plainly so a future reader does not chase the wrong recursion.
Stash's filter inputs are themselves self-referential — `SceneFilterType` has
`AND`/`OR`/`NOT` of its own type. That looks like a recursion problem and is not.

Self-referential *input* types become typed GraphQL **`$variables`**, never
selection sets. The generated operation reads:

```
query FindScenes($scene_filter: SceneFilterType, $ids: [ID!], $filter: FindFilterType) { ... }
```

`SceneFilterType` is carried as a variable type (`operations.go`'s
`renderOperation` emits `$<arg>: <Type>` verbatim from the SDL); genqlient
renders it as a Go input struct, made finite by `use_struct_references: true`
(§3.4). Input recursion is therefore a non-issue for genops, for genqlient, and
for any operation generator. The only recursion genops has to terminate is the
**fragment / output graph** (§3.2), which is what `cycle.go` and the
build-path tracking address.

### 3.4 flatten directives → canonical types

genops emits `# @genqlient(flatten: true)` on the line directly above each
single-fragment-spread field (`flattenDirective` in `fragments.go`; `writeSpread`
places it above the field, never above an operation). With flatten, genqlient
binds the field's Go type *directly to the spread fragment's canonical type*
instead of generating a path-named wrapper struct. Combined with the
`genqlient.yaml` settings —

- `use_struct_references: true` (struct-typed fields become pointers; required so
  self-referential filter inputs are not infinitely sized, and so nullable entity
  edges are pointer-canonical),
- `optional: pointer` (nullable fields become `*T` with omitempty),

— a nested entity edge resolves to a single shared type. The result in
`stash/operations_gen.go`:

```go
type SceneFields struct {
	...
	Studio *StudioRef `json:"studio"`
	...
}
```

`SceneFields.Studio` is `*StudioRef`, the same `StudioRef` every other operation
references, not a `FindScenesSceneStudio`-style path-named struct.

**Why canonical types matter.** They are stable (an operation's result type does
not change name when an unrelated sibling field is added), deduplicated (one
`StudioRef`, not one per call site), and reusable across operations and across
hand-written code that consumes the client.

### 3.5 The path-named exception allowlist

A small set of shapes legitimately *cannot* collapse to a single fragment
spread, so genops emits them as path-named inline selections. These are tracked
and checked against an explicit allowlist in `exceptions.go`
(`pathNamedAllowlist`). The full set as vendored:

| Type | Why path-named |
|------|----------------|
| `SceneGroup` | junction `{group: Group!, scene_index}` — Scene.groups |
| `SceneMovie` | junction `{movie: Movie!, scene_index}` — Scene.movies (deprecated) |
| `GroupDescription` | junction `{group: Group!, description}` — Group.containing_groups / sub_groups |
| `SceneMarkerTag` | junction `{tag: Tag!, scene_markers: [SceneMarker!]!}` — Query.sceneMarkerTags |
| `PluginHook` | junction `{plugin: Plugin!, ...}` — plugin hooks |
| `PluginTask` | junction `{plugin: Plugin!, ...}` — plugin tasks |
| `VisualFile` | union `VideoFile \| ImageFile` — Image.visual_files |
| `BaseFile` | interface (BasicFile/VideoFile/ImageFile/GalleryFile) — findFile / findFiles / Folder.zip_file |
| `Folder` | self-referential folder tree — terminated scalars-only |
| `BasicFile` | file metadata cycling via parent_folder / zip_file — terminated scalars-only |

Three categories: mixed-selection junction wrappers (no `id`, so not ref-able,
yet carrying an object edge, so not a flat value type), union/interface fields
(decoded by a `__typename`-keyed `UnmarshalJSON`, so they cannot be a single
spread), and the terminated value-type cycles in the file/folder graph.

**Why an explicit allowlist.** Anything path-named and *not* on this list is
generation drift. `TestCycleAllowlistComplete` (`cycle_test.go`) calls
`UnlistedPathNamed(fs)` and fails if it is non-empty. So a schema change that
introduces a new un-collapsible shape (a new union, a new junction object) is a
red test, which forces a human to either fix the generator or add the type to the
allowlist *with a stated reason*. The map values are those reasons. The list is a
superset of what any single artifact emits: operation selections surface
`SceneMarkerTag` and `BaseFile` that the fragment set alone does not.

### 3.6 Fragments are built context-free

A canonical fragment's *shape* must not depend on which operation first triggered
its construction. If it did, the same `<T>Fields` could be emitted two different
ways depending on walk order, and the output would not be deterministic.

`Compile` enforces this structurally: it calls `BuildFragments(s)` (which
materialises the whole fragment universe in **sorted type order**) *before*
`BuildOperations(s, fs)` (`compile.go`). Operations then spread the pre-built
fragments; only the fragments actually reached are emitted, since genqlient
rejects unused fragments (`reachableFragments` in `cycle.go`).

The build is also context-free *within* a single pass. `ensureFields`
(`fragments.go`) saves and clears the `onPath` render-path set before building a
fragment body, and restores it after. A review caught a real bug here: without
the clear, an operation's result-wrapper flag (or a mixed wrapper being inlined
above) leaks into a lazily built `<T>Fields` and truncates a valid edge — the
documented case is `PluginFields.tasks.plugin`, dropped when `pluginTasks` roots
before `plugins`. With the fix, the generated `PluginFields` keeps the edge:

```
tasks {
  name
  description
  # @genqlient(flatten: true)
  plugin {
    ...PluginRef
  }
}
```

The context-free property is enforced by the sorted-universe build order in
`Compile` plus the `onPath` save/clear in `ensureFields`, and guarded directly by
`TestFragmentParity` (`fragment_parity_test.go`): it asserts
every fragment the shipped `Compile` surface emits is byte-identical to the same
fragment built standalone by `BuildFragments`, so any operation-render path-state
leak that truncated an edge (the `PluginFields.tasks.plugin` regression) turns the
build red. The acyclic invariant is separately guarded by
`TestCycleNoFragmentCycles` (`cycle_test.go`), which asserts `FragmentCycles(fs)`
is empty over the whole universe.

### 3.7 Manifest, catalog, and CLI from the same AST pass

The manifest (`manifest.go`), catalog (`catalog.go`), and command table
(`cli.go`) are all built from the same loaded schema inside one `Compile` run.
They cannot drift from each other or from the operations, because there is no
second source. The CLI table even references the genqlient operation const
(`stash.<OpName>_Operation`) rather than re-embedding query text, so the query
lives in exactly one place (`EmitCommands`, `cli.go`).

The catalog is the agent-facing surface. Per operation it carries the full
argument list (deprecated args included and flagged, unlike the operation
generator which drops them), the transitive closure of input objects and enums
reachable from those args (`$defs`, via `reach`), enum wire symbols, deprecation
reasons, and a derived exit-code set (`exitCodes`: the frozen base six, plus
`not-found` for a nullable single-entity lookup, plus destructive and
job-returning extensions). The exit-code names are not Stash-specific and are
not defined here: `baseExitCodes` and the conditional names source from the
shared taxonomy in `internal/exitcode`, the single definition the CLI runtime
(`cmd/stash`) also exits with, so the catalog's vocabulary cannot drift from the
process exit codes.

---

## 4. Candidacy for extraction to a standalone library

`genops`'s core is schema-agnostic: it walks any GraphQL SDL, and nothing in the
fragment/ref policy, cycle termination, or operation assembly is Stash-specific.
That makes a standalone module plausible — a second GraphQL CLI (a `go-stashdb`
for stash-box, say) could reuse it. What follows is the honest reusable /
Stash-specific split, read off the code.

### Reusable as-is

| Concern | Code |
|---------|------|
| SDL parse / field enumeration | `parse.go` (`LoadSchema`, `RootFields`, `Edges`, `Scalars`) |
| Fragment + ref policy | `fragments.go` (`IsRefable`, `writeObjectEdge`, `writeAbstractEdge`) |
| Cycle termination | `cycle.go` + the build-path tracking in `fragments.go` |
| Operation assembly | `operations.go` |
| Manifest / catalog builders | `manifest.go`, `catalog.go` |
| CLI path derivation engine | `cli.go` (`derivePath` *structure*, `BuildCommands`, `EmitCommands`) |

None of these hard-codes a Stash type name.

### Stash-flavored — must become inputs before extraction

| Concern | Where | What it hard-codes |
|---------|-------|--------------------|
| Path-named allowlist | `exceptions.go` `pathNamedAllowlist` | Literal Stash type names (`SceneGroup`, `VisualFile`, `Folder`, …). Must become a per-schema config input. |
| Resource/verb derivation | `cli.go` `pluralEntity`, `singularEntity`, `subscriptionPaths`, `derivePath` rules | Stash-ish irregular plurals (`Galleries` → gallery), multi-word nouns, and prefix groups (`metadata*`, `scrape*`, `stashBox*`). Needs to be configurable naming. |
| Scalar bindings | `genqlient.yaml` `bindings` | Already per-schema (lives in genqlient config, not genops). |
| Overlay (destructive / job-returning) | `operations/overlay.yaml` | Already an input file (`overlay.go` `LoadOverlay`), validated against the schema. |

### Proposed extracted shape

A `genops` module taking `(SDL dir, overlay, path-named-allowlist config, naming
config, scalar bindings)` and emitting the artifacts. Each consumer — this repo,
a hypothetical `go-stashdb` — vendors its own SDL plus its own config and depends
on the module.

### Why not extract yet

There is exactly one consumer today. Extracting now means *guessing* the
reusable / specific boundary instead of letting a second real use drive it. The
allowlist and the naming rules are the two places most likely to need a different
shape for a different schema, and we cannot know the right config surface for
them from one example. The module boundary plus versioning overhead is not paid
back until a second tool actually needs the code. Extract when stash-box (or
another GraphQL CLI) forces it, using the split above as the starting plan.
