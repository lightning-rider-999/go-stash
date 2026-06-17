package main

import (
	"encoding/json"
	"testing"

	"github.com/lightning-rider-999/go-stash/stash"
)

// TestBindingThreeStateConformance is the conf-2 conformance gate, living in
// package main because internal/conformance cannot import it. It exercises the
// CLI's REAL variable-binding seam — resolveVariables (which reads --input) feeding
// graphqlVars (which shapes the genqlient request Variables) — and asserts that
// all three field states a partial-update mutation depends on survive end to end:
//
//	present-with-value : organized -> true
//	present-with-null  : title     -> null   (clear the field)
//	absent             : details   -> not on the wire (leave unchanged)
//
// This is the property a typed Go struct could NOT preserve: a struct collapses
// present-null and absent into the same zero value, silently wiping data on a
// partial update. Because the CLI carries variables as map[string]json.RawMessage
// from --input straight through graphqlVars, the distinction is preserved. The
// earlier scalars/partialupdate conformance tests proved the stdlib supports this
// shape; THIS test proves the CLI's own seam actually uses it.
func TestBindingThreeStateConformance(t *testing.T) {
	const inputJSON = `{"input":{"id":"42","title":null,"organized":true}}`

	spec := commandSpec{
		Path:      []string{"scene", "update"},
		OpName:    "SceneUpdate",
		Query:     stash.SceneUpdate_Operation,
		Kind:      "mutation",
		InputType: "SceneUpdateInput",
	}

	// Drive the real flag path: --input - reads the variables object from stdin,
	// exactly as a `stash scene update --input -` invocation would.
	leaf := leafFor(t, "scene", "update")
	parseLeaf(t, leaf, inputJSON, "--input", "-")

	vars, err := resolveVariables(leaf, spec)
	if err != nil {
		t.Fatalf("resolveVariables: %v", err)
	}

	// graphqlVars is the seam that hands the variables to genqlient. Marshalling
	// its result is exactly what the transport does when building the request
	// body, so the bytes here are the bytes that reach the server.
	wire, err := json.Marshal(graphqlVars(vars))
	if err != nil {
		t.Fatalf("marshal graphqlVars output: %v", err)
	}

	// Decode the on-seam variables, then the nested input object, keeping every
	// value raw so the three states stay byte-exact.
	var outerVars map[string]json.RawMessage
	if err := json.Unmarshal(wire, &outerVars); err != nil {
		t.Fatalf("graphqlVars output is not a JSON object: %v\n%s", err, wire)
	}
	rawInput, ok := outerVars["input"]
	if !ok {
		t.Fatalf("seam variables missing the input key: %s", wire)
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(rawInput, &input); err != nil {
		t.Fatalf("input is not a JSON object: %v", err)
	}

	// present-with-value: organized stays true.
	if got, ok := input["organized"]; !ok || string(got) != "true" {
		t.Errorf("organized = %s (present=%v), want present true", got, ok)
	}

	// present-with-null: title MUST be present and the literal JSON null. If it
	// collapsed to absent, a clear-the-title request would silently become a
	// no-op (data not cleared); if it round-tripped through a typed struct it
	// would vanish entirely.
	if got, ok := input["title"]; !ok {
		t.Error("title is absent at the binding seam; a present-null collapsed to absent (clear would no-op)")
	} else if string(got) != "null" {
		t.Errorf("title = %s, want the literal null", got)
	}

	// absent: details was never supplied and MUST NOT materialise, or it would
	// send an unintended (zero-valued) update for that field.
	if got, present := input["details"]; present {
		t.Errorf("details = %s appeared at the binding seam; an absent field must stay absent", got)
	}

	// The present-with-value id round-trips as the quoted string it was given.
	if got := string(input["id"]); got != `"42"` {
		t.Errorf("id = %s, want \"42\"", got)
	}
}

// TestGraphqlVarsEmptyIsObject guards the empty-variables shape graphqlVars
// produces: an operation with no --input and no flags must send an empty JSON
// object {}, not null. A null Variables would marshal to "variables":null, which
// some servers reject; an empty object is the safe, declared-no-variables shape.
func TestGraphqlVarsEmptyIsObject(t *testing.T) {
	wire, err := json.Marshal(graphqlVars(map[string]json.RawMessage{}))
	if err != nil {
		t.Fatalf("marshal empty graphqlVars: %v", err)
	}
	if string(wire) != "{}" {
		t.Errorf("graphqlVars(empty) = %s, want {}", wire)
	}
}
