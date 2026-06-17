package conformance

import (
	"os"
	"testing"
)

// TestSeededInstanceRevalidation is gate 13 (B6): the cyclic Gallery / Image /
// Group / GroupDescription branches of the schema are validated by shape only
// (the fragment generator terminates those value cycles scalars-only to keep the
// fragment DAG acyclic). Confirming the live server actually returns those
// branches the way the generated operations expect requires a SEEDED Stash
// instance, which is not available in CI.
//
// Rather than pass silently — which would falsely imply the live round-trip was
// checked — this test SKIPS with an explicit message unless the environment
// advertises a seeded instance via STASH_SEEDED=1 plus STASHAPP_URL and
// STASHAPP_API_KEY. When those are present, it fails loudly as NOT IMPLEMENTED,
// because wiring the live re-validation is follow-up work that must not be
// mistaken for "already covered".
func TestSeededInstanceRevalidation(t *testing.T) {
	if os.Getenv("STASH_SEEDED") != "1" {
		t.Skip("SKIP: seeded-instance re-validation (B6) requires a seeded Stash instance. " +
			"The cyclic Gallery/Image/Group/GroupDescription branches are validated by schema-shape only " +
			"(see gate 7 and genops' pathNamedAllowlist value-cycle entries) until one is available. " +
			"Set STASH_SEEDED=1 plus STASHAPP_URL and STASHAPP_API_KEY to enable.")
	}

	url := os.Getenv("STASHAPP_URL")
	key := os.Getenv("STASHAPP_API_KEY")
	if url == "" || key == "" {
		t.Skip("SKIP: STASH_SEEDED=1 set but STASHAPP_URL/STASHAPP_API_KEY are missing; " +
			"cannot reach the seeded instance.")
	}

	// A seeded instance is advertised but the live re-validation harness is not
	// yet built. Fail explicitly so the gap is visible rather than papered over.
	t.Fatalf("seeded instance advertised (STASHAPP_URL=%s) but live re-validation of the cyclic "+
		"Gallery/Image/Group/GroupDescription branches is NOT YET IMPLEMENTED — implement the "+
		"round-trip against the seeded instance here.", url)
}
