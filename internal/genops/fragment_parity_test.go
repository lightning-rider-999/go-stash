package genops

import (
	"strings"
	"testing"

	"github.com/lightning-rider-999/go-stashapp/schema"
)

// TestFragmentParity guards against the class of bug where a canonical fragment's
// shape depends on which operation first triggered its construction.
//
// Compile builds fragments lazily while rendering operations; BuildFragments
// builds the whole type universe in sorted order, independent of any operation.
// A canonical <T>Fields must be identical either way. When operation-render path
// state leaks into a lazily-built fragment, the two diverge and a valid edge can
// be silently dropped (the historical PluginFields.tasks.plugin truncation). For
// every fragment the shipped (Compile) surface emits, its body must match the
// canonical universe byte-for-byte.
//
// This is an internal consistency invariant between two of genops's own code
// paths, so it exercises BuildFragments and Compile directly rather than going
// through the conformance fixture. The schema version is immaterial to fragment
// shape; schema.SchemaVersion keeps the call honest without coupling the test to
// a literal.
func TestFragmentParity(t *testing.T) {
	s, err := LoadSchema(schemaDir)
	if err != nil {
		t.Fatalf("loading schema: %v", err)
	}
	universe := BuildFragments(s)

	compiled, err := Compile(schemaDir, "../../operations/overlay.yaml", schema.SchemaVersion)
	if err != nil {
		t.Fatalf("compiling surface: %v", err)
	}

	for _, name := range universe.Names() {
		body, ok := universe.Fragment(name)
		if !ok {
			continue
		}
		// Only fragments the operation surface actually spreads appear in the
		// shipped output; skip the rest of the universe.
		if !strings.Contains(compiled.Fragments, "fragment "+name+" on ") {
			continue
		}
		if !strings.Contains(compiled.Fragments, body) {
			t.Errorf("fragment %s differs between the shipped Compile surface and the canonical BuildFragments universe; "+
				"an operation-render path-state leak may have truncated it\n--- canonical ---\n%s", name, body)
		}
	}
}
