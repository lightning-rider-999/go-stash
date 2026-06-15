// Command stash is the agent-first command-line client for a self-hosted Stash
// instance. Every Stash GraphQL root field is exposed as a resource-and-verb
// command (stash scene list, stash metadata scan); the command table is
// generated from the vendored schema (see gen_commands.go) and executed as raw
// GraphQL through the stash SDK transport. Output is machine-readable JSON by
// default.
package main

import (
	"context"
	"os"
)

// main runs the root command and, on failure, emits the structured error
// envelope to stderr and exits with the taxonomy's integer for the classified
// error. See classifyExit and the exit-code table in docs/AGENTS.md.
func main() {
	os.Exit(run(context.Background()))
}

// run executes the root command and returns the process exit status. It is split
// from main so a test can drive the whole error path without exiting. On success
// it returns ExitOK.Code (0); on failure it classifies the error, writes the
// JSON envelope to stderr, and returns the matching integer.
func run(ctx context.Context) int {
	root := buildRootCommand()
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK.Code
	}

	code := classifyExit(err)
	writeErrorEnvelope(os.Stderr, code, err)
	return code.Code
}
