package conformance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"

	"github.com/lightning-rider-999/go-stashapp/internal/genops"
	gqlast "github.com/vektah/gqlparser/v2/ast"
)

// generatedGoPath is the genqlient-generated typed surface, relative to this
// package.
const generatedGoPath = "../../stash/operations_gen.go"

// canonicalNamedTypes are the canonical fragment-derived entity types that MUST
// exist as standalone named structs (not path-named). These are the SceneFields
// / *Ref / *Fields shapes whose stability the whole codegen design rests on: if
// one of these decayed into a path-named struct, every caller's type reference
// would churn on each generation.
var canonicalNamedTypes = []string{
	"SceneFields", "ImageFields", "GalleryFields", "GroupFields",
	"MovieFields", "PerformerFields", "StudioFields", "TagFields",
	"SceneMarkerFields", "VideoFileFields", "BasicFileFields",
	"GalleryFileFields", "ImageFileFields", "FolderFields", "FingerprintFields",
	"PluginFields",
	"SceneRef", "ImageRef", "GalleryRef", "GroupRef",
	"PerformerRef", "StudioRef", "TagRef", "SceneMarkerRef", "GalleryChapterRef",
}

// TestCanonicalTypeStability is gate 7: the canonical fragment types are named,
// stable structs, and no SceneFields-style ENTITY type is path-named outside the
// audited A3 allowlist.
//
// The gate has three parts:
//
//  1. The A3 allowlist exactly accounts for the path-named types the fragment
//     generator emits: UnlistedPathNamed(BuildFragments(schema)) is empty.
//  2. The canonical entity types exist as standalone named structs in the
//     generated Go file (proving they did not decay into path-named shapes).
//  3. A go/ast scan of the generated file finds no path-named NESTED ENTITY
//     struct that is neither (a) an operation-response wrapper (named after an
//     operation, which is expected) nor (b) reducible to an allowlisted leaf
//     type. The heuristic is documented and conservative; see classifyPathNamed.
func TestCanonicalTypeStability(t *testing.T) {
	f := load(t)

	t.Run("A3_allowlist_matches_UnlistedPathNamed", func(t *testing.T) {
		fs := genops.BuildFragments(f.schema)
		unlisted := genops.UnlistedPathNamed(fs)
		if len(unlisted) != 0 {
			t.Errorf("UnlistedPathNamed is non-empty (generation drift): %v\n"+
				"A new path-named shape appeared in the fragment set. Audit it, then "+
				"either fix the generator or add it to genops' pathNamedAllowlist with a reason.", unlisted)
		}

		// Every path-named type the fragments emit must be in the allowlist —
		// the allowlist is the audited union, so it is a superset.
		allowed := make(map[string]bool)
		for _, name := range genops.AllowedPathNamed() {
			allowed[name] = true
		}
		for _, name := range fs.PathNamedTypes() {
			if !allowed[name] {
				t.Errorf("fragment set emits path-named type %q absent from AllowedPathNamed()", name)
			}
		}
	})

	structs := parseGeneratedStructs(t)

	t.Run("canonical_entity_types_are_named", func(t *testing.T) {
		allowedLeaves := genops.AllowedPathNamed()
		for _, name := range canonicalNamedTypes {
			if !structs[name] {
				t.Errorf("canonical type %q is not a standalone named struct in the generated surface; "+
					"it may have decayed into a path-named shape", name)
			}
			// A canonical type must be a flat name: it must NOT carry an extra
			// allowlisted leaf suffix on top of its <Entity>Fields/<Entity>Ref
			// form, which would mark it as a path-named nested selection.
			if _, ok := allowlistedLeafSuffix(name, allowedLeaves); ok {
				t.Errorf("canonical type %q carries an A3 leaf suffix; canonical entity types must be flat names", name)
			}
		}
	})

	t.Run("no_unaudited_path_named_entity", func(t *testing.T) {
		opNames := make(map[string]bool, len(f.manifest.Operations))
		for _, e := range f.manifest.Operations {
			opNames[e.Name] = true
		}
		// The audited leaf set is the A3 allowlist PLUS the concrete members of
		// any allowlisted union/interface — genqlient emits one nested struct per
		// union member (e.g. VisualFile -> VideoFile, ImageFile), and those
		// members are a mechanical consequence of auditing the abstract type, not
		// separate drift. The member set is read from the SDL, so it stays
		// authoritative rather than guessed.
		allowedLeaves := auditedLeafSet(f.schema, genops.AllowedPathNamed())

		// canonicalSet is the set of flat canonical fragment type names; a
		// path-named struct must begin with one of these prefixes to count as a
		// fragment-derived nested entity (as opposed to an operation wrapper or
		// an input/enum type).
		canonicalSet := make(map[string]bool, len(canonicalNamedTypes))
		for _, n := range canonicalNamedTypes {
			canonicalSet[n] = true
		}

		var offenders []string
		for name := range structs {
			// genqlient emits __premarshal* helpers for union/interface types;
			// they mirror an already-classified type and are not entities.
			if strings.HasPrefix(name, "__premarshal") {
				continue
			}
			// A flat canonical name, or a bare input/enum type, is not path-named.
			if canonicalSet[name] || !looksFragmentDerived(name, canonicalSet) {
				continue
			}
			// Operation-response wrappers are named after an operation and are
			// expected to be path-named — they hold the response shape, not a
			// reusable entity.
			if isOperationWrapper(name, opNames) {
				continue
			}
			// A fragment-derived path-named struct is acceptable only when it
			// terminates in an allowlisted A3 leaf type (junction wrappers,
			// union/interface members, terminated value cycles). The leaf is the
			// trailing GraphQL type name, so a suffix match against the allowlist
			// is exact.
			if leaf, ok := allowlistedLeafSuffix(name, allowedLeaves); ok {
				_ = leaf
				continue
			}
			offenders = append(offenders, name)
		}
		sort.Strings(offenders)
		if len(offenders) > 0 {
			t.Errorf("%d fragment-derived path-named struct(s) do not terminate in an audited A3 leaf "+
				"(a SceneFields-style entity may have become path-named — generation drift):\n  %s\n"+
				"Audit each shape, then fix the generator or add the leaf to genops' pathNamedAllowlist.",
				len(offenders), strings.Join(offenders, "\n  "))
		}
	})

	t.Run("RefName_FieldsName_match_canonical", func(t *testing.T) {
		// The canonical naming functions must agree with what the generator
		// actually emits, so callers can name types by convention.
		if got := genops.RefName("Studio"); got != "StudioRef" {
			t.Errorf("RefName(Studio) = %q, want StudioRef", got)
		}
		if got := genops.FieldsName("Scene"); got != "SceneFields" {
			t.Errorf("FieldsName(Scene) = %q, want SceneFields", got)
		}
		if !structs[genops.RefName("Studio")] {
			t.Error("RefName(Studio) names a type that does not exist in the generated surface")
		}
		if !structs[genops.FieldsName("Scene")] {
			t.Error("FieldsName(Scene) names a type that does not exist in the generated surface")
		}
	})
}

// looksFragmentDerived reports whether a generated type name is a nested type
// emitted from the fragment surface — i.e. it begins with a canonical fragment
// type name (e.g. "SceneFields", "ImageFields", "BasicFileFields") followed by
// more characters (the selection field path plus a leaf type). genqlient names
// nested selection types by concatenating the parent type name with the
// camelCased field name and the leaf type, so a fragment-derived nested type's
// name literally starts with its canonical parent's name. A name equal to a
// canonical name (handled by the caller) is flat, not derived; an operation
// wrapper starts with an operation name, not a fragment name. The check is
// deliberately conservative: it only fires for names with a canonical prefix
// AND trailing characters.
func looksFragmentDerived(name string, canonicalSet map[string]bool) bool {
	for canonical := range canonicalSet {
		if len(name) > len(canonical) && strings.HasPrefix(name, canonical) {
			// The next character after the prefix must start a new word
			// (uppercase), so "BasicFileFields..." matches "BasicFileFields" but
			// "SceneFieldsX" would not falsely match "SceneField".
			next := name[len(canonical)]
			if next >= 'A' && next <= 'Z' {
				return true
			}
		}
	}
	return false
}

// allowlistedLeafSuffix reports whether name terminates in one of the audited
// leaf type names, returning the matched leaf. genqlient appends the leaf
// GraphQL type name last, so a path-named nested selection ends with exactly its
// leaf type name — a suffix match is precise. When several allowlisted names are
// suffixes (none currently overlap), the longest match is returned.
func allowlistedLeafSuffix(name string, allowedLeaves []string) (string, bool) {
	best := ""
	for _, leaf := range allowedLeaves {
		if strings.HasSuffix(name, leaf) && len(leaf) > len(best) {
			best = leaf
		}
	}
	return best, best != ""
}

// auditedLeafSet expands the A3 allowlist with the concrete members of any
// allowlisted union or interface type, read from the schema. Auditing an
// abstract type (VisualFile, BaseFile) implicitly audits the per-member nested
// structs genqlient emits for it, so those members must count as allowed leaves.
func auditedLeafSet(s *gqlast.Schema, allowlist []string) []string {
	out := make([]string, 0, len(allowlist))
	out = append(out, allowlist...)
	for _, name := range allowlist {
		def, ok := s.Types[name]
		if !ok {
			continue
		}
		switch def.Kind {
		case gqlast.Union:
			out = append(out, def.Types...)
		case gqlast.Interface:
			for _, impl := range s.GetPossibleTypes(def) {
				out = append(out, impl.Name)
			}
		}
	}
	return out
}

// isOperationWrapper reports whether a path-named type is an operation-response
// wrapper, i.e. its name begins with the name of a known operation. Such types
// (FindScenesFindScenes..., SceneCreateSceneCreate...) hold the per-operation
// response tree and are expected to be path-named.
func isOperationWrapper(name string, opNames map[string]bool) bool {
	if opNames[name] {
		return true // the top-level response holder, named exactly after the op.
	}
	if strings.HasSuffix(name, "Response") {
		return opNames[strings.TrimSuffix(name, "Response")]
	}
	for op := range opNames {
		if strings.HasPrefix(name, op) {
			return true
		}
	}
	return false
}

// parseGeneratedStructs parses the generated Go file and returns the set of
// declared struct and interface type names.
func parseGeneratedStructs(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, generatedGoPath, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", generatedGoPath, err)
	}
	out := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		switch ts.Type.(type) {
		case *ast.StructType, *ast.InterfaceType:
			out[ts.Name.Name] = true
		}
		return true
	})
	return out
}
