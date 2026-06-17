package conformance

import (
	"os"
	"slices"
	"strings"
	"testing"

	genops "github.com/trackness/graphql-opgen"
	"github.com/vektah/gqlparser/v2/ast"
)

// rootFieldBaseline pins one root-operation kind to its committed, sorted set of
// field names. The three baselines together pin the ENTIRE root surface, so a
// schema refresh that adds, removes, or renames any operation — query,
// subscription, or mutation — forces a human to notice rather than letting
// codegen absorb it silently. Mutations were always pinned (they need a
// destructive/jobReturning triage); queries and subscriptions are pinned too so
// the drift gate covers the whole surface, not just the write side.
type rootFieldBaseline struct {
	op   ast.Operation
	path string
	// triage is the operation-specific resolution guidance appended to a drift
	// failure, since the right response differs by kind.
	triage string
}

var rootFieldBaselines = []rootFieldBaseline{
	{
		op:   ast.Query,
		path: "testdata/queries.txt",
		triage: "  1. Confirm each ADDED query is wanted and routes to a sensible command path.\n" +
			"  2. Confirm each REMOVED query is intentionally gone (a Stash change or schema refresh).\n",
	},
	{
		op:   ast.Subscription,
		path: "testdata/subscriptions.txt",
		triage: "  1. A new subscription needs a pinned CLI path in genops' subscriptionPaths map\n" +
			"     and an entry in the generated stream wiring.\n" +
			"  2. Confirm each REMOVED subscription is intentionally gone.\n",
	},
	{
		op:   ast.Mutation,
		path: "testdata/mutations.txt",
		triage: "  1. Triage each ADDED mutation in internal/gen/overlay.yaml — decide whether it is\n" +
			"     destructive and/or jobReturning, and add it to the relevant list.\n" +
			"  2. Confirm each REMOVED mutation is intentionally gone, and prune any overlay\n" +
			"     entries that named it.\n",
	},
}

// TestRootFieldSetDrift is gate 10: the set of root fields in the vendored SDL,
// per operation kind, must match its committed baseline exactly. The root fields
// are the entire command surface; pinning every kind's set makes any addition,
// removal, or rename a deliberate, reviewed change (regenerate the baseline) and
// never a silent codegen absorption.
func TestRootFieldSetDrift(t *testing.T) {
	f := load(t)

	for _, bl := range rootFieldBaselines {
		t.Run(string(bl.op), func(t *testing.T) {
			var current []string
			for _, fd := range genops.RootFields(f.schema, bl.op) {
				current = append(current, fd.Name)
			}
			slices.Sort(current)

			baseline := readBaseline(t, bl.path)

			if slices.Equal(current, baseline) {
				return
			}

			added := difference(current, baseline)
			removed := difference(baseline, current)

			var b strings.Builder
			b.WriteString(string(bl.op) + " set drifted from the committed baseline " + bl.path + ".\n")
			if len(added) > 0 {
				b.WriteString("  ADDED:   " + strings.Join(added, ", ") + "\n")
			}
			if len(removed) > 0 {
				b.WriteString("  REMOVED: " + strings.Join(removed, ", ") + "\n")
			}
			b.WriteString("\nTo resolve:\n")
			b.WriteString(bl.triage)
			b.WriteString("  Then regenerate the baseline so it matches the new SDL:\n")
			b.WriteString("    " + bl.path + " = sorted " + string(bl.op) + " root-field names, one per line.\n")
			t.Fatal(b.String())
		})
	}
}

// readBaseline reads and parses a committed baseline into a sorted slice of field
// names.
func readBaseline(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading baseline %s: %v", path, err)
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
