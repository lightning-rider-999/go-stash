package conformance

import (
	"bytes"
	"testing"

	"github.com/lightning-rider-999/go-stashapp/internal/genops"
	"github.com/lightning-rider-999/go-stashapp/schema"
)

// TestDeterminism is gate 12: compiling the surface twice must yield
// byte-identical artefacts. Fragments, operations, the manifest JSON, and the
// catalog JSON are all checked into the repo; if generation were non-deterministic
// (map iteration order leaking into output, say), the committed files would churn
// and the freshness gate in `task check` would become a coin flip. This pins
// determinism at the source.
func TestDeterminism(t *testing.T) {
	first, err := genops.Compile(schemaDir, overlayPath, schema.SchemaVersion)
	if err != nil {
		t.Fatalf("first compile: %v", err)
	}
	second, err := genops.Compile(schemaDir, overlayPath, schema.SchemaVersion)
	if err != nil {
		t.Fatalf("second compile: %v", err)
	}

	if first.Fragments != second.Fragments {
		t.Error("Fragments differ between two compiles")
	}
	if first.Operations != second.Operations {
		t.Error("Operations differ between two compiles")
	}

	firstManifestB, err := first.Manifest.JSON()
	if err != nil {
		t.Fatalf("first manifest JSON: %v", err)
	}
	secondManifestB, err := second.Manifest.JSON()
	if err != nil {
		t.Fatalf("second manifest JSON: %v", err)
	}
	if !bytes.Equal(firstManifestB, secondManifestB) {
		t.Error("Manifest JSON differs between two compiles")
	}

	firstCatalogB, err := first.Catalog.JSON()
	if err != nil {
		t.Fatalf("first catalog JSON: %v", err)
	}
	secondCatalogB, err := second.Catalog.JSON()
	if err != nil {
		t.Fatalf("second catalog JSON: %v", err)
	}
	if !bytes.Equal(firstCatalogB, secondCatalogB) {
		t.Error("Catalog JSON differs between two compiles")
	}
}
