package genops

import (
	"fmt"
	"os"
	"sort"

	"github.com/vektah/gqlparser/v2/ast"
	yaml "gopkg.in/yaml.v2"
)

// Overlay is the curated operation metadata that the SDL alone cannot express:
// which operations mutate irreversible state (Destructive) and which return a
// background job id rather than their result inline (JobReturning). Both lists
// are keyed by GraphQL root-field name, e.g. metadataScan — not by the
// generated operation name.
type Overlay struct {
	Destructive  []string `yaml:"destructive"`
	JobReturning []string `yaml:"jobReturning"`
}

// LoadOverlay reads and parses the curated overlay YAML at path. It does not
// validate the names against a schema — call [Overlay.Validate] (or build a
// manifest/catalog, which validates implicitly) once the schema is loaded.
func LoadOverlay(path string) (*Overlay, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read overlay %q: %w", path, err)
	}
	var ov Overlay
	if err := yaml.UnmarshalStrict(b, &ov); err != nil {
		return nil, fmt.Errorf("parse overlay %q: %w", path, err)
	}
	return &ov, nil
}

// Validate reports an error if any name in the overlay does not correspond to a
// real root field across Query, Mutation, and Subscription. The check keeps the
// overlay from silently drifting from the schema: an upstream rename or removal
// becomes a red build (the project's red-build philosophy).
func (ov *Overlay) Validate(s *ast.Schema) error {
	roots := rootFieldNames(s)
	var unknown []string
	for _, name := range ov.Destructive {
		if !roots[name] {
			unknown = append(unknown, "destructive: "+name)
		}
	}
	for _, name := range ov.JobReturning {
		if !roots[name] {
			unknown = append(unknown, "jobReturning: "+name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("overlay references unknown root fields: %v", unknown)
	}
	return nil
}

// rootFieldNames returns the set of every root-field name across Query,
// Mutation, and Subscription (introspection excluded, via RootFields).
func rootFieldNames(s *ast.Schema) map[string]bool {
	out := map[string]bool{}
	for _, op := range []ast.Operation{ast.Query, ast.Mutation, ast.Subscription} {
		for _, f := range RootFields(s, op) {
			out[f.Name] = true
		}
	}
	return out
}

// destructiveSet and jobReturningSet expose the overlay lists as sets keyed by
// root-field name, for O(1) lookup while building the manifest and catalog.
func (ov *Overlay) destructiveSet() map[string]bool  { return toSet(ov.Destructive) }
func (ov *Overlay) jobReturningSet() map[string]bool { return toSet(ov.JobReturning) }

func toSet(xs []string) map[string]bool {
	out := make(map[string]bool, len(xs))
	for _, x := range xs {
		out[x] = true
	}
	return out
}
