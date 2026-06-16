package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// The JWT used in the golden fixtures. Its presence in any output is a leak.
const sampleJWT = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ4In0.sig"

// findScenesDefaultPayload synthesises a findScenes default response whose
// scenes[].paths.stream carries the API key as Stash pre-signs it.
func findScenesDefaultPayload() json.RawMessage {
	return json.RawMessage(`{
		"findScenes": {
			"count": 1,
			"scenes": [
				{
					"id": "42",
					"title": "Two Girls One Cup of Coffee",
					"paths": {
						"stream": "http://stash.local/scene/42/stream?apikey=` + sampleJWT + `",
						"screenshot": "http://stash.local/scene/42/screenshot?apikey=` + sampleJWT + `&t=12"
					}
				}
			]
		}
	}`)
}

func TestRedactAPIKeysScrubsJWT(t *testing.T) {
	out, err := redactAPIKeys(findScenesDefaultPayload())
	if err != nil {
		t.Fatalf("redactAPIKeys: %v", err)
	}
	s := string(out)

	if strings.Contains(s, "eyJ") {
		t.Errorf("redacted output still contains a JWT token:\n%s", s)
	}
	if strings.Contains(s, "apikey="+sampleJWT) {
		t.Errorf("redacted output still contains apikey=<jwt>:\n%s", s)
	}
	if !strings.Contains(s, "apikey=REDACTED") {
		t.Errorf("redacted output is missing apikey=REDACTED:\n%s", s)
	}
	// The surrounding URL is preserved.
	if !strings.Contains(s, "http://stash.local/scene/42/stream") {
		t.Errorf("redaction mangled the URL path:\n%s", s)
	}
	// A sibling parameter on the screenshot URL survives.
	if !strings.Contains(s, "t=12") {
		t.Errorf("redaction dropped a sibling query parameter:\n%s", s)
	}
}

func TestRedactLeavesNonURLStringsAlone(t *testing.T) {
	in := json.RawMessage(`{"title":"a film about apikey theft","note":"apikey is a word here"}`)
	out, err := redactAPIKeys(in)
	if err != nil {
		t.Fatalf("redactAPIKeys: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["title"] != "a film about apikey theft" || got["note"] != "apikey is a word here" {
		t.Errorf("non-URL strings were altered: %v", got)
	}
}

func TestRedactPreservesOtherQueryParams(t *testing.T) {
	in := json.RawMessage(`["http://host/p?token=keepme&apikey=` + sampleJWT + `&page=3"]`)
	out, err := redactAPIKeys(in)
	if err != nil {
		t.Fatalf("redactAPIKeys: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "token=keepme") || !strings.Contains(s, "page=3") {
		t.Errorf("sibling params dropped:\n%s", s)
	}
	if strings.Contains(s, sampleJWT) {
		t.Errorf("apikey not redacted:\n%s", s)
	}
}

// TestRedactScrubsConfigSecrets: the Configuration-shaped payload exposes apiKey,
// username, and password as bare scalar strings. Field-name redaction must scrub
// all three; the URL pass alone never would.
func TestRedactScrubsConfigSecrets(t *testing.T) {
	in := json.RawMessage(`{"configuration":{"general":{"apiKey":"` + sampleJWT + `","username":"admin","password":"hunter2"}}}`)
	out, err := redactAPIKeys(in)
	if err != nil {
		t.Fatalf("redactAPIKeys: %v", err)
	}
	s := string(out)
	for _, secret := range []string{sampleJWT, "hunter2", "admin", "eyJ"} {
		if strings.Contains(s, secret) {
			t.Errorf("config secret %q leaked through redaction:\n%s", secret, s)
		}
	}
	if !strings.Contains(s, "REDACTED") {
		t.Errorf("redacted config is missing REDACTED:\n%s", s)
	}
}

// TestRedactScrubsGenerateAPIKey: the generateAPIKey mutation returns the bare
// JWT as {"generateAPIKey":"<jwt>"}; the field-name pass must scrub it.
func TestRedactScrubsGenerateAPIKey(t *testing.T) {
	out, err := redactAPIKeys(json.RawMessage(`{"generateAPIKey":"` + sampleJWT + `"}`))
	if err != nil {
		t.Fatalf("redactAPIKeys: %v", err)
	}
	s := string(out)
	if strings.Contains(s, sampleJWT) || strings.Contains(s, "eyJ") {
		t.Errorf("generateAPIKey JWT leaked:\n%s", s)
	}
	if !strings.Contains(s, "REDACTED") {
		t.Errorf("generateAPIKey not REDACTED:\n%s", s)
	}
}

// TestWriteOutputPreservesLargeInt64: a custom Int64 scalar above 2^53 must
// round-trip through writeOutput unchanged in every format. Before the
// number-preserving decode it was rounded through float64.
func TestWriteOutputPreservesLargeInt64(t *testing.T) {
	const big = "12345678901234567" // > 2^53
	spec := commandSpec{
		Path:       []string{"file", "get"},
		OpName:     "FindFile",
		ReturnType: "BaseFile",
	}
	payload := json.RawMessage(`{"findFile":{"id":"7","size":` + big + `}}`)
	for _, format := range []string{"json", "ndjson", "yaml", "table"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeOutput(&buf, format, spec, payload); err != nil {
				t.Fatalf("writeOutput(%s): %v", format, err)
			}
			if !strings.Contains(buf.String(), big) {
				t.Errorf("%s output corrupted the Int64 %s:\n%s", format, big, buf.String())
			}
		})
	}
}

// TestWriteOutputRedactsAllFormats is the end-to-end golden: the payload run
// through writeOutput in json and ndjson modes must leak no JWT.
func TestWriteOutputRedactsAllFormats(t *testing.T) {
	spec := commandSpec{
		Path:       []string{"scene", "list"},
		OpName:     "FindScenes",
		ReturnType: "FindScenesResultType",
	}
	for _, format := range []string{"json", "ndjson", "yaml", "table"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeOutput(&buf, format, spec, findScenesDefaultPayload()); err != nil {
				t.Fatalf("writeOutput(%s): %v", format, err)
			}
			s := buf.String()
			if strings.Contains(s, "eyJ") {
				t.Errorf("%s output leaked a JWT:\n%s", format, s)
			}
			if strings.Contains(s, "apikey="+sampleJWT) {
				t.Errorf("%s output leaked apikey=<jwt>:\n%s", format, s)
			}
		})
	}
}
