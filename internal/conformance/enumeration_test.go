package conformance

import (
	"testing"

	"github.com/lightning-rider-999/go-stashapp/internal/genops"
	"github.com/vektah/gqlparser/v2/ast"
)

// TestSDLInputEnumEnumeration is gate 2: the catalog's `$defs` must be exactly
// the set of InputObjects and Enums reachable from operation arguments.
//
//   - Forward: every InputObject/Enum reachable from a non-deprecated argument
//     of a root field (transitively, through input-object fields) resolves to a
//     `$defs` entry. A missing entry means the catalog under-documents the input
//     surface.
//   - Reverse: every `$defs` entry names a real schema type whose kind matches
//     the catalog's recorded kind ("input" -> INPUT_OBJECT, "enum" -> ENUM). A
//     stale entry means the catalog references a type the SDL no longer defines.
func TestSDLInputEnumEnumeration(t *testing.T) {
	f := load(t)

	// Reverse direction: every $defs entry is a real schema type of the right
	// kind.
	for name, def := range f.catalog.Defs {
		sdef, ok := f.schema.Types[name]
		if !ok {
			t.Errorf("catalog $defs has %q but the SDL defines no such type", name)
			continue
		}
		switch def.Kind {
		case "input":
			if sdef.Kind != ast.InputObject {
				t.Errorf("catalog $defs %q is kind %q but the SDL type is %s, not INPUT_OBJECT", name, def.Kind, sdef.Kind)
			}
		case "enum":
			if sdef.Kind != ast.Enum {
				t.Errorf("catalog $defs %q is kind %q but the SDL type is %s, not ENUM", name, def.Kind, sdef.Kind)
			}
		default:
			t.Errorf("catalog $defs %q has unexpected kind %q (want input or enum)", name, def.Kind)
		}
	}

	// Forward direction: compute the reachable input/enum closure independently
	// and assert each member has a $defs entry.
	want := reachableInputEnums(f.schema)
	for name := range want {
		if _, ok := f.catalog.Defs[name]; !ok {
			t.Errorf("type %q is reachable from operation arguments but has no catalog $defs entry", name)
		}
	}

	// The two sets should coincide exactly: the catalog must not invent $defs
	// entries that are unreachable from any operation argument.
	for name := range f.catalog.Defs {
		if !want[name] {
			t.Errorf("catalog $defs has %q but it is not reachable from any operation argument", name)
		}
	}
}

// reachableInputEnums returns the names of every InputObject and Enum reachable
// from the operations' input surface. It mirrors BuildCatalog's reachability
// rule exactly, so the two derivations cross-check:
//
//   - The closure is SEEDED from each root field's FULL argument list,
//     deprecated arguments included — the catalog's argDocs document every
//     argument (with a deprecation note), so $defs must resolve every type an
//     argument names, even one referenced only by a deprecated argument (e.g.
//     PluginArgInput via runPluginTask.args). Seeding from non-deprecated args
//     alone would leave that a dangling $defs reference.
//   - The closure then RECURSES through ALL input-object fields, deprecated
//     included — a deprecated input field's type is still part of that input's
//     wire shape (e.g. SceneMovieInput via SceneUpdateInput.movies).
func reachableInputEnums(s *ast.Schema) map[string]bool {
	seen := make(map[string]bool)

	var visit func(typeName string)
	visit = func(typeName string) {
		if seen[typeName] {
			return
		}
		def, ok := s.Types[typeName]
		if !ok {
			return
		}
		switch def.Kind {
		case ast.Enum:
			seen[typeName] = true
		case ast.InputObject:
			seen[typeName] = true
			for _, fld := range def.Fields {
				// All input-object fields, deprecated included.
				visit(genops.BaseTypeName(fld.Type))
			}
		}
	}

	for _, op := range rootOps {
		def := rootOperationDef(s, op)
		if def == nil {
			continue
		}
		for _, field := range def.Fields {
			if isIntrospection(field.Name) {
				continue
			}
			for _, arg := range field.Arguments {
				// Full argument list, deprecated included: the catalog documents
				// and resolves every argument's type.
				visit(genops.BaseTypeName(arg.Type))
			}
		}
	}
	return seen
}

// isIntrospection reports whether a field is a GraphQL introspection meta-field.
func isIntrospection(name string) bool {
	return name == "__schema" || name == "__type"
}
