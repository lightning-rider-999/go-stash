package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"

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

// printCatalogEntry pretty-prints the single catalog entry for opName, or
// errors if no such operation exists in the embedded catalog.
func printCatalogEntry(cmd *cobra.Command, opName string) error {
	var cat struct {
		Commands map[string]json.RawMessage `json:"commands"`
	}
	if err := json.Unmarshal(catalogJSON, &cat); err != nil {
		return fmt.Errorf("decoding embedded catalog: %w", err)
	}
	entry, ok := cat.Commands[opName]
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
