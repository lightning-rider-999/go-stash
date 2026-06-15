package genops

import "strings"

// Compiled is the full output of a genops run: the genqlient operation and
// fragment source, plus the manifest and catalog, all derived from one schema
// load so they cannot drift from each other.
type Compiled struct {
	Fragments  string // every operation-reachable fragment, sorted by name
	Operations string // every root-field operation, sorted by name
	Manifest   *Manifest
	Catalog    *Catalog
}

// Compile loads the vendored SDL and curated overlay and produces the complete
// generated surface. Fragments are built lazily while rendering operations, so
// only fragments an operation actually spreads are emitted — genqlient rejects
// unused fragments, and the full type universe is far larger than the reachable
// set.
func Compile(schemaDir, overlayPath, schemaVersion string) (*Compiled, error) {
	s, err := LoadSchema(schemaDir)
	if err != nil {
		return nil, err
	}
	ov, err := LoadOverlay(overlayPath)
	if err != nil {
		return nil, err
	}
	if err := ov.Validate(s); err != nil {
		return nil, err
	}

	fs := newFragmentSet(s)
	ops, err := BuildOperations(s, fs)
	if err != nil {
		return nil, err
	}

	var frag strings.Builder
	for _, name := range fs.Names() {
		body, _ := fs.Fragment(name)
		frag.WriteString(body)
		frag.WriteByte('\n')
	}

	var oper strings.Builder
	for _, op := range ops {
		oper.WriteString(op.Text)
		oper.WriteByte('\n')
	}

	manifest, err := BuildManifest(s, ov, schemaVersion)
	if err != nil {
		return nil, err
	}
	catalog, err := BuildCatalog(s, ov, schemaVersion)
	if err != nil {
		return nil, err
	}

	return &Compiled{
		Fragments:  frag.String(),
		Operations: oper.String(),
		Manifest:   manifest,
		Catalog:    catalog,
	}, nil
}
