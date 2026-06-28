package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lightning-rider-999/go-stash/stash"
)

// leafFor finds the built leaf command for a spec's path in a fresh root tree,
// so a test drives the same flags and convenience wiring the binary uses.
func leafFor(t *testing.T, path ...string) *cobra.Command {
	t.Helper()
	root := buildRootCommand()
	cur := root
	for _, seg := range path {
		next, _, err := cur.Find([]string{seg})
		if err != nil || next == cur {
			t.Fatalf("command %q not found under %q", seg, cur.Name())
		}
		cur = next
	}
	return cur
}

// parseLeaf parses argv onto a leaf, merging the root's persistent flags
// (--input, --output) the same way cobra does during a real invocation. Pass
// stdin for an --input - case.
func parseLeaf(t *testing.T, leaf *cobra.Command, stdin string, args ...string) {
	t.Helper()
	if stdin != "" {
		leaf.SetIn(strings.NewReader(stdin))
	}
	if err := leaf.ParseFlags(args); err != nil {
		t.Fatalf("parse flags %v: %v", args, err)
	}
}

// TestPartialUpdate is the three-state golden: a sceneUpdate input with a
// present-null field (title), a present field (organized), and an absent field
// (details) must reach the wire with title as JSON null, organized true, and
// details simply absent — proving the CLI never round-trips mutation input
// through a typed Go struct that would erase the null/absent distinction.
func TestPartialUpdate(t *testing.T) {
	// The user's variables object, exactly as --input would supply it.
	const inputJSON = `{"input":{"id":"42","title":null,"organized":true}}`

	fs := newFakeServer(t, `{"data":{"sceneUpdate":{"id":"42"}}}`)
	c := fs.client(t)

	spec := commandSpec{
		Path:      []string{"scene", "update"},
		OpName:    "SceneUpdate",
		Query:     stash.SceneUpdate_Operation,
		Kind:      "mutation",
		InputType: "SceneUpdateInput",
	}

	// Resolve through the real flag path: --input - reads the object from stdin.
	leaf := leafFor(t, "scene", "update")
	parseLeaf(t, leaf, inputJSON, "--input", "-")

	vars, err := resolveVariables(leaf, spec)
	if err != nil {
		t.Fatalf("resolveVariables: %v", err)
	}

	// Execute so the genqlient request is built and marshalled onto the wire.
	var out bytes.Buffer
	if err := runOperation(context.Background(), c, spec, vars, "json", &out, false); err != nil {
		t.Fatalf("runOperation: %v", err)
	}

	// Pull the on-wire variables object back out of the request body.
	var body struct {
		Variables map[string]json.RawMessage `json:"variables"`
	}
	if err := json.Unmarshal(fs.lastBody, &body); err != nil {
		t.Fatalf("request body not JSON: %v\n%s", err, fs.lastBody)
	}
	rawInput, ok := body.Variables["input"]
	if !ok {
		t.Fatalf("wire variables missing input key: %s", fs.lastBody)
	}

	// Decode the input object preserving raw values, then assert the three-state
	// byte-for-byte: title is literal null, organized is true, details absent.
	var input map[string]json.RawMessage
	if err := json.Unmarshal(rawInput, &input); err != nil {
		t.Fatalf("input not an object: %v", err)
	}

	title, ok := input["title"]
	if !ok {
		t.Errorf("title missing from wire input; want present as null")
	} else if string(title) != "null" {
		t.Errorf("title on wire = %s, want null", title)
	}

	organized, ok := input["organized"]
	if !ok || string(organized) != "true" {
		t.Errorf("organized on wire = %s (present=%v), want true", organized, ok)
	}

	if _, present := input["details"]; present {
		t.Errorf("details present on wire, want absent")
	}

	// The id round-trips as the quoted string it was given.
	if got := string(input["id"]); got != `"42"` {
		t.Errorf("id on wire = %s, want \"42\"", got)
	}
}

// TestConvenienceFlagNeverInjectsMutationInput proves a mutation leaf has no
// convenience flags at all, so none can become a mutation input key, and that
// resolveVariables on a mutation passes the --input variables through untouched.
func TestConvenienceFlagNeverInjectsMutationInput(t *testing.T) {
	leaf := leafFor(t, "scene", "update")

	for _, name := range []string{"id", "query", "page", "per-page", "sort", "direction"} {
		if leaf.Flags().Lookup(name) != nil {
			t.Errorf("mutation leaf has convenience flag --%s; it must not", name)
		}
	}

	spec := commandSpec{
		Path:      []string{"scene", "update"},
		OpName:    "SceneUpdate",
		Query:     stash.SceneUpdate_Operation,
		Kind:      "mutation",
		InputType: "SceneUpdateInput",
	}
	parseLeaf(t, leaf, `{"input":{"id":"7"}}`, "--input", "-")

	vars, err := resolveVariables(leaf, spec)
	if err != nil {
		t.Fatalf("resolveVariables: %v", err)
	}
	if len(vars) != 1 {
		t.Fatalf("vars = %v, want only the input key", vars)
	}
	if _, ok := vars["input"]; !ok {
		t.Errorf("vars missing input key: %v", vars)
	}
}

// TestConvenienceIDSetsList checks --id on a list query (FindScenes declares
// ids: [ID!]) becomes ids: ["<id>"], and that an explicit --input ids wins.
func TestConvenienceIDSetsList(t *testing.T) {
	spec := commandSpec{
		Path:       []string{"scene", "list"},
		OpName:     "FindScenes",
		Query:      stash.FindScenes_Operation,
		Kind:       "query",
		ReturnType: "FindScenesResultType",
	}

	t.Run("flag sets ids list", func(t *testing.T) {
		leaf := leafFor(t, "scene", "list")
		if leaf.Flags().Lookup("id") == nil {
			t.Fatal("FindScenes leaf is missing the --id convenience flag")
		}
		parseLeaf(t, leaf, "", "--id", "99")
		vars, err := resolveVariables(leaf, spec)
		if err != nil {
			t.Fatalf("resolveVariables: %v", err)
		}
		if got := string(vars["ids"]); got != `["99"]` {
			t.Errorf("ids = %s, want [\"99\"]", got)
		}
	})

	t.Run("input ids wins over flag", func(t *testing.T) {
		leaf := leafFor(t, "scene", "list")
		parseLeaf(t, leaf, `{"ids":["1","2"]}`, "--input", "-", "--id", "99")
		vars, err := resolveVariables(leaf, spec)
		if err != nil {
			t.Fatalf("resolveVariables: %v", err)
		}
		if got := string(vars["ids"]); got != `["1","2"]` {
			t.Errorf("ids = %s, want the --input value to win", got)
		}
	})
}

// TestConvenienceFilterFlags checks the FindFilterType shorthand merges into a
// filter object with the right JSON types and that --input filter fields win.
func TestConvenienceFilterFlags(t *testing.T) {
	spec := commandSpec{
		Path:       []string{"scene", "list"},
		OpName:     "FindScenes",
		Query:      stash.FindScenes_Operation,
		Kind:       "query",
		ReturnType: "FindScenesResultType",
	}

	t.Run("flags build a filter object", func(t *testing.T) {
		leaf := leafFor(t, "scene", "list")
		parseLeaf(t, leaf, "", "--per-page", "10", "--query", "anal", "--direction", "DESC")

		vars, err := resolveVariables(leaf, spec)
		if err != nil {
			t.Fatalf("resolveVariables: %v", err)
		}
		var filter map[string]json.RawMessage
		if err := json.Unmarshal(vars["filter"], &filter); err != nil {
			t.Fatalf("filter not an object: %v (%s)", err, vars["filter"])
		}
		if got := string(filter["per_page"]); got != "10" {
			t.Errorf("per_page = %s, want number 10", got)
		}
		if got := string(filter["q"]); got != `"anal"` {
			t.Errorf("q = %s, want \"anal\"", got)
		}
		if got := string(filter["direction"]); got != `"DESC"` {
			t.Errorf("direction = %s, want \"DESC\"", got)
		}
	})

	t.Run("input filter field wins, others merge", func(t *testing.T) {
		leaf := leafFor(t, "scene", "list")
		parseLeaf(t, leaf, `{"filter":{"per_page":5}}`, "--input", "-", "--per-page", "10", "--sort", "date")

		vars, err := resolveVariables(leaf, spec)
		if err != nil {
			t.Fatalf("resolveVariables: %v", err)
		}
		var filter map[string]json.RawMessage
		if err := json.Unmarshal(vars["filter"], &filter); err != nil {
			t.Fatalf("filter not an object: %v", err)
		}
		if got := string(filter["per_page"]); got != "5" {
			t.Errorf("per_page = %s, want the --input value 5", got)
		}
		if got := string(filter["sort"]); got != `"date"` {
			t.Errorf("sort = %s, want \"date\" merged from the flag", got)
		}
	})

	t.Run("invalid direction is rejected", func(t *testing.T) {
		leaf := leafFor(t, "scene", "list")
		parseLeaf(t, leaf, "", "--direction", "sideways")
		if _, err := resolveVariables(leaf, spec); err == nil {
			t.Fatal("expected an error for an invalid --direction")
		}
	})

	t.Run("non-integer per-page is rejected", func(t *testing.T) {
		leaf := leafFor(t, "scene", "list")
		parseLeaf(t, leaf, "", "--per-page", "lots")
		if _, err := resolveVariables(leaf, spec); err == nil {
			t.Fatal("expected an error for a non-integer --per-page")
		}
	})
}

// listSpecForExit is a FindScenes (filter + ids) query spec reused by the
// usage-classification tests below.
var listSpecForExit = commandSpec{
	Path:       []string{"scene", "list"},
	OpName:     "FindScenes",
	Query:      stash.FindScenes_Operation,
	Kind:       "query",
	ReturnType: "FindScenesResultType",
}

// TestInputErrorsClassifyAsUsage proves every user-facing input-validation
// failure resolveVariables can return is tagged as a usage error, so the runtime
// exits 2 (usage) rather than 1 (internal). Each case drives the real flag path
// and then runs the returned error through classifyExit.
func TestInputErrorsClassifyAsUsage(t *testing.T) {
	wantUsage := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if got := classifyExit(err); got != ExitUsage {
			t.Errorf("classifyExit(%v) = %+v, want ExitUsage (%+v)", err, got, ExitUsage)
		}
	}

	t.Run("bad --input path", func(t *testing.T) {
		leaf := leafFor(t, "scene", "list")
		// A path that cannot be read: os.ReadFile fails.
		parseLeaf(t, leaf, "", "--input", "/no/such/file/here.json")
		_, err := resolveVariables(leaf, listSpecForExit)
		wantUsage(t, err)
	})

	t.Run("malformed --input JSON", func(t *testing.T) {
		leaf := leafFor(t, "scene", "list")
		parseLeaf(t, leaf, "{not json", "--input", "-")
		_, err := resolveVariables(leaf, listSpecForExit)
		wantUsage(t, err)
	})

	t.Run("malformed --input filter object", func(t *testing.T) {
		leaf := leafFor(t, "scene", "list")
		// filter present but not an object: applyFilterFlags rejects it once a
		// filter flag is also set so the merge path runs.
		parseLeaf(t, leaf, `{"filter":42}`, "--input", "-", "--per-page", "10")
		_, err := resolveVariables(leaf, listSpecForExit)
		wantUsage(t, err)
	})

	t.Run("unaccepted --id", func(t *testing.T) {
		// applyIDFlag's default branch fires when --id is set but the operation
		// declares neither ids nor id. Drive it directly with an args set that
		// has neither, and a changed --id flag.
		cmd := &cobra.Command{Use: "x"}
		cmd.Flags().String("id", "", "")
		if err := cmd.Flags().Set("id", "7"); err != nil {
			t.Fatalf("set --id: %v", err)
		}
		err := applyIDFlag(cmd, map[string]bool{}, map[string]json.RawMessage{})
		wantUsage(t, err)
	})

	t.Run("invalid --direction", func(t *testing.T) {
		leaf := leafFor(t, "scene", "list")
		parseLeaf(t, leaf, "", "--direction", "sideways")
		_, err := resolveVariables(leaf, listSpecForExit)
		wantUsage(t, err)
	})

	t.Run("non-integer --per-page", func(t *testing.T) {
		leaf := leafFor(t, "scene", "list")
		parseLeaf(t, leaf, "", "--per-page", "lots")
		_, err := resolveVariables(leaf, listSpecForExit)
		wantUsage(t, err)
	})
}

// TestConvenienceSingularID checks an op declaring a scalar id (findScene) gets
// --id wired as the id variable, not an ids list.
func TestConvenienceSingularID(t *testing.T) {
	spec := commandSpec{
		Path:       []string{"scene", "get"},
		OpName:     "FindScene",
		Query:      stash.FindScene_Operation,
		Kind:       "query",
		ReturnType: "Scene",
	}
	leaf := leafFor(t, "scene", "get")
	if leaf.Flags().Lookup("id") == nil {
		t.Fatal("FindScene leaf is missing --id")
	}
	// FindScene has no filter argument, so no filter flags should exist.
	if leaf.Flags().Lookup("per-page") != nil {
		t.Error("FindScene must not offer --per-page; it declares no filter arg")
	}
	parseLeaf(t, leaf, "", "--id", "42")
	vars, err := resolveVariables(leaf, spec)
	if err != nil {
		t.Fatalf("resolveVariables: %v", err)
	}
	if got := string(vars["id"]); got != `"42"` {
		t.Errorf("id = %s, want \"42\"", got)
	}
	if _, ok := vars["ids"]; ok {
		t.Error("ids should be absent for a scalar-id op")
	}
}
