package conformance

import (
	"encoding/json"
	"testing"

	"github.com/lightning-rider-999/go-stashapp/stash"
)

// ptr returns a pointer to v. With optional: pointer in genqlient.yaml, every
// GraphQL-nullable input field (here SceneFilterType.Organized and
// HierarchicalMultiCriterionInput.Depth) is a Go pointer, so a literal is taken
// by address to set one.
func ptr[T any](v T) *T { return &v }

// TestRecursiveFilterRoundTrip is gate 5: the self-referential filter input must
// survive a JSON round-trip with its nesting and depth intact. SceneFilterType
// carries AND/OR/NOT pointers to itself; HierarchicalMultiCriterionInput carries
// a Depth. If genqlient were regenerated without use_struct_references (so the
// self-referential fields stopped being pointers) the type would not compile;
// this gate guards the runtime behaviour of the pointers that do exist.
func TestRecursiveFilterRoundTrip(t *testing.T) {
	in := &stash.SceneFilterType{
		Title: &stash.StringCriterionInput{
			Value:    "outer",
			Modifier: stash.CriterionModifierEquals,
		},
		AND: &stash.SceneFilterType{
			Organized: ptr(true),
			Tags: &stash.HierarchicalMultiCriterionInput{
				Value:    []string{"7", "9"},
				Modifier: stash.CriterionModifierIncludes,
				Depth:    ptr(3),
			},
		},
		OR: &stash.SceneFilterType{
			NOT: &stash.SceneFilterType{
				Title: &stash.StringCriterionInput{
					Value:    "innermost",
					Modifier: stash.CriterionModifierNotEquals,
				},
			},
		},
	}

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal SceneFilterType: %v", err)
	}

	var out stash.SceneFilterType
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal SceneFilterType: %v", err)
	}

	// Top-level criterion survives.
	if out.Title == nil || out.Title.Value != "outer" {
		t.Fatalf("top-level Title lost in round-trip: %+v", out.Title)
	}

	// AND branch and its hierarchical criterion (with Depth) survive.
	if out.AND == nil {
		t.Fatal("AND branch lost in round-trip")
	}
	if out.AND.Organized == nil {
		t.Fatal("AND.Organized lost in round-trip (nil after unmarshal)")
	}
	if !*out.AND.Organized {
		t.Error("AND.Organized lost in round-trip")
	}
	if out.AND.Tags == nil {
		t.Fatal("AND.Tags (HierarchicalMultiCriterionInput) lost in round-trip")
	}
	if out.AND.Tags.Depth == nil {
		t.Fatal("AND.Tags.Depth lost in round-trip (nil after unmarshal)")
	}
	if *out.AND.Tags.Depth != 3 {
		t.Errorf("AND.Tags.Depth = %d, want 3 (hierarchical depth did not survive)", *out.AND.Tags.Depth)
	}
	if got, want := out.AND.Tags.Value, []string{"7", "9"}; !equalStrings(got, want) {
		t.Errorf("AND.Tags.Value = %v, want %v", got, want)
	}

	// OR -> NOT nesting survives to the innermost leaf.
	if out.OR == nil || out.OR.NOT == nil {
		t.Fatalf("OR/NOT nesting lost in round-trip: OR=%+v", out.OR)
	}
	if out.OR.NOT.Title == nil || out.OR.NOT.Title.Value != "innermost" {
		t.Errorf("innermost NOT.Title lost in round-trip: %+v", out.OR.NOT.Title)
	}
	if out.OR.NOT.Title != nil && out.OR.NOT.Title.Modifier != stash.CriterionModifierNotEquals {
		t.Errorf("innermost NOT.Title.Modifier = %q, want NOT_EQUALS", out.OR.NOT.Title.Modifier)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
