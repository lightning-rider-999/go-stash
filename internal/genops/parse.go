// Package genops compiles Stash's vendored GraphQL SDL into a typed surface:
// one genqlient operation per root field, a thin manifest indexing those
// operations, and a machine-facing catalog of inputs, enums, and deprecations.
//
// The compiler reads strictly from the schema AST (gqlparser/v2) — it never
// carries a hand-maintained list of fields or edges, so a server upgrade that
// drifts a field is a red build rather than a silent nil.
//
// Field enumeration distinguishes two surfaces:
//
//   - Root operations ([RootFields]) include every field of Query, Mutation,
//     and Subscription, deprecated ones included — every operation stays
//     reachable from the CLI, with deprecations flagged in the catalog.
//   - Entity selections ([Edges] and [Scalars]) exclude @deprecated fields, so
//     the canonical fragment types carry the clean, current surface; the
//     deprecated fields are still recorded in the catalog.
package genops

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// isIntrospection reports whether a field is a GraphQL introspection meta-field
// (__schema, __type, __typename). gqlparser injects __schema and __type into
// the Query type; these are never real operations and must not be generated.
func isIntrospection(name string) bool {
	return strings.HasPrefix(name, "__")
}

// LoadSchema parses every *.graphql file under dir (recursively) into a single
// validated schema. Sources are loaded in a deterministic (name-sorted) order.
func LoadSchema(dir string) (*ast.Schema, error) {
	var sources []*ast.Source
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".graphql" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			rel = path
		}
		sources = append(sources, &ast.Source{Name: filepath.ToSlash(rel), Input: string(b)})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk schema dir %q: %w", dir, err)
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("no .graphql files under %q", dir)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })

	schema, gerr := gqlparser.LoadSchema(sources...)
	if gerr != nil {
		return nil, fmt.Errorf("load schema: %w", gerr)
	}
	return schema, nil
}

// IsDeprecated reports whether a field carries an @deprecated directive.
func IsDeprecated(f *ast.FieldDefinition) bool {
	return f.Directives.ForName("deprecated") != nil
}

// DeprecationReason returns the verbatim @deprecated reason for a field, or ""
// if the field is not deprecated (or carries no explicit reason).
func DeprecationReason(f *ast.FieldDefinition) string {
	d := f.Directives.ForName("deprecated")
	if d == nil {
		return ""
	}
	if arg := d.Arguments.ForName("reason"); arg != nil && arg.Value != nil {
		return arg.Value.Raw
	}
	return ""
}

// BaseTypeName unwraps any list wrappers and returns the underlying named type.
func BaseTypeName(t *ast.Type) string {
	for t.Elem != nil {
		t = t.Elem
	}
	return t.NamedType
}

// kind returns the DefinitionKind of a field's underlying named type, or the
// empty kind if the type is unknown to the schema.
func kind(s *ast.Schema, f *ast.FieldDefinition) ast.DefinitionKind {
	if def := s.Types[BaseTypeName(f.Type)]; def != nil {
		return def.Kind
	}
	return ""
}

// RootFields returns every field of the given root operation type (query,
// mutation, or subscription), deprecated fields included but introspection
// meta-fields (__schema, __type) excluded. The result is nil if the schema does
// not define that root type.
func RootFields(s *ast.Schema, op ast.Operation) ast.FieldList {
	switch op {
	case ast.Query:
		return realFields(s.Query)
	case ast.Mutation:
		return realFields(s.Mutation)
	case ast.Subscription:
		return realFields(s.Subscription)
	default:
		return nil
	}
}

func realFields(def *ast.Definition) ast.FieldList {
	if def == nil {
		return nil
	}
	out := make(ast.FieldList, 0, len(def.Fields))
	for _, f := range def.Fields {
		if isIntrospection(f.Name) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// Edges returns the non-deprecated fields of def whose underlying type is an
// object, interface, or union — the relationships that resolve to other
// entities. Deprecated edges are omitted (recorded in the catalog instead).
func Edges(s *ast.Schema, def *ast.Definition) []*ast.FieldDefinition {
	var out []*ast.FieldDefinition
	for _, f := range def.Fields {
		if IsDeprecated(f) || isIntrospection(f.Name) {
			continue
		}
		switch kind(s, f) {
		case ast.Object, ast.Interface, ast.Union:
			out = append(out, f)
		}
	}
	return out
}

// Scalars returns the non-deprecated fields of def whose underlying type is a
// scalar or enum — the leaf data of the entity.
func Scalars(s *ast.Schema, def *ast.Definition) []*ast.FieldDefinition {
	var out []*ast.FieldDefinition
	for _, f := range def.Fields {
		if IsDeprecated(f) || isIntrospection(f.Name) {
			continue
		}
		switch kind(s, f) {
		case ast.Scalar, ast.Enum:
			out = append(out, f)
		}
	}
	return out
}
