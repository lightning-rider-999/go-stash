package genops

import (
	"strings"
	"testing"
)

func TestCycleNoFragmentCycles(t *testing.T) {
	s, err := LoadSchema(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	fs := BuildFragments(s)
	if cycles := FragmentCycles(fs); len(cycles) != 0 {
		t.Errorf("fragment spread graph has cycles: %v", cycles)
	}
}

func TestCycleEntityTermination(t *testing.T) {
	s, err := LoadSchema(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	fs := BuildFragments(s)

	// The Performer <-> Scene mutual cycle must terminate via Refs: each side
	// spreads the other's Ref leaf, never the other's full Fields fragment.
	scene, _ := fs.Fragment("SceneFields")
	if !strings.Contains(scene, "...PerformerRef") {
		t.Error("SceneFields.performers should terminate at PerformerRef")
	}
	if strings.Contains(scene, "...PerformerFields") {
		t.Error("SceneFields must not expand the full PerformerFields (B2)")
	}
	perf, ok := fs.Fragment("PerformerFields")
	if !ok {
		t.Fatal("PerformerFields not emitted")
	}
	if !strings.Contains(perf, "...SceneRef") {
		t.Error("PerformerFields.scenes should terminate at SceneRef")
	}
	if strings.Contains(perf, "...SceneFields") {
		t.Error("PerformerFields must not expand the full SceneFields (cycle)")
	}

	// A Ref is a leaf: {id, display} only, no nested spreads.
	studioRef, _ := fs.Fragment("StudioRef")
	if strings.Contains(studioRef, "...") {
		t.Errorf("StudioRef must be a leaf with no spreads:\n%s", studioRef)
	}
}

func TestCycleMixedWrapper(t *testing.T) {
	s, err := LoadSchema(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	fs := BuildFragments(s)

	// SceneGroup is a mixed wrapper: inlined (path-named) with its scalar
	// scene_index and the inner group flattened to a Ref.
	if !IsPathNamedAllowed("SceneGroup") {
		t.Error("SceneGroup must be in the path-named allowlist")
	}
	scene, _ := fs.Fragment("SceneFields")
	wantWrapper := "  groups {\n" +
		"    # @genqlient(flatten: true)\n" +
		"    group {\n" +
		"      ...GroupRef\n" +
		"    }\n" +
		"    scene_index\n" +
		"  }\n"
	if !strings.Contains(scene, wantWrapper) {
		t.Errorf("SceneFields missing inlined SceneGroup wrapper:\n%s\n--- body ---\n%s", wantWrapper, scene)
	}
}

func TestCycleUnion(t *testing.T) {
	s, err := LoadSchema(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	fs := BuildFragments(s)

	if !IsPathNamedAllowed("VisualFile") {
		t.Error("VisualFile must be in the path-named allowlist")
	}
	img, ok := fs.Fragment("ImageFields")
	if !ok {
		t.Fatal("ImageFields not emitted")
	}
	// A5: union selection carries __typename plus one inline fragment per member.
	for _, want := range []string{
		"visual_files {",
		"__typename",
		"... on VideoFile {",
		"...VideoFileFields",
		"... on ImageFile {",
		"...ImageFileFields",
	} {
		if !strings.Contains(img, want) {
			t.Errorf("ImageFields union selection missing %q:\n%s", want, img)
		}
	}
}

func TestCycleAllowlistComplete(t *testing.T) {
	s, err := LoadSchema(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	fs := BuildFragments(s)
	if unlisted := UnlistedPathNamed(fs); len(unlisted) != 0 {
		t.Errorf("path-named types not in the audited allowlist (drift): %v", unlisted)
	}
}
