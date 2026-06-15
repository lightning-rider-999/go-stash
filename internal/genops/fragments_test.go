package genops

import (
	"strings"
	"testing"
)

func TestRefFragmentShape(t *testing.T) {
	s, err := LoadSchema(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	fs := BuildFragments(s)

	// B1: refs are {id, name|title} — never id-only.
	cases := map[string]string{
		"StudioRef":    "fragment StudioRef on Studio {\n  id\n  name\n}\n",
		"TagRef":       "fragment TagRef on Tag {\n  id\n  name\n}\n",
		"PerformerRef": "fragment PerformerRef on Performer {\n  id\n  name\n}\n",
		"SceneRef":     "fragment SceneRef on Scene {\n  id\n  title\n}\n", // title-keyed
		"GalleryRef":   "fragment GalleryRef on Gallery {\n  id\n  title\n}\n",
	}
	for name, want := range cases {
		got, ok := fs.Fragment(name)
		if !ok {
			t.Errorf("%s not emitted", name)
			continue
		}
		if got != want {
			t.Errorf("%s =\n%q\nwant\n%q", name, got, want)
		}
	}
}

func TestSceneFieldsFlattenAndCanonicalTargets(t *testing.T) {
	s, err := LoadSchema(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	fs := BuildFragments(s)
	body, ok := fs.Fragment("SceneFields")
	if !ok {
		t.Fatal("SceneFields not emitted")
	}

	// A1: every nested single-fragment-spread field carries the flatten
	// directive on the line directly above it; the spread resolves to the
	// canonical type named in the Task 4 acceptance criteria.
	wantBlocks := map[string]string{
		"studio":     "  # @genqlient(flatten: true)\n  studio {\n    ...StudioRef\n  }\n",
		"tags":       "  # @genqlient(flatten: true)\n  tags {\n    ...TagRef\n  }\n",
		"performers": "  # @genqlient(flatten: true)\n  performers {\n    ...PerformerRef\n  }\n",
		"files":      "  # @genqlient(flatten: true)\n  files {\n    ...VideoFileFields\n  }\n",
	}
	for field, block := range wantBlocks {
		if !strings.Contains(body, block) {
			t.Errorf("SceneFields missing flattened %s block:\n%s\n--- full body ---\n%s", field, block, body)
		}
	}

	// A1 (negative): the flatten directive must never sit on the line before the
	// fragment keyword.
	if strings.Contains(body, flattenDirective+"\nfragment") {
		t.Error("flatten directive precedes a fragment keyword")
	}

	// The parameterized VideoFile.fingerprint(type: String!) accessor must be
	// omitted (required argument cannot be supplied in a selection).
	vff, _ := fs.Fragment("VideoFileFields")
	for _, line := range strings.Split(vff, "\n") {
		if strings.TrimSpace(line) == "fingerprint" {
			t.Error("VideoFileFields selected fingerprint (has a required arg)")
		}
	}
	if !strings.Contains(vff, "...FingerprintFields") {
		t.Error("VideoFileFields should still expand the fingerprints list")
	}
}

func TestIsRefable(t *testing.T) {
	s, err := LoadSchema(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	refable := []string{"Scene", "Studio", "Tag", "Performer", "Group", "Gallery", "Image", "SceneMarker"}
	for _, name := range refable {
		if !IsRefable(s.Types[name]) {
			t.Errorf("%s should be ref-able", name)
		}
	}
	notRefable := []string{"VideoFile", "StashID", "Folder", "Fingerprint", "SceneGroup", "ScenePathsType"}
	for _, name := range notRefable {
		if d := s.Types[name]; d != nil && IsRefable(d) {
			t.Errorf("%s should not be ref-able", name)
		}
	}
}
