package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/spf13/cobra"
)

// catalogJSON is the build-time operation catalog, embedded so `stash catalog`
// needs no runtime SDL parsing and no filesystem access. The bytes are a
// byte-identical copy of schema/catalog.json that the genops generator writes
// beside the CLI (go:embed cannot reach the repo-root path with `..`); the
// check gate diffs both files against a fresh `task generate` so the copy never
// drifts from the single source of truth.
//
//go:embed catalog.json
var catalogJSON []byte

// newCatalogCommand builds the `stash catalog` command. With no argument it
// prints the embedded catalog JSON verbatim — the schema version, the full
// commands map (one entry per Stash root field), and the $defs type
// dictionary — so an agent can read the whole machine-facing surface in one
// call without a live server. Given an operation name it prints just that
// command's entry, pretty-printed; an unknown name is an error.
func newCatalogCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "catalog [OpName]",
		Short: "Print the embedded machine-facing operation catalog",
		Long: "catalog prints the build-time catalog of every Stash operation: its " +
			"field, kind, arguments, return type, hazard flags, and exit codes, plus " +
			"the $defs type dictionary. With no argument it emits the whole catalog " +
			"verbatim; with an operation name (e.g. `stash catalog FindScenes`) it " +
			"emits just that entry. No server connection is needed.",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				// Verbatim: the embedded bytes plus a trailing newline.
				if _, err := out.Write(catalogJSON); err != nil {
					return err
				}
				if n := len(catalogJSON); n == 0 || catalogJSON[n-1] != '\n' {
					_, err := out.Write([]byte{'\n'})
					return err
				}
				return nil
			}
			return printCatalogEntry(cmd, args[0])
		},
	}
}

// catalogArg is one declared argument of an operation, as recorded in the
// embedded catalog: its variable name, GraphQL type, and whether it is required.
type catalogArg struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

// catalogOperation is the subset of a catalog command entry the CLI reads at
// runtime — the declared arguments, used to gate convenience flags. The rest of
// the entry (kind, returnType, exitCodes, ...) is consumed by the catalog
// command and by the docs, not here.
type catalogOperation struct {
	Field string       `json:"field"`
	Kind  string       `json:"kind"`
	Args  []catalogArg `json:"args"`
}

// parsedCatalog is the decoded embedded catalog, holding both the typed view the
// runtime reads (typed) and the raw per-command JSON the catalog command
// pretty-prints (raw). Both come from a single decode of the same bytes.
type parsedCatalog struct {
	// typed maps an operation name to the subset of fields the CLI reads at
	// runtime (the declared arguments). Consumed by catalogEntry/operationArgNames.
	typed map[string]catalogOperation
	// raw maps an operation name to its verbatim catalog entry, kept so
	// printCatalogEntry can re-indent the original JSON without a second decode.
	raw map[string]json.RawMessage
}

// loadCatalog decodes the embedded catalog exactly once and caches the result.
// catalogJSON is a build-time //go:embed constant, so a parse failure is a
// programming error (a corrupt or mis-generated embed), not a runtime condition
// a caller could recover from — it panics with the cause rather than degrading
// to an empty catalog that would silently report every operation as absent. The
// ~300KB decode runs a single time per process regardless of how many leaves
// query their argument names at startup.
var loadCatalog = sync.OnceValue(func() parsedCatalog {
	var cat struct {
		Commands map[string]json.RawMessage `json:"commands"`
	}
	if err := json.Unmarshal(catalogJSON, &cat); err != nil {
		panic(fmt.Errorf("cmd/stash: embedded catalog.json is not valid JSON: %w", err))
	}
	typed := make(map[string]catalogOperation, len(cat.Commands))
	for name, rawEntry := range cat.Commands {
		var op catalogOperation
		if err := json.Unmarshal(rawEntry, &op); err != nil {
			panic(fmt.Errorf("cmd/stash: embedded catalog.json entry %q is malformed: %w", name, err))
		}
		typed[name] = op
	}
	return parsedCatalog{typed: typed, raw: cat.Commands}
})

// catalogEntry returns the operation entry for opName from the cached catalog,
// or ok=false when the operation is absent. The catalog is decoded once by
// loadCatalog (a corrupt embed panics there); this is a map lookup.
func catalogEntry(opName string) (catalogOperation, bool) {
	entry, ok := loadCatalog().typed[opName]
	return entry, ok
}

// printCatalogEntry pretty-prints the single catalog entry for opName, or
// errors if no such operation exists in the embedded catalog. It re-indents the
// verbatim raw entry from the cached catalog, so no per-call decode runs.
func printCatalogEntry(cmd *cobra.Command, opName string) error {
	entry, ok := loadCatalog().raw[opName]
	if !ok {
		return fmt.Errorf("no operation %q in the catalog", opName)
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, entry, "", "  "); err != nil {
		return fmt.Errorf("formatting catalog entry: %w", err)
	}
	buf.WriteByte('\n')
	_, err := cmd.OutOrStdout().Write(buf.Bytes())
	return err
}
