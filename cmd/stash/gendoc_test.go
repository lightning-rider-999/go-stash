package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

// docsOutputDir is the committed location of the generated CLI reference,
// relative to this package (cmd/stash).
const docsOutputDir = "../../docs/cli"

// TestGenerateCLIDocs writes the markdown CLI reference for the whole command
// tree to docs/cli/. It is an opt-in generator, not an assertion: it is skipped
// unless GEN_CLI_DOCS=1 is set, so the normal `go test ./...` run does not
// rewrite committed files. Regenerate the reference with:
//
//	GEN_CLI_DOCS=1 go test ./cmd/stash -run TestGenerateCLIDocs
//
// The output is deterministic: DisableAutoGenTag is set on every command so
// cobra omits the "Auto generated ... on <date>" footer, and cobra sorts both
// the command pages and their contents by name.
func TestGenerateCLIDocs(t *testing.T) {
	if os.Getenv("GEN_CLI_DOCS") != "1" {
		t.Skip("set GEN_CLI_DOCS=1 to regenerate docs/cli/")
	}

	root := buildRootCommand()
	disableAutoGenTag(root)

	dir, err := filepath.Abs(docsOutputDir)
	if err != nil {
		t.Fatalf("resolve output dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create output dir: %v", err)
	}

	if err := doc.GenMarkdownTree(root, dir); err != nil {
		t.Fatalf("generate markdown tree: %v", err)
	}
	t.Logf("wrote CLI reference to %s", dir)
}

// disableAutoGenTag turns off cobra's auto-generated date footer on a command
// and all of its descendants, so the generated markdown is reproducible.
func disableAutoGenTag(cmd *cobra.Command) {
	cmd.DisableAutoGenTag = true
	for _, child := range cmd.Commands() {
		disableAutoGenTag(child)
	}
}
