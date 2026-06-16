package genops

import (
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/lightning-rider-999/go-stashapp/internal/exitcode"
)

// Catalog is the machine-facing description of the generated client surface:
// one CommandDoc per operation plus the transitive closure of input objects and
// enums those operations reference ($defs). It is consumed by `stash catalog`
// and by agents that drive the CLI, so every field is fully self-describing.
type Catalog struct {
	SchemaVersion string                `json:"schemaVersion"`
	Commands      map[string]CommandDoc `json:"commands"` // keyed by operation Name
	Defs          map[string]TypeDef    `json:"$defs"`    // input objects + enums, keyed by type name
}

// CommandDoc describes one operation: its call surface, hazards, and the exit
// codes a caller may observe.
type CommandDoc struct {
	Field        string   `json:"field"`
	Kind         string   `json:"kind"`
	Description  string   `json:"description,omitempty"`
	Args         []ArgDoc `json:"args,omitempty"`
	ReturnType   string   `json:"returnType"`
	Destructive  bool     `json:"destructive,omitempty"`
	JobReturning bool     `json:"jobReturning,omitempty"`
	Deprecated   string   `json:"deprecated,omitempty"` // verbatim @deprecated reason
	ExitCodes    []string `json:"exitCodes"`            // frozen taxonomy, derived below
}

// ArgDoc describes one operation argument. Unlike the operation generator,
// the catalog INCLUDES deprecated arguments (flagged via Deprecated) so an
// agent can see, for example, that a deprecated scene_ids still exists.
type ArgDoc struct {
	Name        string `json:"name"`
	Type        string `json:"type"`     // ast.Type.String(), e.g. "[ID!]" or "SceneFilterType"
	Required    bool   `json:"required"` // top-level NonNull with no default
	Default     string `json:"default,omitempty"`
	Deprecated  string `json:"deprecated,omitempty"`
	Description string `json:"description,omitempty"`
}

// TypeDef describes one referenced input object or enum.
type TypeDef struct {
	Kind        string         `json:"kind"` // "input" | "enum"
	Description string         `json:"description,omitempty"`
	Fields      []FieldDoc     `json:"fields,omitempty"` // input objects, SDL order
	Values      []EnumValueDoc `json:"values,omitempty"` // enums, SDL order
}

// FieldDoc describes one field of an input object.
type FieldDoc struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Default     string `json:"default,omitempty"`
	Deprecated  string `json:"deprecated,omitempty"`
	Description string `json:"description,omitempty"`
}

// EnumValueDoc describes one enum value. Value is the wire symbol.
type EnumValueDoc struct {
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Deprecated  string `json:"deprecated,omitempty"`
}

// baseExitCodes are the names of the exit codes every command can return, in
// frozen order. The catalog needs only the names; they are sourced from the
// shared taxonomy in internal/exitcode so the catalog's vocabulary cannot drift
// from the CLI's actual exit codes.
var baseExitCodes = exitCodeNames(exitcode.Base)

// exitCodeNames projects the names out of a slice of taxonomy codes, in order.
func exitCodeNames(codes []exitcode.Code) []string {
	names := make([]string, len(codes))
	for i, c := range codes {
		names[i] = c.Name
	}
	return names
}

// BuildCatalog produces the catalog: a CommandDoc per root field plus the
// transitive closure of input objects and enums reachable from every
// operation's non-deprecated arguments. The overlay is validated against the
// schema first.
func BuildCatalog(s *ast.Schema, ov *Overlay, schemaVersion string) (*Catalog, error) {
	if err := ov.Validate(s); err != nil {
		return nil, err
	}
	destructive := ov.destructiveSet()
	jobReturning := ov.jobReturningSet()

	commands := map[string]CommandDoc{}
	defs := map[string]TypeDef{}
	reach := newReach(s)

	for _, op := range []ast.Operation{ast.Query, ast.Mutation, ast.Subscription} {
		for _, f := range RootFields(s, op) {
			name := exportName(f.Name)
			isDestructive := destructive[f.Name]
			isJob := jobReturning[f.Name]

			commands[name] = CommandDoc{
				Field:        f.Name,
				Kind:         opKind(op),
				Description:  f.Description,
				Args:         argDocs(f),
				ReturnType:   BaseTypeName(f.Type),
				Destructive:  isDestructive,
				JobReturning: isJob,
				Deprecated:   DeprecationReason(f),
				ExitCodes:    exitCodes(s, f, isDestructive, isJob),
			}
			// Seed reachability from the operation's FULL argument list,
			// deprecated args included: argDocs documents every argument (with a
			// deprecation note), so $defs must resolve every type those args name,
			// even one referenced only by a deprecated arg (e.g. PluginArgInput via
			// runPluginTask.args). The set-membership guard makes the closure
			// terminate on mutual recursion.
			for _, a := range f.Arguments {
				reach.add(BaseTypeName(a.Type))
			}
		}
	}

	// Emit a TypeDef for every reachable input object and enum.
	for name := range reach.set {
		def := s.Types[name]
		switch def.Kind {
		case ast.InputObject:
			defs[name] = inputTypeDef(s, def)
		case ast.Enum:
			defs[name] = enumTypeDef(def)
		}
	}

	return &Catalog{SchemaVersion: schemaVersion, Commands: commands, Defs: defs}, nil
}

// JSON renders the catalog as deterministic, 2-space-indented JSON with a
// trailing newline. Commands and $defs are maps, so json.MarshalIndent sorts
// their keys; every slice within is in a fixed order, so the output is stable.
func (c *Catalog) JSON() ([]byte, error) {
	return marshalIndent(c)
}

// reach accumulates the transitive set of input-object and enum type names
// reachable from a seed of argument types, expanding through input-object
// fields to a fixpoint. Scalars, objects, unions, and interfaces are not added;
// a self-referential input object (e.g. SceneFilterType) is added once.
type reach struct {
	schema *ast.Schema
	set    map[string]bool
}

func newReach(s *ast.Schema) *reach {
	return &reach{schema: s, set: map[string]bool{}}
}

// add records typeName if it names an input object or enum, and — for an input
// object newly added — transitively records the base types of its fields. The
// set membership check makes the recursion terminate on cycles.
func (r *reach) add(typeName string) {
	def := r.schema.Types[typeName]
	if def == nil || r.set[typeName] {
		return
	}
	switch def.Kind {
	case ast.Enum:
		r.set[typeName] = true
	case ast.InputObject:
		r.set[typeName] = true
		for _, fld := range def.Fields {
			r.add(BaseTypeName(fld.Type))
		}
	}
}

// argDocs renders a field's full argument list (deprecated args included,
// flagged), preserving SDL order.
func argDocs(f *ast.FieldDefinition) []ArgDoc {
	if len(f.Arguments) == 0 {
		return nil
	}
	out := make([]ArgDoc, 0, len(f.Arguments))
	for _, a := range f.Arguments {
		out = append(out, ArgDoc{
			Name:        a.Name,
			Type:        a.Type.String(),
			Required:    a.Type.NonNull && a.DefaultValue == nil,
			Default:     defaultString(a.DefaultValue),
			Deprecated:  argDeprecation(a),
			Description: a.Description,
		})
	}
	return out
}

// inputTypeDef renders an input object's fields in SDL order.
func inputTypeDef(_ *ast.Schema, def *ast.Definition) TypeDef {
	fields := make([]FieldDoc, 0, len(def.Fields))
	for _, fld := range def.Fields {
		fields = append(fields, FieldDoc{
			Name:        fld.Name,
			Type:        fld.Type.String(),
			Required:    fld.Type.NonNull && fld.DefaultValue == nil,
			Default:     defaultString(fld.DefaultValue),
			Deprecated:  fieldDeprecation(fld),
			Description: fld.Description,
		})
	}
	return TypeDef{Kind: "input", Description: def.Description, Fields: fields}
}

// enumTypeDef renders an enum's values in SDL order; Value is the wire symbol.
func enumTypeDef(def *ast.Definition) TypeDef {
	values := make([]EnumValueDoc, 0, len(def.EnumValues))
	for _, v := range def.EnumValues {
		values = append(values, EnumValueDoc{
			Value:       v.Name,
			Description: v.Description,
			Deprecated:  directiveReason(v.Directives),
		})
	}
	return TypeDef{Kind: "enum", Description: def.Description, Values: values}
}

// exitCodes derives a command's exit codes from the frozen taxonomy: the base
// six, then "not-found" for a nullable single-entity object lookup, then the
// destructive and job-returning extensions, in that fixed order.
func exitCodes(s *ast.Schema, f *ast.FieldDefinition, destructive, jobReturning bool) []string {
	codes := append([]string(nil), baseExitCodes...)
	if canMiss(s, f) {
		codes = append(codes, exitcode.NotFound.Name)
	}
	if destructive {
		codes = append(codes, exitcode.DestructiveRefused.Name)
	}
	if jobReturning {
		codes = append(codes, exitcode.JobFailed.Name, exitcode.StillRunning.Name, exitcode.Unconfirmed.Name)
	}
	return codes
}

// canMiss reports whether a field is a single-entity lookup that can return
// nothing: a query whose return type's base is an Object and whose return type
// is nullable (e.g. findScene(id): Scene). List returns and non-null returns
// cannot "miss" in the not-found sense.
func canMiss(s *ast.Schema, f *ast.FieldDefinition) bool {
	if f.Type.NonNull {
		return false
	}
	def := s.Types[BaseTypeName(f.Type)]
	if def == nil {
		return false
	}
	switch def.Kind {
	case ast.Object, ast.Interface, ast.Union:
		return true
	default:
		return false
	}
}

// defaultString renders a default value via ast.Value.String(), or "" if none.
func defaultString(v *ast.Value) string {
	if v == nil {
		return ""
	}
	return v.String()
}

// argDeprecation returns the @deprecated reason on an argument, or "".
func argDeprecation(a *ast.ArgumentDefinition) string {
	return directiveReason(a.Directives)
}

// fieldDeprecation returns the @deprecated reason on an input-object field, or "".
func fieldDeprecation(f *ast.FieldDefinition) string {
	return directiveReason(f.Directives)
}

// directiveReason returns the reason argument of a @deprecated directive in a
// list, or "" if the list carries no @deprecated.
func directiveReason(d ast.DirectiveList) string {
	dir := d.ForName("deprecated")
	if dir == nil {
		return ""
	}
	if arg := dir.Arguments.ForName("reason"); arg != nil && arg.Value != nil {
		return arg.Value.Raw
	}
	return ""
}
