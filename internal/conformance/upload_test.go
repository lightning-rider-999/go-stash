package conformance

import (
	"fmt"
	"sort"
	"testing"

	genops "github.com/trackness/graphql-opgen"
	"github.com/vektah/gqlparser/v2/ast"
)

// TestExactlyOneUpload is gate 9: the SDL must declare exactly one field whose
// (unwrapped) type is the Upload scalar, and it must be ImportObjectsInput.file.
// Upload is the multipart transport scalar; a second Upload field would mean a
// second multipart code path the client does not handle, so its uniqueness is a
// load-bearing invariant. The walk covers every input-object field and every
// output-field argument across the whole schema.
func TestExactlyOneUpload(t *testing.T) {
	f := load(t)

	type loc struct{ owner, field string }
	var found []loc

	for _, def := range f.schema.Types {
		// Input-object fields.
		if def.Kind == ast.InputObject {
			for _, fld := range def.Fields {
				if genops.BaseTypeName(fld.Type) == "Upload" {
					found = append(found, loc{def.Name, fld.Name})
				}
			}
		}
		// Object/interface field arguments (an Upload could appear as an arg).
		if def.Kind == ast.Object || def.Kind == ast.Interface {
			for _, fld := range def.Fields {
				for _, arg := range fld.Arguments {
					if genops.BaseTypeName(arg.Type) == "Upload" {
						found = append(found, loc{def.Name + "." + fld.Name, "arg:" + arg.Name})
					}
				}
				// An output field typed Upload would be unusual, but check it.
				if genops.BaseTypeName(fld.Type) == "Upload" {
					found = append(found, loc{def.Name, fld.Name})
				}
			}
		}
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].owner != found[j].owner {
			return found[i].owner < found[j].owner
		}
		return found[i].field < found[j].field
	})

	if len(found) != 1 {
		var s []string
		for _, l := range found {
			s = append(s, fmt.Sprintf("%s.%s", l.owner, l.field))
		}
		t.Fatalf("found %d Upload-typed fields, want exactly 1: %v", len(found), s)
	}
	if found[0].owner != "ImportObjectsInput" || found[0].field != "file" {
		t.Errorf("the single Upload field is %s.%s, want ImportObjectsInput.file", found[0].owner, found[0].field)
	}
}
