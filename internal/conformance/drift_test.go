package conformance

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/lightning-rider-999/go-stashapp/internal/genops"
	"github.com/vektah/gqlparser/v2/ast"
)

// mutationBaselinePath is the committed, sorted list of mutation root-field
// names. Regenerate it deliberately (see the failure message) when the SDL
// genuinely changes.
const mutationBaselinePath = "testdata/mutations.txt"

// TestMutationSetDrift is gate 10: the set of mutation root fields in the
// vendored SDL must match a committed baseline exactly. Mutations are the
// data-modifying surface; a new one needs a destructive/jobReturning triage in
// overlay.yaml, and a removed one breaks a command. Pinning the set forces a
// human to notice the change rather than letting codegen absorb it silently.
func TestMutationSetDrift(t *testing.T) {
	f := load(t)

	var current []string
	for _, fd := range genops.RootFields(f.schema, ast.Mutation) {
		current = append(current, fd.Name)
	}
	slices.Sort(current)

	baseline := readBaseline(t)

	if slices.Equal(current, baseline) {
		return
	}

	added := difference(current, baseline)
	removed := difference(baseline, current)

	var b strings.Builder
	b.WriteString("mutation set drifted from the committed baseline.\n")
	if len(added) > 0 {
		b.WriteString("  ADDED (new mutations):   " + strings.Join(added, ", ") + "\n")
	}
	if len(removed) > 0 {
		b.WriteString("  REMOVED (gone mutations): " + strings.Join(removed, ", ") + "\n")
	}
	b.WriteString("\nTo resolve:\n")
	b.WriteString("  1. Triage each ADDED mutation in operations/overlay.yaml — decide whether it is\n")
	b.WriteString("     destructive and/or jobReturning, and add it to the relevant list.\n")
	b.WriteString("  2. Confirm each REMOVED mutation is intentionally gone (a Stash downgrade or\n")
	b.WriteString("     schema refresh), and prune any overlay entries that named it.\n")
	b.WriteString("  3. Regenerate the baseline so it matches the new SDL:\n")
	b.WriteString("       internal/conformance/testdata/mutations.txt = sorted mutation root-field names,\n")
	b.WriteString("       one per line. (RootFields(schema, ast.Mutation), .Name, sorted.)\n")
	t.Fatal(b.String())
}

// readBaseline reads and parses the committed mutation baseline into a sorted
// slice of field names.
func readBaseline(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(mutationBaselinePath)
	if err != nil {
		t.Fatalf("reading mutation baseline %s: %v", mutationBaselinePath, err)
	}
	var names []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
	}
	slices.Sort(names)
	return names
}

// difference returns the elements of a that are not in b (both assumed sorted).
func difference(a, b []string) []string {
	in := make(map[string]bool, len(b))
	for _, s := range b {
		in[s] = true
	}
	var out []string
	for _, s := range a {
		if !in[s] {
			out = append(out, s)
		}
	}
	return out
}
