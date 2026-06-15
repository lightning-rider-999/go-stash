package conformance

import (
	"testing"

	"github.com/lightning-rider-999/go-stashapp/internal/genops"
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

	t.Run("deprecated_field_carries_verbatim_reason", func(t *testing.T) {
		// AllGalleries is a deprecated root field; the catalog command must carry
		// the verbatim SDL reason.
		cmd, ok := f.catalog.Commands["AllGalleries"]
		if !ok {
			t.Fatal("AllGalleries command missing from catalog")
		}
		field := genops.RootFields(f.schema, ast.Query).ForName("allGalleries")
		if field == nil {
			t.Fatal("allGalleries is not a Query root field in the SDL")
		}
		want := genops.DeprecationReason(field)
		if want == "" {
			t.Fatal("allGalleries carries no SDL deprecation reason; fixture assumption is stale")
		}
		if cmd.Deprecated != want {
			t.Errorf("AllGalleries deprecated reason = %q, want verbatim SDL reason %q", cmd.Deprecated, want)
		}

		// A deprecated input field: BulkGalleryUpdateInput.url -> "Use urls".
		def, ok := f.catalog.Defs["BulkGalleryUpdateInput"]
		if !ok {
			t.Fatal("BulkGalleryUpdateInput missing from $defs")
		}
		var urlField *genops.FieldDoc
		for i := range def.Fields {
			if def.Fields[i].Name == "url" {
				urlField = &def.Fields[i]
				break
			}
		}
		if urlField == nil {
			t.Fatal("BulkGalleryUpdateInput.url missing from $defs fields")
		}
		sdef := f.schema.Types["BulkGalleryUpdateInput"]
		sdlField := sdef.Fields.ForName("url")
		wantReason := deprecationReasonOfInput(sdlField)
		if wantReason == "" {
			t.Fatal("BulkGalleryUpdateInput.url carries no SDL deprecation reason; fixture assumption is stale")
		}
		if urlField.Deprecated != wantReason {
			t.Errorf("BulkGalleryUpdateInput.url deprecated reason = %q, want %q", urlField.Deprecated, wantReason)
		}
	})
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
