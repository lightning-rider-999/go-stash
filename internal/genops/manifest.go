package genops

import (
	"encoding/json"
	"sort"

	"github.com/vektah/gqlparser/v2/ast"
)

// ManifestEntry is the thin per-operation index record: enough to route a CLI
// invocation and flag its hazards, without the full argument/type detail that
// the catalog carries.
type ManifestEntry struct {
	Name              string `json:"name"`                        // FindScenes
	Field             string `json:"field"`                       // findScenes
	Kind              string `json:"kind"`                        // "query" | "mutation" | "subscription"
	InputType         string `json:"inputType,omitempty"`         // base type of the arg named "input", else ""
	ReturnType        string `json:"returnType"`                  // base named type of the field's return
	Destructive       bool   `json:"destructive,omitempty"`       // from the overlay
	JobReturning      bool   `json:"jobReturning,omitempty"`      // from the overlay
	Deprecated        bool   `json:"deprecated,omitempty"`        // field carries @deprecated
	DeprecationReason string `json:"deprecationReason,omitempty"` // verbatim reason
}

// Manifest indexes every generated operation against the schema version it was
// derived from. Operations is sorted by Name for deterministic output.
type Manifest struct {
	SchemaVersion string          `json:"schemaVersion"`
	Operations    []ManifestEntry `json:"operations"`
}

// opKind maps a root operation type to its manifest/catalog kind string.
func opKind(op ast.Operation) string {
	switch op {
	case ast.Query:
		return "query"
	case ast.Mutation:
		return "mutation"
	case ast.Subscription:
		return "subscription"
	default:
		return string(op)
	}
}

// BuildManifest produces one entry per root field across Query, Mutation, and
// Subscription (211 for the vendored v0.31.1 SDL), sorted by Name. The overlay
// is validated against the schema first, so an overlay that references a
// non-existent root field is an error rather than a silent miss.
func BuildManifest(s *ast.Schema, ov *Overlay, schemaVersion string) (*Manifest, error) {
	if err := ov.Validate(s); err != nil {
		return nil, err
	}
	destructive := ov.destructiveSet()
	jobReturning := ov.jobReturningSet()

	var entries []ManifestEntry
	for _, op := range []ast.Operation{ast.Query, ast.Mutation, ast.Subscription} {
		for _, f := range RootFields(s, op) {
			e := ManifestEntry{
				Name:         exportName(f.Name),
				Field:        f.Name,
				Kind:         opKind(op),
				InputType:    inputArgType(f),
				ReturnType:   BaseTypeName(f.Type),
				Destructive:  destructive[f.Name],
				JobReturning: jobReturning[f.Name],
				Deprecated:   IsDeprecated(f),
			}
			if e.Deprecated {
				e.DeprecationReason = DeprecationReason(f)
			}
			entries = append(entries, e)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	return &Manifest{SchemaVersion: schemaVersion, Operations: entries}, nil
}

// inputArgType returns the base type of the argument literally named "input",
// or "" if the field has no such argument. Stash's mutation convention wraps a
// write payload in a single input:<T>Input! argument.
func inputArgType(f *ast.FieldDefinition) string {
	if a := f.Arguments.ForName("input"); a != nil {
		return BaseTypeName(a.Type)
	}
	return ""
}

// JSON renders the manifest as deterministic, 2-space-indented JSON with a
// trailing newline. Map keys are absent (the manifest is all slices), and
// Operations is pre-sorted, so the output is byte-stable across runs.
func (m *Manifest) JSON() ([]byte, error) {
	return marshalIndent(m)
}

// marshalIndent is the shared deterministic JSON encoder for the manifest and
// catalog: 2-space indent, a trailing newline, and HTML escaping left on (the
// stdlib default) so the output matches `json.MarshalIndent` exactly.
func marshalIndent(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
