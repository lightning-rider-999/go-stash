package main

import (
	"bytes"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

// committedCatalogPaths are the two on-disk catalog copies the build keeps in
// lockstep: the source of truth the generator writes under schema/, and the copy
// beside the CLI that go:embed pulls in (go:embed cannot reach a `..` path). The
// check gate diffs both against a fresh `task generate`; this test pins their
// byte-identity directly so a hand-edit or a half-finished regeneration of just
// one of them fails inside `go test`.
const (
	schemaCatalogPath = "../../schema/catalog.json"
	cliCatalogPath    = "catalog.json"
)

// TestCLICatalogMatchesSchemaCatalog asserts cmd/stash/catalog.json is
// byte-identical to schema/catalog.json. The CLI embeds its local copy (see the
// embed directive in catalog.go) and serves it from `stash catalog`; if the two
// ever diverged, the binary would ship a catalog that disagrees with the
// canonical artefact the rest of the toolchain (docs, conformance gates) reads.
// Both files are read from disk by relative path and compared byte-for-byte.
func TestCLICatalogMatchesSchemaCatalog(t *testing.T) {
	schemaBytes, err := os.ReadFile(schemaCatalogPath)
	if err != nil {
		t.Fatalf("reading %s: %v", schemaCatalogPath, err)
	}
	cliBytes, err := os.ReadFile(cliCatalogPath)
	if err != nil {
		t.Fatalf("reading %s: %v", cliCatalogPath, err)
	}
	if !bytes.Equal(schemaBytes, cliBytes) {
		t.Errorf("cmd/stash/catalog.json is NOT byte-identical to schema/catalog.json "+
			"(%d vs %d bytes). They must be exact copies — regenerate with `task generate` "+
			"so the embedded CLI catalog matches the canonical source.",
			len(cliBytes), len(schemaBytes))
	}
}

// TestEmbeddedCatalogGatesDestroyers is the destructive-gate fail-closed check at
// the CLI's own embed boundary (the conf gate-2, mirrored here on the EMBEDDED
// bytes the binary actually ships). Every command in the embedded catalog whose
// field name marks it a destroyer — ends in Destroy, ends in Merge, or is
// deleteFiles/destroyFiles/destroySavedFilter — must carry "destructive": true.
// The destructive flag is what makes newLeafCommand attach the
// --yes-i-understand gate (see addDestructiveFlag), so an ungated destroyer here
// is a CLI that would wipe data without confirmation. Reading from the embed
// (not the source SDL) catches a drift introduced at the copy/embed step too.
func TestEmbeddedCatalogGatesDestroyers(t *testing.T) {
	var cat struct {
		Commands map[string]struct {
			Field       string `json:"field"`
			Kind        string `json:"kind"`
			Destructive bool   `json:"destructive"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(catalogJSON, &cat); err != nil {
		t.Fatalf("embedded catalog.json is not valid JSON: %v", err)
	}
	if len(cat.Commands) == 0 {
		t.Fatal("embedded catalog has no commands; the embed is empty or malformed")
	}

	var found, ungated []string
	for _, cmd := range cat.Commands {
		if cmd.Kind != "mutation" || !isEmbeddedDestroyer(cmd.Field) {
			continue
		}
		found = append(found, cmd.Field)
		if !cmd.Destructive {
			ungated = append(ungated, cmd.Field)
		}
	}
	sort.Strings(ungated)

	if len(found) == 0 {
		t.Fatal("no destroy/delete/merge-family mutation found in the embedded catalog; " +
			"the name-shape detector or the embed is broken")
	}
	if len(ungated) > 0 {
		t.Errorf("%d destroy/delete/merge-family mutation(s) in the EMBEDDED catalog are not "+
			"destructive: %v\nThe CLI would run these without the --%s confirmation gate. "+
			"Mark them destructive in operations/overlay.yaml and regenerate.",
			len(ungated), ungated, confirmFlag)
	}
}

// isEmbeddedDestroyer reports whether a mutation root-field name belongs to the
// destroy/delete/merge family by name shape. Kept independent of the conformance
// package's copy so this CLI-side gate stands alone.
func isEmbeddedDestroyer(field string) bool {
	switch field {
	case "deleteFiles", "destroyFiles", "destroySavedFilter":
		return true
	}
	return strings.HasSuffix(field, "Destroy") || strings.HasSuffix(field, "Merge")
}
