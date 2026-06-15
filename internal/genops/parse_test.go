package genops

import (
	"slices"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
)

// schemaDir is the vendored SDL, relative to this package's directory.
const schemaDir = "../../schema"

func TestParseRootFieldCounts(t *testing.T) {
	s, err := LoadSchema(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		op   ast.Operation
		want int
	}{
		{ast.Query, 74},
		{ast.Mutation, 134},
		{ast.Subscription, 3},
	}
	for _, c := range cases {
		got := len(RootFields(s, c.op))
		t.Logf("%s root fields = %d", c.op, got)
		if got != c.want {
			t.Errorf("%s root fields = %d, want %d", c.op, got, c.want)
		}
	}

	// gqlparser injects __schema/__type into Query.Fields; they must not be
	// enumerated as operations (that is why Query is 74, not 76).
	for _, f := range RootFields(s, ast.Query) {
		if f.Name == "__schema" || f.Name == "__type" {
			t.Errorf("introspection field %q leaked into RootFields(Query)", f.Name)
		}
	}
}

func TestParsePerformerEdges(t *testing.T) {
	s, err := LoadSchema(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	perf := s.Types["Performer"]
	if perf == nil {
		t.Fatal("Performer type not found in schema")
	}

	got := fieldNames(Edges(s, perf))
	slices.Sort(got)
	// movies is an object edge but @deprecated → excluded from the clean surface.
	want := []string{"groups", "scenes", "stash_ids", "tags"}
	if !slices.Equal(got, want) {
		t.Errorf("Performer edges = %v, want %v", got, want)
	}

	// B5: studios is not a Performer field — edges come from the AST, not a
	// hand-list, so a lookup returns not-found.
	if f := perf.Fields.ForName("studios"); f != nil {
		t.Error("Performer.studios resolved but should not exist (B5)")
	}
	// The deprecated movies edge must not leak into the clean edge surface.
	if slices.Contains(got, "movies") {
		t.Error("deprecated Performer.movies leaked into Edges")
	}
}

func TestParseScalarsExcludeDeprecated(t *testing.T) {
	s, err := LoadSchema(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	perf := s.Types["Performer"]
	scalars := fieldNames(Scalars(s, perf))
	// Sanity: current scalar fields present, deprecated ones absent.
	for _, want := range []string{"name", "favorite", "custom_fields", "rating100"} {
		if !slices.Contains(scalars, want) {
			t.Errorf("Scalars(Performer) missing %q", want)
		}
	}
	for _, dep := range []string{"url", "twitter", "instagram", "career_length", "movie_count"} {
		if slices.Contains(scalars, dep) {
			t.Errorf("Scalars(Performer) included deprecated %q", dep)
		}
	}
}

func fieldNames(fs []*ast.FieldDefinition) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Name
	}
	return out
}
