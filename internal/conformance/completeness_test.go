package conformance

import (
	"testing"

	"github.com/lightning-rider-999/go-stashapp/internal/genops"
	"github.com/vektah/gqlparser/v2/ast"
)

// TestCompleteness is gate 1: every SDL root field across Query, Mutation, and
// Subscription must be covered by exactly one manifest operation. A new root
// field on a Stash upgrade that the overlay/generator does not pick up shows up
// here as an uncovered field; a duplicate shows up as a doubly-covered field.
func TestCompleteness(t *testing.T) {
	f := load(t)

	// Count how many manifest operations claim each schema root field.
	covered := make(map[string]int)
	for _, e := range f.manifest.Operations {
		covered[e.Field]++
	}

	// Collect every root field the SDL actually defines.
	var rootFields []string
	for _, op := range rootOps {
		for _, fd := range genops.RootFields(f.schema, op) {
			rootFields = append(rootFields, fd.Name)
		}
	}

	if len(rootFields) != 211 {
		t.Errorf("SDL root fields = %d, want 211 (schema drift: a root field was added or removed)", len(rootFields))
	}

	var uncovered, doubled []string
	for _, name := range rootFields {
		switch covered[name] {
		case 1:
			// Exactly one manifest operation — the required state.
		case 0:
			uncovered = append(uncovered, name)
		default:
			doubled = append(doubled, name)
		}
	}

	if len(uncovered) > 0 {
		t.Errorf("%d SDL root field(s) have no manifest operation: %v", len(uncovered), uncovered)
	}
	if len(doubled) > 0 {
		t.Errorf("%d SDL root field(s) are covered by more than one manifest operation: %v", len(doubled), doubled)
	}

	// Inverse direction: every manifest operation must name a real root field,
	// so a stale entry referencing a removed field cannot linger.
	rootSet := make(map[string]bool, len(rootFields))
	for _, name := range rootFields {
		rootSet[name] = true
	}
	for _, e := range f.manifest.Operations {
		if !rootSet[e.Field] {
			t.Errorf("manifest operation %q names field %q which is not an SDL root field", e.Name, e.Field)
		}
	}

	if len(f.manifest.Operations) != 211 {
		t.Errorf("manifest operations = %d, want 211", len(f.manifest.Operations))
	}
}

// rootArgTypes returns the named root-operation type definitions (Query type,
// Mutation type, Subscription type) present in the schema.
func rootOperationDef(s *ast.Schema, op ast.Operation) *ast.Definition {
	switch op {
	case ast.Query:
		return s.Query
	case ast.Mutation:
		return s.Mutation
	case ast.Subscription:
		return s.Subscription
	default:
		return nil
	}
}
