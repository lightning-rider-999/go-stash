# go-stash — a Go client/library & CLI for Stash's GraphQL API

Stash is a self-hosted organiser for an adult-video collection. This repo builds a reusable Go **client/library** for its GraphQL API, and a **CLI** on top. Read this whole file before touching anything here.

Refer to these as a default, instead of inferring or guessing:
- **Repo**: https://github.com/stashapp/stash
- **Docs**: https://docs.stashapp.cc/

## Behaviour

@../claude-refs/behaviour.md

## The material

@../claude-refs/adult-material.md

## Voice

@../claude-refs/adult-voice.md

## Go

The bar is the most modern Go there is: `go.mod` pins the **latest stable release** (`GOTOOLCHAIN=auto` fetches it regardless of the host install; raise it when a new one lands) and the code is written in that release's current idiom, not folklore — `log/slog` for diagnostics, `slices`/`maps`/`cmp` and range-over-func iterators over hand-rolled loops, error chains with `%w`, generics where they pay their way, `tool` directives in `go.mod` (never a tools.go), `testing/synctest` for timing-dependent tests. Every change passes `go build ./...`, `go vet ./...`, `go test -race ./...`, and `golangci-lint run`; `gofmt` is law; doc comments on every package and anything exported or non-obvious.

## Engineering

- **Reach for the right library; don't hand-roll out of habit.** When a mature, well-fitting tool exists — `genqlient` for typed GraphQL, an established client, a generator off the service's own schema — default to it, and justify *not* using it rather than the reverse. Runtime dependencies have real cost in a long-lived binary, so weigh them honestly — but "fewer deps" is a tradeoff to argue out loud, never an axiom to hide behind.
- **Generate from the source of truth.** Vendor Stash's own GraphQL SDL (from `stashapp/stash`, `graphql/schema/**`, stamped with the version it came from) and generate the typed surface from it with `genqlient`, so a server upgrade that drifts a field is a red build, not a silent nil. Hand-written islands (write-input contracts, recursive shapes) get conformance-tested against the vendored schema.
- **Taskfile-driven gates.** `Taskfile.yml` carries the rituals — run `task --list` for the full set. The umbrella is `check` (gofmt, build, vet, `test -race`, lint, tidy, codegen-freshness); `vuln` runs govulncheck (stdlib CVEs matter for a static binary).

## Service specifics

- **GraphQL** at `<base>/graphql` — `STASHAPP_URL` (the base UI URL) is normalised: posting GraphQL to the base returns the SPA's HTML, so append `/graphql` when the URL has no path.
- Auth: the `STASHAPP_API_KEY` credential is sent in the `ApiKey` header.
- It's the user's own self-hosted instance, but be a well-behaved client anyway: bounded concurrency, no retry that masks a failure.

## Source control

@../claude-refs/adult-github-account.md
