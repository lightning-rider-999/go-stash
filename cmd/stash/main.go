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
	"runtime/debug"
	"syscall"
)

// Build information, injected at release time via -ldflags -X. GoReleaser's
// default ldflags set main.version, main.commit, and main.date, so these names
// are part of the release contract (see .goreleaser.yaml). The defaults below
// are what a plain `go build` produces; for a `go install <module>@<version>`
// build, which carries no ldflags, resolveBuildInfo fills them in from the
// embedded module build info so `stash --version` still reports something
// coherent outside the release pipeline.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// resolveBuildInfo upgrades the placeholder build vars from Go's embedded module
// build info when no release ldflags were applied. It returns the values to use,
// leaving release builds (where version is already set by -ldflags) untouched.
//
// The trigger is version == "dev", the plain-build default: a `go install
// module@v1.2.3` build embeds that version as info.Main.Version and stamps the
// VCS revision and time into the build settings (vcs.revision / vcs.time), so a
// binary that would otherwise self-report "dev (commit none, built unknown)" can
// instead report its real module version and commit. Any field the build info
// does not provide keeps its incoming placeholder.
func resolveBuildInfo(version, commit, date string, info *debug.BuildInfo, ok bool) (string, string, string) {
	// ldflags already set a real version: GoReleaser builds win, untouched.
	if version != "dev" || !ok || info == nil {
		return version, commit, date
	}

	// Main.Version is "(devel)" for an uncommitted local build and a real tag
	// (e.g. v1.2.3) for `go install module@version`. Only a real version is an
	// improvement over "dev".
	if v := info.Main.Version; v != "" && v != "(devel)" {
		version = v
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if s.Value != "" {
				commit = s.Value
			}
		case "vcs.time":
			if s.Value != "" {
				date = s.Value
			}
		}
	}
	return version, commit, date
}

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
	// stop() must run before os.Exit, which does not honour deferred calls; a bare
	// `defer stop()` alongside os.Exit(run(ctx)) would leak the signal handler
	// (gocritic exitAfterDefer). Sequence it explicitly instead.
	code := run(ctx)
	stop()
	os.Exit(code)
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
