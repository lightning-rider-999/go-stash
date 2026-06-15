package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

// catalogShape is the minimal view of the embedded catalog the CLI relies on.
type catalogShape struct {
	SchemaVersion string                     `json:"schemaVersion"`
	Commands      map[string]json.RawMessage `json:"commands"`
	Defs          map[string]json.RawMessage `json:"$defs"`
}

func TestEmbeddedCatalogParses(t *testing.T) {
	var cat catalogShape
	if err := json.Unmarshal(catalogJSON, &cat); err != nil {
		t.Fatalf("embedded catalog is not valid JSON: %v", err)
	}
	if cat.SchemaVersion == "" {
		t.Error("embedded catalog has no schemaVersion")
	}
	if got := len(cat.Commands); got != 211 {
		t.Errorf("embedded catalog has %d commands, want 211", got)
	}
	if len(cat.Defs) == 0 {
		t.Error("embedded catalog has an empty $defs")
	}
}

func TestCatalogCommandPrintsVerbatim(t *testing.T) {
	cmd := newCatalogCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("catalog command: %v", err)
	}
	// Verbatim: byte-identical to the embedded bytes (modulo a trailing
	// newline the command appends).
	got := bytes.TrimRight(out.Bytes(), "\n")
	want := bytes.TrimRight(catalogJSON, "\n")
	if !bytes.Equal(got, want) {
		t.Errorf("catalog output is not the embedded catalog verbatim")
	}
}

func TestCatalogCommandSingleEntry(t *testing.T) {
	cmd := newCatalogCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"FindScenes"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("catalog FindScenes: %v", err)
	}
	var entry map[string]any
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
		t.Fatalf("single-entry output is not valid JSON: %v\n%s", err, out.String())
	}
	if entry["field"] != "findScenes" {
		t.Errorf("entry = %v, want field=findScenes", entry)
	}
}

func TestCatalogCommandUnknownEntry(t *testing.T) {
	cmd := newCatalogCommand()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"NoSuchOperation"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for an unknown operation name")
	}
}
