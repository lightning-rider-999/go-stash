package conformance

import (
	"encoding/json"
	"testing"
)

// TestPartialUpdateThreeState is gate 6: a partial-update mutation input must
// preserve all three field states through the CLI's raw-JSON binding —
// present-with-value, present-with-null, and absent. The CLI binds mutation
// inputs as map[string]json.RawMessage precisely so that "set this field to
// null" (present null) stays distinct from "leave this field unchanged"
// (absent). Collapsing the two would silently wipe data on a partial update, so
// this property is locked at the conformance layer.
func TestPartialUpdateThreeState(t *testing.T) {
	// title is present and null (clear it); organized is present and true (set
	// it); details is absent (leave it unchanged).
	const raw = `{"id":"1","title":null,"organized":true}`

	var binding map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &binding); err != nil {
		t.Fatalf("unmarshal into map[string]json.RawMessage: %v", err)
	}

	// id: present with a value.
	idVal, ok := binding["id"]
	if !ok {
		t.Fatal("id is absent; present field was dropped")
	}
	if string(idVal) != `"1"` {
		t.Errorf("id raw value = %s, want \"1\"", idVal)
	}

	// title: present, and explicitly null — the key MUST exist and decode to the
	// JSON null literal.
	titleVal, ok := binding["title"]
	if !ok {
		t.Fatal("title is absent; a present-null field collapsed to absent (data-wiping bug)")
	}
	if string(titleVal) != "null" {
		t.Errorf("title raw value = %s, want null", titleVal)
	}

	// organized: present with a non-null value.
	orgVal, ok := binding["organized"]
	if !ok {
		t.Fatal("organized is absent; present field was dropped")
	}
	if string(orgVal) != "true" {
		t.Errorf("organized raw value = %s, want true", orgVal)
	}

	// details: absent — the key MUST NOT exist.
	if _, ok := binding["details"]; ok {
		t.Error("details is present; an omitted field materialised (would send an unintended update)")
	}

	// The three states must remain distinguishable when re-marshalled: the
	// re-encoded payload must still carry title:null and must still omit details.
	out, err := json.Marshal(binding)
	if err != nil {
		t.Fatalf("re-marshal binding: %v", err)
	}
	var reparsed map[string]json.RawMessage
	if err := json.Unmarshal(out, &reparsed); err != nil {
		t.Fatalf("re-unmarshal binding: %v", err)
	}
	if v, ok := reparsed["title"]; !ok || string(v) != "null" {
		t.Errorf("after re-marshal, title = %s present=%v, want present null", v, ok)
	}
	if _, ok := reparsed["details"]; ok {
		t.Error("after re-marshal, details appeared; absent state was not preserved")
	}
}
