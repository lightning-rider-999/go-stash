package conformance

import (
	"sync"
	"testing"

	"github.com/lightning-rider-999/go-stashapp/internal/genops"
	"github.com/lightning-rider-999/go-stashapp/schema"
	"github.com/vektah/gqlparser/v2/ast"
)

// Paths to the vendored SDL and curated overlay, relative to this package's
// directory (internal/conformance).
const (
	schemaDir   = "../../schema"
	overlayPath = "../../operations/overlay.yaml"
)

// fixture bundles the once-loaded schema, overlay, and compiled surface so each
// gate can reach for whichever artefact it needs without re-parsing the SDL.
type fixture struct {
	schema   *ast.Schema
	overlay  *genops.Overlay
	manifest *genops.Manifest
	catalog  *genops.Catalog
	compiled *genops.Compiled
}

var (
	loadOnce sync.Once
	loaded   *fixture
	loadErr  error
)

// load returns the shared fixture, parsing the schema and compiling the surface
// on first use. It fails the test (rather than returning a partial fixture) on
// any error, so every gate starts from a known-good baseline.
func load(t *testing.T) *fixture {
	t.Helper()
	loadOnce.Do(func() {
		f := &fixture{}
		f.schema, loadErr = genops.LoadSchema(schemaDir)
		if loadErr != nil {
			return
		}
		f.overlay, loadErr = genops.LoadOverlay(overlayPath)
		if loadErr != nil {
			return
		}
		f.compiled, loadErr = genops.Compile(schemaDir, overlayPath, schema.SchemaVersion)
		if loadErr != nil {
			return
		}
		f.manifest = f.compiled.Manifest
		f.catalog = f.compiled.Catalog
		loaded = f
	})
	if loadErr != nil {
		t.Fatalf("loading conformance fixture: %v", loadErr)
	}
	return loaded
}

// rootOps is the set of root operation kinds whose fields back the generated
// command surface.
var rootOps = []ast.Operation{ast.Query, ast.Mutation, ast.Subscription}
