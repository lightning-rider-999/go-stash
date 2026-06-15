package conformance

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Paths to the generated GraphQL sources, relative to this package.
const (
	generatedOpsPath  = "../../operations/generated/operations.graphql"
	generatedFragPath = "../../operations/generated/fragments.graphql"
)

// inlineFragmentRE matches a GraphQL inline fragment type condition, capturing
// the member type name (e.g. "... on VideoFile" -> "VideoFile").
var inlineFragmentRE = regexp.MustCompile(`\.\.\.\s+on\s+([A-Za-z_][A-Za-z0-9_]*)`)

// fragmentSpreadRE matches a fragment spread, capturing the fragment name
// (e.g. "...SceneFields" -> "SceneFields"), while excluding inline fragments
// (which are "... on Type").
var fragmentSpreadRE = regexp.MustCompile(`\.\.\.([A-Za-z_][A-Za-z0-9_]*)`)

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
func TestAbstractTypeCoverage(t *testing.T) {
	ops := mustRead(t, generatedOpsPath)
	frags := mustRead(t, generatedFragPath)

	fragBodies := parseFragments(frags)
	opBodies := parseOperations(ops)

	t.Run("findFiles_carries_BaseFile_machinery", func(t *testing.T) {
		body, ok := opBodies["FindFiles"]
		if !ok {
			t.Fatal("FindFiles operation not found in generated operations.graphql")
		}
		// The inline fragments live directly in the operation body for findFiles.
		members := inlineMembers(t, body)
		for _, want := range []string{"BasicFile", "GalleryFile", "ImageFile", "VideoFile"} {
			if !members[want] {
				t.Errorf("FindFiles is missing an inline fragment for BaseFile member %q (got %v)", want, keys(members))
			}
		}
		if !strings.Contains(body, "__typename") {
			t.Error("FindFiles is missing __typename for BaseFile discrimination")
		}
	})

	t.Run("findImages_carries_VisualFile_machinery", func(t *testing.T) {
		selection := transitiveSelection(t, opBodies, fragBodies, "FindImages")
		members := inlineMembers(t, selection)
		for _, want := range []string{"ImageFile", "VideoFile"} {
			if !members[want] {
				t.Errorf("FindImages selection is missing an inline fragment for VisualFile member %q (got %v)", want, keys(members))
			}
		}
		if !strings.Contains(selection, "__typename") {
			t.Error("FindImages selection is missing __typename for VisualFile discrimination")
		}
	})

	t.Run("findScenes_has_no_abstract_machinery", func(t *testing.T) {
		selection := transitiveSelection(t, opBodies, fragBodies, "FindScenes")
		members := inlineMembers(t, selection)
		if len(members) != 0 {
			t.Errorf("FindScenes selection carries inline fragments %v; Scene uses the concrete VideoFile (VideoFileFields), so there must be none", keys(members))
		}
		if strings.Contains(selection, "... on BaseFile") || strings.Contains(selection, "BaseFile") {
			t.Error("FindScenes selection references BaseFile; Scene.files is the concrete VideoFile type")
		}
		// VisualFile is the Image union and must not leak into the Scene
		// selection.
		if strings.Contains(selection, "VisualFile") {
			t.Error("FindScenes selection references VisualFile; that union belongs to Image, not Scene")
		}
	})
}

// transitiveSelection concatenates an operation body with the bodies of every
// fragment it spreads, transitively, producing the full text that the operation
// selects against the server. Inline fragments inside referenced fragments are
// therefore visible to the caller.
func transitiveSelection(t *testing.T, ops, frags map[string]string, opName string) string {
	t.Helper()
	body, ok := ops[opName]
	if !ok {
		t.Fatalf("%s operation not found in generated operations.graphql", opName)
	}

	var sb strings.Builder
	sb.WriteString(body)

	seen := map[string]bool{}
	var pull func(text string)
	pull = func(text string) {
		for _, name := range spreadNames(text) {
			if seen[name] {
				continue
			}
			seen[name] = true
			frag, ok := frags[name]
			if !ok {
				// A spread that names no known fragment would itself be a defect,
				// but gate 7 owns fragment-set integrity; here we only need the
				// text we can resolve.
				continue
			}
			sb.WriteString("\n")
			sb.WriteString(frag)
			pull(frag)
		}
	}
	pull(body)
	return sb.String()
}

// spreadNames returns the fragment names spread in text (the "...Name" forms),
// excluding inline fragments ("... on Type").
func spreadNames(text string) []string {
	var out []string
	for _, m := range fragmentSpreadRE.FindAllStringSubmatch(text, -1) {
		name := m[1]
		if name == "on" {
			continue // this is an inline fragment, not a spread
		}
		out = append(out, name)
	}
	return out
}

// inlineMembers returns the set of member type names referenced by inline
// fragments in text.
func inlineMembers(t *testing.T, text string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, m := range inlineFragmentRE.FindAllStringSubmatch(text, -1) {
		out[m[1]] = true
	}
	return out
}

// parseOperations splits the operations source into a map from exported
// operation name to its full source block.
func parseOperations(src string) map[string]string {
	return parseBlocks(src, regexp.MustCompile(`(?m)^(?:query|mutation|subscription)\s+([A-Za-z_][A-Za-z0-9_]*)`))
}

// parseFragments splits the fragments source into a map from fragment name to
// its full source block.
func parseFragments(src string) map[string]string {
	return parseBlocks(src, regexp.MustCompile(`(?m)^fragment\s+([A-Za-z_][A-Za-z0-9_]*)\s+on\s`))
}

// parseBlocks slices src at each header matched by headerRE, returning a map
// from the captured name to the text spanning that header up to (but not
// including) the next header.
func parseBlocks(src string, headerRE *regexp.Regexp) map[string]string {
	idx := headerRE.FindAllStringSubmatchIndex(src, -1)
	out := make(map[string]string, len(idx))
	for i, m := range idx {
		name := src[m[2]:m[3]]
		start := m[0]
		end := len(src)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		out[name] = src[start:end]
	}
	return out
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

// keys returns the keys of a set as a slice, for diagnostics.
func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
