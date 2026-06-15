package genops

import (
	"bytes"
	"slices"
	"testing"
)

// overlayPath is the curated overlay YAML, relative to this package's directory.
const overlayPath = "../../operations/overlay.yaml"

// buildArtifacts loads the schema and overlay and builds both the manifest and
// catalog, failing the test on any error.
func buildArtifacts(t *testing.T) (*Manifest, *Catalog, *Overlay) {
	t.Helper()
	s, err := LoadSchema(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	ov, err := LoadOverlay(overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	man, err := BuildManifest(s, ov, "v0.31.1")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := BuildCatalog(s, ov, "v0.31.1")
	if err != nil {
		t.Fatal(err)
	}
	return man, cat, ov
}

func manifestByName(m *Manifest) map[string]ManifestEntry {
	out := make(map[string]ManifestEntry, len(m.Operations))
	for _, e := range m.Operations {
		out[e.Name] = e
	}
	return out
}

func TestManifestCatalog(t *testing.T) {
	man, cat, _ := buildArtifacts(t)

	t.Run("Manifest", func(t *testing.T) {
		if len(man.Operations) != 211 {
			t.Errorf("manifest operations = %d, want 211", len(man.Operations))
		}
		if !slices.IsSortedFunc(man.Operations, func(a, b ManifestEntry) int {
			if a.Name < b.Name {
				return -1
			}
			if a.Name > b.Name {
				return 1
			}
			return 0
		}) {
			t.Error("manifest operations are not sorted by Name")
		}

		by := manifestByName(man)

		if got := by["SceneCreate"]; got.InputType != "SceneCreateInput" || got.Kind != "mutation" {
			t.Errorf("SceneCreate = {InputType:%q Kind:%q}, want {SceneCreateInput mutation}", got.InputType, got.Kind)
		}
		if !by["MetadataScan"].JobReturning {
			t.Error("MetadataScan should be JobReturning")
		}
		if !by["QuerySQL"].Destructive {
			t.Error("QuerySQL should be Destructive")
		}
		if fm := by["FindMovie"]; !fm.Deprecated || fm.DeprecationReason == "" {
			t.Errorf("FindMovie should be Deprecated with a reason, got {Deprecated:%v Reason:%q}", fm.Deprecated, fm.DeprecationReason)
		}
	})

	t.Run("CatalogDefs", func(t *testing.T) {
		sft, ok := cat.Defs["SceneFilterType"]
		if !ok {
			t.Fatal("$defs missing SceneFilterType")
		}
		if sft.Kind != "input" {
			t.Errorf("SceneFilterType kind = %q, want input", sft.Kind)
		}
		for _, want := range []string{"AND", "OR", "NOT"} {
			fd := findField(sft.Fields, want)
			if fd == nil {
				t.Errorf("SceneFilterType missing field %q", want)
				continue
			}
			if fd.Type != "SceneFilterType" {
				t.Errorf("SceneFilterType.%s type = %q, want SceneFilterType", want, fd.Type)
			}
		}

		// An enum is present, with wire-symbol values.
		var foundEnum bool
		for _, name := range []string{"GenderEnum", "SortDirectionEnum"} {
			if def, ok := cat.Defs[name]; ok {
				foundEnum = true
				if def.Kind != "enum" {
					t.Errorf("%s kind = %q, want enum", name, def.Kind)
				}
				if len(def.Values) == 0 {
					t.Errorf("%s has no values", name)
				}
				for _, v := range def.Values {
					if v.Value == "" {
						t.Errorf("%s has an empty enum value symbol", name)
					}
				}
			}
		}
		if !foundEnum {
			t.Error("$defs contains neither GenderEnum nor SortDirectionEnum")
		}

		hmc, ok := cat.Defs["HierarchicalMultiCriterionInput"]
		if !ok {
			t.Fatal("$defs missing HierarchicalMultiCriterionInput")
		}
		if findField(hmc.Fields, "depth") == nil {
			t.Error("HierarchicalMultiCriterionInput missing depth field")
		}
	})

	t.Run("ExitCodes", func(t *testing.T) {
		qsql, ok := cat.Commands["QuerySQL"]
		if !ok {
			t.Fatal("commands missing QuerySQL")
		}
		if !slices.Contains(qsql.ExitCodes, "destructive-refused") {
			t.Errorf("QuerySQL exit codes %v missing destructive-refused", qsql.ExitCodes)
		}

		scan, ok := cat.Commands["MetadataScan"]
		if !ok {
			t.Fatal("commands missing MetadataScan")
		}
		for _, want := range []string{"still-running", "unconfirmed"} {
			if !slices.Contains(scan.ExitCodes, want) {
				t.Errorf("MetadataScan exit codes %v missing %q", scan.ExitCodes, want)
			}
		}

		// A plain query (no overlay flags, not a single-entity miss) has exactly
		// the six base codes. FindScenes returns a non-null FindScenesResultType.
		base, ok := cat.Commands["FindScenes"]
		if !ok {
			t.Fatal("commands missing FindScenes")
		}
		want := []string{"ok", "usage", "auth", "transport", "validation", "server-fault"}
		if !slices.Equal(base.ExitCodes, want) {
			t.Errorf("FindScenes exit codes = %v, want %v", base.ExitCodes, want)
		}
	})

	t.Run("Deterministic", func(t *testing.T) {
		mj1, err := man.JSON()
		if err != nil {
			t.Fatal(err)
		}
		mj2, err := man.JSON()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(mj1, mj2) {
			t.Error("manifest JSON is not deterministic")
		}
		if !bytes.HasSuffix(mj1, []byte("\n")) {
			t.Error("manifest JSON missing trailing newline")
		}

		cj1, err := cat.JSON()
		if err != nil {
			t.Fatal(err)
		}
		cj2, err := cat.JSON()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(cj1, cj2) {
			t.Error("catalog JSON is not deterministic")
		}
		if !bytes.HasSuffix(cj1, []byte("\n")) {
			t.Error("catalog JSON missing trailing newline")
		}
	})

	t.Run("OverlayValidates", func(t *testing.T) {
		s, err := LoadSchema(schemaDir)
		if err != nil {
			t.Fatal(err)
		}
		ov, err := LoadOverlay(overlayPath)
		if err != nil {
			t.Fatalf("LoadOverlay: %v", err)
		}
		if err := ov.Validate(s); err != nil {
			t.Errorf("overlay does not validate against schema: %v", err)
		}
	})
}

func findField(fields []FieldDoc, name string) *FieldDoc {
	for i := range fields {
		if fields[i].Name == name {
			return &fields[i]
		}
	}
	return nil
}
