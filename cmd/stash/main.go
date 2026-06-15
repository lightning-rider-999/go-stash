// Command stash is the agent-first command-line client for a self-hosted Stash
// instance. Every Stash GraphQL root field is exposed as a resource-and-verb
// command (stash scene list, stash metadata scan); the command table is
// generated from the vendored schema (see gen_commands.go) and executed as raw
// GraphQL through the stash SDK transport. Output is machine-readable JSON by
// default.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := buildRootCommand()
	if err := root.ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "stash:", err)
		// TODO(Task 19): map errors to the frozen exit-code taxonomy. For now a
		// coarse nonzero exit signals any failure.
		os.Exit(1)
	}
}

// readVariables resolves the operation's GraphQL variables from the --input
// flag: a file path, or "-" for stdin. The payload must be a JSON object whose
// keys are variable names; each value is kept as raw JSON so it round-trips
// verbatim (the three-state present/absent/null distinction Task 18 relies on).
// An empty --input yields empty variables, which suits operations with no input
// and no required arguments.
//
// TODO(Task 18): bind typed input from flags/positional args, not just raw JSON.
func readVariables(cmd *cobra.Command) (map[string]json.RawMessage, error) {
	input, _ := cmd.Flags().GetString("input")
	if input == "" {
		return map[string]json.RawMessage{}, nil
	}

	var (
		data []byte
		err  error
	)
	if input == "-" {
		data, err = io.ReadAll(cmd.InOrStdin())
	} else {
		data, err = os.ReadFile(input)
	}
	if err != nil {
		return nil, fmt.Errorf("reading --input: %w", err)
	}
	if len(data) == 0 {
		return map[string]json.RawMessage{}, nil
	}

	var vars map[string]json.RawMessage
	if err := json.Unmarshal(data, &vars); err != nil {
		return nil, fmt.Errorf("--input must be a JSON object of variables: %w", err)
	}
	if vars == nil {
		return map[string]json.RawMessage{}, nil
	}
	return vars, nil
}
