package conformance

import (
	"os"
	"sort"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// Paths to the generated GraphQL sources, relative to this package.
const (
	generatedOpsPath  = "../../internal/gen/generated/operations.graphql"
	generatedFragPath = "../../internal/gen/generated/fragments.graphql"
)

// TestAbstractTypeCoverage is gate 4 (A4): the generated operations for the
// abstract-typed fields carry the discrimination machinery, and the
// concrete-typed field does not.
//
//   - findFiles selects BaseFile (an interface): the FindFiles operation body
//     must carry __typename and a per-member inline fragment for each concrete
//     BaseFile implementor.
//   - findImages selects Image.visual_files (the VisualFile union): the
//     transitive selection (operation -> ImageFields fragment) must carry
//     __typename and a per-member inline fragment.
//   - findScenes selects Scene with a concrete VideoFile file type: the
//     transitive selection must carry NO inline fragments and must not name
//     BaseFile or VisualFile — Scene's files are the concrete VideoFileFields.
//
// This gate REPARSES the generated GraphQL with gqlparser and walks the real AST
// (*ast.InlineFragment / *ast.FragmentSpread). A previous version scraped the
// source text with regexes, which could be fooled by a "... on Foo" inside a
// comment or string literal, or miss a spread split across lines. Walking the AST
// removes those failure modes: an inline fragment is an *ast.InlineFragment node
// with a TypeCondition, full stop, and __typename is a real Field named so.
func TestAbstractTypeCoverage(t *testing.T) {
	doc := parseGenerated(t)

	ops := make(map[string]*ast.OperationDefinition, len(doc.Operations))
	for _, op := range doc.Operations {
		ops[op.Name] = op
	}
	frags := make(map[string]*ast.FragmentDefinition, len(doc.Fragments))
	for _, fd := range doc.Fragments {
		frags[fd.Name] = fd
	}

	t.Run("findFiles_carries_BaseFile_machinery", func(t *testing.T) {
		op, ok := ops["FindFiles"]
		if !ok {
			t.Fatal("FindFiles operation not found in generated operations.graphql")
		}
		w := walkSelection(op.SelectionSet, frags)
		for _, want := range []string{"BasicFile", "GalleryFile", "ImageFile", "VideoFile"} {
			if !w.inlineMembers[want] {
				t.Errorf("FindFiles is missing an inline fragment for BaseFile member %q (got %v)", want, sortedKeys(w.inlineMembers))
			}
		}
		if !w.hasTypename {
			t.Error("FindFiles is missing __typename for BaseFile discrimination")
		}
	})

	t.Run("findImages_carries_VisualFile_machinery", func(t *testing.T) {
		op, ok := ops["FindImages"]
		if !ok {
			t.Fatal("FindImages operation not found in generated operations.graphql")
		}
		w := walkSelection(op.SelectionSet, frags)
		for _, want := range []string{"ImageFile", "VideoFile"} {
			if !w.inlineMembers[want] {
				t.Errorf("FindImages selection is missing an inline fragment for VisualFile member %q (got %v)", want, sortedKeys(w.inlineMembers))
			}
		}
		if !w.hasTypename {
			t.Error("FindImages selection is missing __typename for VisualFile discrimination")
		}
	})

	t.Run("findScenes_has_no_abstract_machinery", func(t *testing.T) {
		op, ok := ops["FindScenes"]
		if !ok {
			t.Fatal("FindScenes operation not found in generated operations.graphql")
		}
		w := walkSelection(op.SelectionSet, frags)
		if len(w.inlineMembers) != 0 {
			t.Errorf("FindScenes selection carries inline fragments %v; Scene uses the concrete VideoFile (VideoFileFields), so there must be none", sortedKeys(w.inlineMembers))
		}
		// The Image-only VisualFile union and the BaseFile interface must not leak
		// into the Scene selection's type conditions. With the AST walk these are
		// exact: an inline fragment's TypeCondition naming BaseFile or VisualFile
		// would have shown up in inlineMembers, so the empty-set check above already
		// covers it; assert explicitly for a clearer failure.
		if w.inlineMembers["BaseFile"] {
			t.Error("FindScenes selection has a `... on BaseFile`; Scene.files is the concrete VideoFile type")
		}
		if w.inlineMembers["VisualFile"] {
			t.Error("FindScenes selection has a `... on VisualFile`; that union belongs to Image, not Scene")
		}
	})
}

// walkResult accumulates the discrimination machinery found while walking a
// selection set transitively through its fragment spreads.
type walkResult struct {
	inlineMembers map[string]bool // TypeCondition of every *ast.InlineFragment reached
	hasTypename   bool            // a __typename Field appears anywhere in the closure
}

// walkSelection walks sel and every fragment it spreads, transitively, recording
// the type condition of each inline fragment and whether __typename is selected.
// Fragment spreads are resolved against frags; a cycle (or a spread naming an
// unknown fragment) terminates safely via the seen set.
func walkSelection(sel ast.SelectionSet, frags map[string]*ast.FragmentDefinition) walkResult {
	w := walkResult{inlineMembers: map[string]bool{}}
	seen := map[string]bool{}

	var walk func(ast.SelectionSet)
	walk = func(ss ast.SelectionSet) {
		for _, s := range ss {
			switch n := s.(type) {
			case *ast.Field:
				if n.Name == "__typename" {
					w.hasTypename = true
				}
				walk(n.SelectionSet)
			case *ast.InlineFragment:
				if n.TypeCondition != "" {
					w.inlineMembers[n.TypeCondition] = true
				}
				walk(n.SelectionSet)
			case *ast.FragmentSpread:
				if seen[n.Name] {
					continue
				}
				seen[n.Name] = true
				if fd, ok := frags[n.Name]; ok {
					walk(fd.SelectionSet)
				}
				// A spread naming no known fragment would itself be a defect, but
				// gate 7 owns fragment-set integrity; here we walk only what resolves.
			}
		}
	}
	walk(sel)
	return w
}

// parseGenerated reads and parses the generated operations and fragments into one
// query document. Both files are concatenated and parsed with parser.ParseQuery,
// which validates syntax only — exactly right here, since the generated documents
// use the @flatten genqlient directive and __typename meta-fields that a schema
// validator would need the full schema to accept; the AST shape is all this gate
// needs.
func parseGenerated(t *testing.T) *ast.QueryDocument {
	t.Helper()
	ops := mustRead(t, generatedOpsPath)
	frags := mustRead(t, generatedFragPath)
	src := &ast.Source{Name: "generated", Input: ops + "\n" + frags}
	doc, err := parser.ParseQuery(src)
	if err != nil {
		t.Fatalf("parsing generated GraphQL: %v", err)
	}
	return doc
}

// mustRead reads a generated source file, failing the test if it is missing.
func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// sortedKeys returns the keys of a set as a sorted slice, for stable diagnostics.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
