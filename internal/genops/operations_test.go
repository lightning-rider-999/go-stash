package genops

import (
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
)

func buildOps(t *testing.T) (map[string]Operation, []Operation) {
	t.Helper()
	s, err := LoadSchema(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	fs := BuildFragments(s)
	ops, err := BuildOperations(s, fs)
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]Operation, len(ops))
	for _, o := range ops {
		if _, dup := byName[o.Name]; dup {
			t.Fatalf("duplicate operation name %q", o.Name)
		}
		byName[o.Name] = o
	}
	return byName, ops
}

func TestOperationsCount(t *testing.T) {
	byName, ops := buildOps(t)
	if len(ops) != 211 {
		t.Errorf("total operations = %d, want 211", len(ops))
	}
	var q, m, sub int
	for _, o := range ops {
		switch o.Op {
		case ast.Query:
			q++
		case ast.Mutation:
			m++
		case ast.Subscription:
			sub++
		}
	}
	if q != 74 || m != 134 || sub != 3 {
		t.Errorf("counts = %d/%d/%d, want 74/134/3", q, m, sub)
	}
	if byName["JobsSubscribe"].Op != ast.Subscription {
		t.Error("JobsSubscribe should be a subscription")
	}
}

func TestOperationIDsPreferred(t *testing.T) {
	byName, _ := buildOps(t)
	// A6: the ids:[ID!] form is generated; the deprecated [Int!] variant is not.
	for _, name := range []string{"FindScenes", "FindImages", "FindPerformers"} {
		op, ok := byName[name]
		if !ok {
			t.Fatalf("%s not generated", name)
		}
		if !strings.Contains(op.Text, "$ids: [ID!]") {
			t.Errorf("%s should declare $ids: [ID!]\n%s", name, op.Text)
		}
		if !strings.Contains(op.Text, "ids: $ids") {
			t.Errorf("%s should forward ids: $ids", name)
		}
		for _, dep := range []string{"scene_ids", "image_ids", "performer_ids"} {
			if strings.Contains(op.Text, dep) {
				t.Errorf("%s leaked deprecated argument %q", name, dep)
			}
		}
	}
}

func TestOperationEntityPayloadFlattened(t *testing.T) {
	byName, _ := buildOps(t)
	// Result-wrapper container expands the entity edge to the full fragment.
	fsq := byName["FindScenes"].Text
	if !strings.Contains(fsq, "  # @genqlient(flatten: true)\n    scenes {\n      ...SceneFields\n    }") {
		t.Errorf("FindScenes should flatten scenes -> SceneFields\n%s", fsq)
	}
}

func TestOperationAbstractTypename(t *testing.T) {
	byName, _ := buildOps(t)
	// A5: interface/union return selects __typename + a fragment per member.
	ff := byName["FindFile"].Text
	for _, want := range []string{"__typename", "... on VideoFile {", "...VideoFileFields", "... on ImageFile {"} {
		if !strings.Contains(ff, want) {
			t.Errorf("FindFile missing %q\n%s", want, ff)
		}
	}
}

func TestOperationScalarNoSelection(t *testing.T) {
	byName, _ := buildOps(t)
	sd := byName["SceneDestroy"].Text
	// Boolean return: the root field has no sub-selection block.
	if !strings.Contains(sd, "  sceneDestroy(input: $input)\n") {
		t.Errorf("SceneDestroy should have no selection block\n%s", sd)
	}
	if strings.Contains(sd, "sceneDestroy(input: $input) {") {
		t.Error("SceneDestroy must not open a selection block on a scalar return")
	}
}

func TestOperationsDeterministic(t *testing.T) {
	byA, opsA := buildOps(t)
	_, opsB := buildOps(t)
	if len(opsA) != len(opsB) {
		t.Fatalf("op counts differ: %d vs %d", len(opsA), len(opsB))
	}
	for i := range opsA {
		if opsA[i].Name != opsB[i].Name {
			t.Fatalf("op %d order differs: %q vs %q", i, opsA[i].Name, opsB[i].Name)
		}
		if opsA[i].Text != byA[opsB[i].Name].Text {
			t.Errorf("op %q not deterministic", opsA[i].Name)
		}
	}
}
