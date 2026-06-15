package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// listResultSpec is a findScenes-shaped result-wrapper spec.
var listResultSpec = commandSpec{
	Path:       []string{"scene", "list"},
	OpName:     "FindScenes",
	ReturnType: "FindScenesResultType",
}

// bareListSpec returns a top-level array ([Scene!]!).
var bareListSpec = commandSpec{
	Path:       []string{"scene", "all"},
	OpName:     "AllScenes",
	ReturnType: "Scene",
}

// singleSpec returns a single entity.
var singleSpec = commandSpec{
	Path:       []string{"scene", "get"},
	OpName:     "FindScene",
	ReturnType: "Scene",
}

func findScenesN(n int) json.RawMessage {
	items := make([]string, n)
	for i := range items {
		items[i] = `{"id":"` + string(rune('a'+i)) + `","title":"scene"}`
	}
	return json.RawMessage(`{"findScenes":{"count":` + itoa(n) + `,"scenes":[` + strings.Join(items, ",") + `]}}`)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestOutputJSONIsDefaultAndPretty(t *testing.T) {
	var buf bytes.Buffer
	payload := json.RawMessage(`{"version":{"version":"v0.31.1"}}`)
	if err := writeOutput(&buf, "json", singleSpec, payload); err != nil {
		t.Fatalf("writeOutput: %v", err)
	}
	s := buf.String()
	// Pretty: contains a newline and a two-space indent.
	if !strings.Contains(s, "\n") || !strings.Contains(s, "  ") {
		t.Errorf("json is not pretty-printed:\n%s", s)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("json output is not valid JSON: %v\n%s", err, s)
	}
}

func TestOutputNDJSONStreamsListResult(t *testing.T) {
	const n = 3
	var buf bytes.Buffer
	if err := writeOutput(&buf, "ndjson", listResultSpec, findScenesN(n)); err != nil {
		t.Fatalf("writeOutput: %v", err)
	}
	lines := nonEmptyLines(buf.String())
	if len(lines) != n {
		t.Fatalf("ndjson emitted %d lines, want %d:\n%s", len(lines), n, buf.String())
	}
	for i, ln := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(ln), &obj); err != nil {
			t.Errorf("line %d is not valid JSON: %v\n%s", i, err, ln)
		}
		if obj["title"] != "scene" {
			t.Errorf("line %d = %v, want a scene object", i, obj)
		}
	}
}

func TestOutputNDJSONStreamsBareList(t *testing.T) {
	var buf bytes.Buffer
	payload := json.RawMessage(`{"allScenes":[{"id":"1"},{"id":"2"}]}`)
	if err := writeOutput(&buf, "ndjson", bareListSpec, payload); err != nil {
		t.Fatalf("writeOutput: %v", err)
	}
	if got := len(nonEmptyLines(buf.String())); got != 2 {
		t.Fatalf("ndjson bare list emitted %d lines, want 2:\n%s", got, buf.String())
	}
}

func TestOutputNDJSONNonListEmitsOneLine(t *testing.T) {
	var buf bytes.Buffer
	payload := json.RawMessage(`{"findScene":{"id":"42","title":"solo"}}`)
	if err := writeOutput(&buf, "ndjson", singleSpec, payload); err != nil {
		t.Fatalf("writeOutput: %v", err)
	}
	lines := nonEmptyLines(buf.String())
	if len(lines) != 1 {
		t.Fatalf("ndjson non-list emitted %d lines, want 1:\n%s", len(lines), buf.String())
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &obj); err != nil {
		t.Fatalf("line is not valid JSON: %v", err)
	}
}

func TestOutputYAML(t *testing.T) {
	var buf bytes.Buffer
	payload := json.RawMessage(`{"findScene":{"id":"42","title":"solo"}}`)
	if err := writeOutput(&buf, "yaml", singleSpec, payload); err != nil {
		t.Fatalf("writeOutput: %v", err)
	}
	s := buf.String()
	if !strings.Contains(s, "title: solo") {
		t.Errorf("yaml output missing expected key/value:\n%s", s)
	}
}

func TestOutputTableList(t *testing.T) {
	var buf bytes.Buffer
	if err := writeOutput(&buf, "table", listResultSpec, findScenesN(2)); err != nil {
		t.Fatalf("writeOutput: %v", err)
	}
	s := buf.String()
	// Header columns are the union of scalar keys.
	if !strings.Contains(s, "id") || !strings.Contains(s, "title") {
		t.Errorf("table missing expected columns:\n%s", s)
	}
	if !strings.Contains(s, "scene") {
		t.Errorf("table missing row data:\n%s", s)
	}
}

func TestOutputTableSingleObject(t *testing.T) {
	var buf bytes.Buffer
	payload := json.RawMessage(`{"findScene":{"id":"42","title":"solo"}}`)
	if err := writeOutput(&buf, "table", singleSpec, payload); err != nil {
		t.Fatalf("writeOutput: %v", err)
	}
	s := buf.String()
	if !strings.Contains(s, "id") || !strings.Contains(s, "42") || !strings.Contains(s, "title") {
		t.Errorf("key/value table missing expected content:\n%s", s)
	}
}

func TestOutputTableSkipsNested(t *testing.T) {
	var buf bytes.Buffer
	payload := json.RawMessage(`{"findScene":{"id":"42","paths":{"stream":"x"},"tags":[1,2]}}`)
	if err := writeOutput(&buf, "table", singleSpec, payload); err != nil {
		t.Fatalf("writeOutput: %v", err)
	}
	// A nested object/array value should render as a placeholder, not raw JSON.
	if strings.Contains(buf.String(), `"stream"`) {
		t.Errorf("table inlined a nested object:\n%s", buf.String())
	}
}

func TestOutputUnknownFormatErrors(t *testing.T) {
	var buf bytes.Buffer
	err := writeOutput(&buf, "xml", singleSpec, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected an error for an unknown format")
	}
	for _, want := range []string{"json", "ndjson", "table", "yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name the valid format %q", err, want)
		}
	}
}

func TestOutputNullData(t *testing.T) {
	var buf bytes.Buffer
	if err := writeOutput(&buf, "json", singleSpec, nil); err != nil {
		t.Fatalf("writeOutput: %v", err)
	}
	if strings.TrimSpace(buf.String()) != "null" {
		t.Errorf("null data = %q, want null", buf.String())
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	return out
}
