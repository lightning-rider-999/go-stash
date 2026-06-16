# go-stashapp

A Go client library and an agent-first command-line client for
[Stash](https://github.com/stashapp/stash)'s GraphQL API.

The repository ships two things from one typed surface:

- **`stash`** — a reusable SDK (`github.com/lightning-rider-999/go-stashapp/stash`):
  a configured `Client`, generated typed operations for every Stash GraphQL root
  field, a typed error model, bounded-concurrency batching, and reconnecting
  subscriptions.
- **`cmd/stash`** — a CLI that exposes every operation as a
  resource-and-verb command (`stash scene list`, `stash metadata scan`), with
  machine-readable output and a frozen exit-code taxonomy.

The whole typed surface is generated from Stash's own GraphQL SDL, vendored under
`schema/` and stamped with the release it came from (currently **v0.31.1**), so a
server upgrade that drifts a field is a red build rather than a silent nil.

## Install

**With Go** (the recommended path for a Go CLI):

```sh
go install github.com/lightning-rider-999/go-stashapp/cmd/stash@latest
```

**Linux/macOS without Go** — download and install a prebuilt binary. The
installer detects your OS/arch, verifies the release's sha256 checksum, and
installs to a directory on your PATH:

```sh
curl -sSL https://raw.githubusercontent.com/lightning-rider-999/go-stashapp/main/install.sh | sh
```

Override the target directory or pin a version with environment variables:

```sh
# Install into ~/.local/bin instead of the default (/usr/local/bin):
curl -sSL https://raw.githubusercontent.com/lightning-rider-999/go-stashapp/main/install.sh | INSTALL_DIR="$HOME/.local/bin" sh

# Pin a specific release tag instead of the latest:
curl -sSL https://raw.githubusercontent.com/lightning-rider-999/go-stashapp/main/install.sh | VERSION=v1.2.3 sh
```

**Manual** — download the archive for your platform from the
[Releases page](https://github.com/lightning-rider-999/go-stashapp/releases),
verify it against `checksums.txt`, then extract the `stash` binary onto your
PATH:

```sh
tar -xzf stash_<version>_<os>_<arch>.tar.gz
install -m 0755 stash /usr/local/bin/stash
```

**From a checkout:**

```sh
go build -o bin/stash ./cmd/stash
```

**As a library** in another project:

```sh
go get github.com/lightning-rider-999/go-stashapp/stash
```

## Configure

The CLI and the SDK both read two environment variables:

| Variable           | Purpose                                                              |
|--------------------|---------------------------------------------------------------------|
| `STASHAPP_URL`     | Base UI URL of the Stash instance. GraphQL is served at `<url>/graphql`; the URL is normalised, so the base URL is enough. |
| `STASHAPP_API_KEY` | API key, sent in the `ApiKey` header (and the subscription `connection_init` payload). Optional for an unauthenticated instance. |

```sh
export STASHAPP_URL="http://stash.local:9999"
export STASHAPP_API_KEY="your-api-key"
```

The CLI's `--url` and `--api-key` flags override the variables per invocation.

## CLI quickstart

```sh
# List the first page of scenes as newline-delimited JSON.
stash scene list -o ndjson

# Fetch one scene by id.
stash scene get --id 42

# Inspect an operation's inputs, enums, and exit codes without a server.
stash catalog FindScenes
```

Output defaults to JSON; `-o ndjson|table|yaml` selects another format.
Operation variables come from `--input` (a JSON file path, or `-` for stdin) and
are forwarded as raw JSON, which preserves the present / absent / null
distinction that partial-update mutations depend on. Job-returning mutations take
`--wait` to block on the job's outcome; destructive operations require
`--yes-i-understand`. On any failure the CLI writes a single-line JSON error
envelope to stderr and exits with the matching taxonomy integer.

The full machine-facing contract — exit codes, the error envelope, the input
model, enum symbols, multi-criterion filters, the `--wait` re-attach flow, and
the partial-update three-state rule — is in
[`docs/AGENTS.md`](docs/AGENTS.md). The generated per-command reference is in
[`docs/cli/`](docs/cli/), starting at [`docs/cli/stash.md`](docs/cli/stash.md).

## Library quickstart

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/lightning-rider-999/go-stashapp/stash"
)

// ptr returns a pointer to v, for the generated input types whose optional
// fields are pointers (Q *string, Per_page *int, …).
func ptr[T any](v T) *T { return &v }

func main() {
	// URL and API key fall back to STASHAPP_URL / STASHAPP_API_KEY.
	c, err := stash.NewClient(stash.WithURL("http://stash.local:9999"))
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	// The version handshake.
	info, err := c.Version(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Stash %s (%s)\n", info.Version, info.Hash)

	// A generated operation. Pass c.GraphQL() as the client.
	resp, err := stash.FindScenes(ctx, c.GraphQL(), nil, nil, &stash.FindFilterType{
		Q:        ptr("sunset"),
		Per_page: ptr(25),
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, scene := range resp.FindScenes.Scenes {
		fmt.Println(scene.Id, scene.Title)
	}
}
```

The SDK also offers `stash.Batch` / `stash.BatchResults` for bounded-concurrency
fan-out, `stash.Subscribe` for reconnecting typed subscriptions, and a typed
error model (`*stash.GraphQLError`, `*stash.TransportError`,
`stash.ErrUnauthorized`). See the runnable examples in `stash/example_test.go`
and the package documentation (`go doc github.com/lightning-rider-999/go-stashapp/stash`).

## How generation works

The typed surface is built by two steps, run in order by `go generate`:

1. **`internal/genops`** reads the vendored SDL under `schema/` and emits the
   genqlient operations and fragments, the operation manifest, the CLI command
   table (`cmd/stash/gen_commands.go`), and the machine-facing catalog.
2. **genqlient** turns those operations and fragments into the typed Go client
   (`stash/operations_gen.go`).

Regenerate everything:

```sh
task generate    # runs `go generate ./...`
```

After a Stash upgrade, refresh the vendored SDL to a new pinned release and
re-stamp the version, then regenerate:

```sh
task schema      # refresh schema/ at the tag in schema/version.txt
task generate
```

Hand-written islands (the write-input contracts and recursive shapes) are
conformance-tested against the vendored schema under `internal/conformance`, so
drift between the hand-written and generated surfaces turns a test red.

### Regenerating the CLI reference

`docs/cli/` is produced from the live cobra command tree, separately from the
typed-surface generation above so it never interferes with the codegen-freshness
gate. Regenerate it with:

```sh
GEN_CLI_DOCS=1 go test ./cmd/stash -run TestGenerateCLIDocs
```

The output is deterministic (the date footer is disabled), so a clean checkout
regenerates byte-for-byte.

## Quality gates

Every change is expected to pass the gates in the `Taskfile.yml`:

```sh
task check    # gofmt, build, vet, test -race, lint, tidy, codegen freshness
task vuln     # govulncheck (stdlib + dependency CVEs)
```

`task check`'s codegen-freshness step regenerates the typed surface and fails if
any committed generated artifact changed, so the vendored schema and the typed
client can never silently drift apart.

## License

[MIT](LICENSE).
