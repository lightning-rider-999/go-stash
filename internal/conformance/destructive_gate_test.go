package conformance

import (
	"slices"
	"strings"
	"testing"

	genops "github.com/trackness/graphql-opgen"
	"github.com/vektah/gqlparser/v2/ast"
)

// destroyFamilyExtras are the destroy/delete family members whose names do not
// end in the "Destroy" suffix and are not a "*Merge". They are the file/saved
// filter destroyers Stash spells differently (deleteFiles, destroyFiles,
// destroySavedFilter); each removes media or DB records with no undo, so each
// MUST be gated as destructive exactly like the suffix-matched destroyers.
var destroyFamilyExtras = map[string]bool{
	"deleteFiles":        true,
	"destroyFiles":       true,
	"destroySavedFilter": true,
}

// isDestroyFamily reports whether a mutation root-field name belongs to the
// destroy/delete/merge family — the set of operations that drop, delete, or
// merge-away data irreversibly. Membership is by NAME SHAPE, derived from the
// SDL field name alone, so a brand-new destroyer on a Stash upgrade is caught
// without anyone having to list it: any field that ends in "Destroy", ends in
// "Merge", or is one of the explicitly named file/saved-filter destroyers.
func isDestroyFamily(field string) bool {
	if destroyFamilyExtras[field] {
		return true
	}
	return strings.HasSuffix(field, "Destroy") || strings.HasSuffix(field, "Merge")
}

// TestDestroyFamilyIsGated is the destructive-gate fail-closed conformance gate
// (gate-2): every mutation root field whose NAME marks it a destroyer
// (ends in Destroy, ends in Merge, or is deleteFiles/destroyFiles/
// destroySavedFilter) MUST be flagged Destructive — both in the curated overlay
// and in the generated catalog derived from it. A future Stash upgrade that adds
// an ungated destroyer (say sceneFooDestroy) therefore turns THIS test red
// rather than shipping a CLI that wipes data without the --yes-i-understand
// confirmation gate.
//
// The family is identified purely from the SDL field names, so no hand-kept list
// can fall out of date: the test discovers the destroyers itself and then checks
// each one is gated. It is the inverse of the overlay's own Validate (which only
// rejects names that are not real root fields); here we assert that a real
// destroyer cannot be SILENTLY OMITTED from the destructive list.
func TestDestroyFamilyIsGated(t *testing.T) {
	f := load(t)

	destructive := make(map[string]bool, len(f.overlay.Destructive))
	for _, name := range f.overlay.Destructive {
		destructive[name] = true
	}

	// Catalog commands are keyed by exported operation name; index the
	// generated catalog's Destructive flag by root-field name so the assertion
	// can cross-check the overlay against the artefact the CLI actually embeds.
	catalogDestructiveByField := make(map[string]bool, len(f.catalog.Commands))
	for _, cmd := range f.catalog.Commands {
		catalogDestructiveByField[cmd.Field] = cmd.Destructive
	}

	var family []string
	for _, fd := range genops.RootFields(f.schema, ast.Mutation) {
		if isDestroyFamily(fd.Name) {
			family = append(family, fd.Name)
		}
	}
	slices.Sort(family)

	if len(family) == 0 {
		t.Fatal("no destroy/delete/merge-family mutation root fields were discovered in the SDL; " +
			"the name-shape detector is broken (it must find sceneDestroy, tagsMerge, deleteFiles, etc.)")
	}

	var ungatedOverlay, ungatedCatalog []string
	for _, name := range family {
		if !destructive[name] {
			ungatedOverlay = append(ungatedOverlay, name)
		}
		if !catalogDestructiveByField[name] {
			ungatedCatalog = append(ungatedCatalog, name)
		}
	}

	if len(ungatedOverlay) > 0 {
		t.Errorf("%d destroy/delete/merge-family mutation(s) are NOT marked destructive in operations/overlay.yaml: %v\n"+
			"Each can drop, delete, or merge-away data with no undo and MUST be added to the overlay's "+
			"`destructive:` list so the CLI demands --yes-i-understand before running it.",
			len(ungatedOverlay), ungatedOverlay)
	}
	if len(ungatedCatalog) > 0 {
		t.Errorf("%d destroy/delete/merge-family mutation(s) are NOT Destructive in the generated catalog: %v\n"+
			"The overlay drives the catalog; if the overlay is fixed and these still show, regenerate.",
			len(ungatedCatalog), ungatedCatalog)
	}
}
