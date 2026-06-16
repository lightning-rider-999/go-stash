package conformance

import (
	"strings"
	"testing"

	"github.com/lightning-rider-999/go-stashapp/internal/genops"
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
func TestFragmentParity(t *testing.T) {
	f := load(t)
	universe := genops.BuildFragments(f.schema)

	for _, name := range universe.Names() {
		body, ok := universe.Fragment(name)
		if !ok {
			continue
		}
		// Only fragments the operation surface actually spreads appear in the
		// shipped output; skip the rest of the universe.
		if !strings.Contains(f.compiled.Fragments, "fragment "+name+" on ") {
			continue
		}
		if !strings.Contains(f.compiled.Fragments, body) {
			t.Errorf("fragment %s differs between the shipped Compile surface and the canonical BuildFragments universe; "+
				"an operation-render path-state leak may have truncated it\n--- canonical ---\n%s", name, body)
		}
	}
}
