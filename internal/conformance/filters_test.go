package conformance

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/lightning-rider-999/go-stash/stash"
	genops "github.com/trackness/graphql-opgen"
	"github.com/vektah/gqlparser/v2/ast"
)

// selfReferentialFilterTypes maps each SDL self-referential filter input name to
// a zero value of its generated Go type. The map is asserted COMPLETE against the
// schema by TestSelfReferentialFiltersAreHandled, so a new self-referential
// filter input on a Stash upgrade fails the test until it is added here and
// confirmed to round-trip — the class is locked, not a single instance.
var selfReferentialFilterTypes = map[string]any{
	"FileFilterType":      stash.FileFilterType{},
	"FolderFilterType":    stash.FolderFilterType{},
	"GalleryFilterType":   stash.GalleryFilterType{},
	"GroupFilterType":     stash.GroupFilterType{},
	"ImageFilterType":     stash.ImageFilterType{},
	"MovieFilterType":     stash.MovieFilterType{},
	"PerformerFilterType": stash.PerformerFilterType{},
	"SceneFilterType":     stash.SceneFilterType{},
	"StudioFilterType":    stash.StudioFilterType{},
	"TagFilterType":       stash.TagFilterType{},
}

// TestSelfReferentialFiltersAreHandled is gate 5's CLASS check: EVERY filter
// input the SDL declares as self-referential (it has an AND/OR/NOT field whose
// type is the input itself) must be in selfReferentialFilterTypes, its generated
// Go struct must make each self-reference a pointer to the same type (so the type
// is finitely sized — the use_struct_references contract), and a nested instance
// must round-trip through JSON with its nesting intact. The deep golden below
// (TestRecursiveFilterRoundTrip) still exercises SceneFilterType field-by-field;
// this proves the property holds for the whole family, not one hand-picked type.
func TestSelfReferentialFiltersAreHandled(t *testing.T) {
	f := load(t)

	// Discover the self-referential filter inputs directly from the SDL, so the
	// map cannot silently fall out of sync with the schema.
	discovered := discoverSelfReferentialInputs(f.schema)
	if len(discovered) == 0 {
		t.Fatal("no self-referential filter inputs discovered in the SDL; the detector is broken")
	}

	var missing []string
	for name := range discovered {
		if _, ok := selfReferentialFilterTypes[name]; !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("self-referential filter input(s) present in the SDL but not handled here: %v\n"+
			"Add each to selfReferentialFilterTypes and confirm it round-trips.", missing)
	}

	for name, zero := range selfReferentialFilterTypes {
		t.Run(name, func(t *testing.T) {
			if !discovered[name] {
				t.Errorf("%s is in selfReferentialFilterTypes but the SDL no longer declares it "+
					"self-referential; prune it", name)
				return
			}
			rt := reflect.TypeOf(zero)
			// Each of AND/OR/NOT must be a pointer to the same struct type.
			for _, fld := range []string{"AND", "OR", "NOT"} {
				sf, ok := rt.FieldByName(fld)
				if !ok {
					t.Errorf("%s has no %s field; the self-reference was dropped", name, fld)
					continue
				}
				if sf.Type.Kind() != reflect.Pointer || sf.Type.Elem() != rt {
					t.Errorf("%s.%s is %s, want *%s (use_struct_references makes the self-reference a pointer)",
						name, fld, sf.Type, name)
				}
			}
			assertFilterNestingRoundTrips(t, name, rt)
		})
	}
}

// assertFilterNestingRoundTrips builds a value of the filter type with a nested
// AND -> NOT chain via reflection, marshals it, unmarshals it back, and confirms
// the two pointer levels survived. It needs no field-specific knowledge, so it
// works for every self-referential filter type uniformly.
func assertFilterNestingRoundTrips(t *testing.T, name string, rt reflect.Type) {
	t.Helper()
	// outer.AND.NOT — a two-deep self-reference chain.
	innermost := reflect.New(rt) // *T
	mid := reflect.New(rt)
	mid.Elem().FieldByName("NOT").Set(innermost)
	outer := reflect.New(rt)
	outer.Elem().FieldByName("AND").Set(mid)

	b, err := json.Marshal(outer.Interface())
	if err != nil {
		t.Fatalf("%s: marshal nested filter: %v", name, err)
	}
	back := reflect.New(rt)
	if err := json.Unmarshal(b, back.Interface()); err != nil {
		t.Fatalf("%s: unmarshal nested filter: %v", name, err)
	}
	andField := back.Elem().FieldByName("AND")
	if andField.IsNil() {
		t.Fatalf("%s: AND branch lost in round-trip", name)
	}
	notField := andField.Elem().FieldByName("NOT")
	if notField.IsNil() {
		t.Errorf("%s: AND.NOT nesting lost in round-trip", name)
	}
}

// discoverSelfReferentialInputs returns the set of INPUT_OBJECT type names that
// have at least one DIRECT (non-list) field whose type is the input itself — the
// AND/OR/NOT filter shape. A direct self-reference is what makes the value-typed
// struct infinitely sized, so it is precisely the case use_struct_references must
// pointer-break and this gate must guard. A LIST self-reference (e.g.
// PluginValueInput.a: [PluginValueInput!]) is naturally finite (a slice) and is
// deliberately excluded — it is not part of the filter family. Reading the set
// from the schema keeps the class definition authoritative, not a hand-kept list.
func discoverSelfReferentialInputs(s *ast.Schema) map[string]bool {
	out := map[string]bool{}
	for name, def := range s.Types {
		if def.Kind != ast.InputObject {
			continue
		}
		for _, fld := range def.Fields {
			// A direct (non-list) self-reference: fld.Type names the input with no
			// list wrapper. ast.Type.Elem is non-nil only for a list type.
			if fld.Type.Elem == nil && genops.BaseTypeName(fld.Type) == name {
				out[name] = true
				break
			}
		}
	}
	return out
}

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
			Organized: new(true),
			Tags: &stash.HierarchicalMultiCriterionInput{
				Value:    []string{"7", "9"},
				Modifier: stash.CriterionModifierIncludes,
				Depth:    new(3),
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
