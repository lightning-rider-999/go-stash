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
	"os/signal"
	"syscall"
)

// Build information, injected at release time via -ldflags -X. GoReleaser's
// default ldflags set main.version, main.commit, and main.date, so these names
// are part of the release contract (see .goreleaser.yaml). The defaults below
// are what a plain `go build` (or `go install`) produces, so `stash --version`
// always reports something coherent even outside the release pipeline.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// main runs the root command and, on failure, emits the structured error
// envelope to stderr and exits with the taxonomy's integer for the classified
// error. See classifyExit and the exit-code table in docs/AGENTS.md.
//
// The root context is cancelled on the first SIGINT (Ctrl-C) or SIGTERM, so the
// ctx.Done() clean-stop branches in the subscription streamer and the --wait
// tracker fire and report a classified stop instead of the process being killed
// by default disposition. signal.NotifyContext stops trapping after that first
// signal, so a second Ctrl-C still force-kills a wedged command.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx))
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
