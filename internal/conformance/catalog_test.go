package conformance

import (
	"testing"

	genops "github.com/trackness/graphql-opgen"
	"github.com/vektah/gqlparser/v2/ast"
)

// TestCatalogCoverage is gate 8: the catalog documents every operation, its
// enum references resolve to faithful `$defs` enums, and deprecated fields carry
// the verbatim SDL reason.
//
//   - Every manifest operation has a Catalog.Commands[Name] entry.
//   - Every enum referenced by a `$defs` input field's type resolves to a
//     `$defs` enum whose values are exactly the SDL enum's values, in the same
//     order. The catalog records enum symbols as their wire values, so symbol ==
//     wire value is asserted directly against the SDL.
//   - A deprecated SDL field surfaces in the catalog with its verbatim
//     @deprecated reason.
func TestCatalogCoverage(t *testing.T) {
	f := load(t)

	t.Run("every_manifest_op_has_a_command", func(t *testing.T) {
		for _, e := range f.manifest.Operations {
			if _, ok := f.catalog.Commands[e.Name]; !ok {
				t.Errorf("manifest operation %q has no Catalog.Commands entry", e.Name)
			}
		}
		if len(f.catalog.Commands) != len(f.manifest.Operations) {
			t.Errorf("catalog commands = %d, manifest operations = %d; they must match 1:1",
				len(f.catalog.Commands), len(f.manifest.Operations))
		}
	})

	t.Run("enum_refs_resolve_and_values_match_SDL", func(t *testing.T) {
		for defName, def := range f.catalog.Defs {
			if def.Kind != "input" {
				continue
			}
			for _, fld := range def.Fields {
				base := genops.BaseTypeName(typeFromString(fld.Type))
				sdef, ok := f.schema.Types[base]
				if !ok || sdef.Kind != ast.Enum {
					continue // not an enum reference
				}
				// The referenced enum must itself be a $defs enum.
				enumDef, ok := f.catalog.Defs[base]
				if !ok {
					t.Errorf("input %q field %q references enum %q which has no $defs entry", defName, fld.Name, base)
					continue
				}
				if enumDef.Kind != "enum" {
					t.Errorf("$defs %q is referenced as an enum by %q.%q but its kind is %q", base, defName, fld.Name, enumDef.Kind)
					continue
				}
				// Catalog enum values must equal the SDL enum values exactly,
				// symbol == wire value, same order.
				assertEnumMatchesSDL(t, base, enumDef, sdef)
			}
		}
	})

	t.Run("every_deprecated_root_field_carries_verbatim_reason", func(t *testing.T) {
		// The CLASS check: every deprecated root field across Query/Mutation/
		// Subscription must surface its verbatim @deprecated reason in its catalog
		// command, and no command may carry a deprecation reason its SDL field does
		// not. This catches a generator that drops, truncates, or fabricates a
		// reason for ANY operation, not just the one historically spot-checked.
		checked := 0
		for _, op := range rootOps {
			for _, field := range genops.RootFields(f.schema, op) {
				want := genops.DeprecationReason(field)
				cmd, ok := f.catalog.Commands[exportNameLocal(field.Name)]
				if !ok {
					t.Errorf("root field %q has no catalog command", field.Name)
					continue
				}
				if cmd.Deprecated != want {
					t.Errorf("command %q deprecated reason = %q, want verbatim SDL reason %q",
						cmd.Field, cmd.Deprecated, want)
				}
				if want != "" {
					checked++
				}
			}
		}
		if checked == 0 {
			t.Fatal("no deprecated root field was found in the SDL; the fixture or schema is stale " +
				"(allGalleries/findMovie etc. should be deprecated)")
		}
	})

	t.Run("every_deprecated_input_field_carries_verbatim_reason", func(t *testing.T) {
		// The CLASS check for $defs: every deprecated field of every catalogued
		// input object must carry its verbatim SDL @deprecated reason, and no
		// FieldDoc may invent one. This is the input-side mirror of the root-field
		// class check above.
		checked := 0
		for defName, def := range f.catalog.Defs {
			if def.Kind != "input" {
				continue
			}
			sdef, ok := f.schema.Types[defName]
			if !ok {
				continue // the enumeration gate owns dangling $defs entries.
			}
			for i := range def.Fields {
				fld := def.Fields[i]
				sdlField := sdef.Fields.ForName(fld.Name)
				want := deprecationReasonOfInput(sdlField)
				if fld.Deprecated != want {
					t.Errorf("$defs %q field %q deprecated reason = %q, want verbatim SDL reason %q",
						defName, fld.Name, fld.Deprecated, want)
				}
				if want != "" {
					checked++
				}
			}
		}
		if checked == 0 {
			t.Fatal("no deprecated input field was found across $defs; the fixture or schema is stale " +
				"(e.g. BulkGalleryUpdateInput.url should be deprecated)")
		}
	})
}

// exportNameLocal upper-cases the first rune of a camelCase root-field name to
// the exported operation name the catalog keys commands by (findScenes ->
// FindScenes). It mirrors genops' internal exportName, which is unexported.
func exportNameLocal(field string) string {
	if field == "" {
		return ""
	}
	r := []rune(field)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] = r[0] - 'a' + 'A'
	}
	return string(r)
}

// assertEnumMatchesSDL fails the test unless the catalog enum's values are
// exactly the SDL enum's values, in order, with each catalog value equal to the
// SDL value name (symbol == wire value).
func assertEnumMatchesSDL(t *testing.T, name string, catEnum genops.TypeDef, sdef *ast.Definition) {
	t.Helper()
	if len(catEnum.Values) != len(sdef.EnumValues) {
		t.Errorf("enum %q: catalog has %d values, SDL has %d", name, len(catEnum.Values), len(sdef.EnumValues))
		return
	}
	for i, cv := range catEnum.Values {
		sv := sdef.EnumValues[i]
		if cv.Value != sv.Name {
			t.Errorf("enum %q value[%d] = %q, want SDL %q (symbol must equal wire value, in order)", name, i, cv.Value, sv.Name)
		}
	}
}

// typeFromString parses a catalog type string (e.g. "[GenderEnum!]") back into
// an ast.Type so BaseTypeName can unwrap it. The catalog records types via
// ast.Type.String(), so this is the inverse.
func typeFromString(s string) *ast.Type {
	return parseTypeRef(s)
}

// parseTypeRef parses a GraphQL type reference string into an ast.Type. It
// handles named types, non-null (!), and list ([]) wrappers as produced by
// ast.Type.String().
func parseTypeRef(s string) *ast.Type {
	nonNull := false
	if len(s) > 0 && s[len(s)-1] == '!' {
		nonNull = true
		s = s[:len(s)-1]
	}
	if len(s) >= 2 && s[0] == '[' && s[len(s)-1] == ']' {
		return &ast.Type{Elem: parseTypeRef(s[1 : len(s)-1]), NonNull: nonNull}
	}
	return &ast.Type{NamedType: s, NonNull: nonNull}
}

// deprecationReasonOfInput returns the verbatim @deprecated reason of an input
// field definition, or "" if it is not deprecated.
func deprecationReasonOfInput(fld *ast.FieldDefinition) string {
	if fld == nil {
		return ""
	}
	d := fld.Directives.ForName("deprecated")
	if d == nil {
		return ""
	}
	if arg := d.Arguments.ForName("reason"); arg != nil && arg.Value != nil {
		return arg.Value.Raw
	}
	return ""
}
