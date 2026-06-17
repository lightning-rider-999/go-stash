package conformance

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lightning-rider-999/go-stashapp/internal/redact"
)

// sampleJWT is a representative pre-signed API key. Its presence in any redacted
// output is a leak.
const sampleJWT = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ4In0.sig"

// TestApiKeyRedaction is gate 11 (B4) at the conformance layer: a realistic
// findScenes-shaped response whose media URLs carry the instance API key as
// ?apikey=<JWT> must come out of redact.APIKeys with the JWT gone, apikey=REDACTED
// in its place, and the rest of every URL — path and sibling query parameters —
// intact. This is the integration-shaped guarantee that a payload the CLI prints
// or logs cannot leak the credential, exercised against the same importable
// redaction the CLI uses on a payload shaped like a real query result.
//
// The unit-level behaviour of the redactor (bare secret fields, URL-pass edge
// cases, the empty/undecodable passthrough, large-Int64 number preservation, and
// in-text JWT scrubbing) is covered DIRECTLY and exhaustively in
// internal/redact's own tests (TestAPIKeysInText, TestAPIKeys_SecretFields,
// TestAPIKeys_URLPass, TestAPIKeys_LargeIntPreserved,
// TestAPIKeys_EmptyAndUndecodablePassthrough). This gate deliberately keeps only
// the realistic-payload integration case so the two layers do not duplicate.
func TestApiKeyRedaction(t *testing.T) {
	payload := json.RawMessage(`{
		"findScenes": {
			"count": 1,
			"scenes": [
				{
					"id": "42",
					"title": "anything",
					"paths": {
						"stream": "http://stash.local/scene/42/stream?apikey=` + sampleJWT + `",
						"screenshot": "http://stash.local/scene/42/screenshot?apikey=` + sampleJWT + `&t=12"
					}
				}
			]
		}
	}`)

	out, err := redact.APIKeys(payload)
	if err != nil {
		t.Fatalf("redact.APIKeys: %v", err)
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

	// The rest of each URL survives after decoding.
	var got struct {
		FindScenes struct {
			Scenes []struct {
				Paths struct {
					Stream     string `json:"stream"`
					Screenshot string `json:"screenshot"`
				} `json:"paths"`
			} `json:"scenes"`
		} `json:"findScenes"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal redacted payload: %v", err)
	}
	if len(got.FindScenes.Scenes) != 1 {
		t.Fatalf("expected 1 scene in redacted payload, got %d", len(got.FindScenes.Scenes))
	}
	stream := got.FindScenes.Scenes[0].Paths.Stream
	if !strings.HasPrefix(stream, "http://stash.local/scene/42/stream") {
		t.Errorf("stream URL path was mangled: %q", stream)
	}
	if !strings.Contains(stream, "apikey=REDACTED") {
		t.Errorf("stream URL apikey not redacted: %q", stream)
	}
	screenshot := got.FindScenes.Scenes[0].Paths.Screenshot
	if !strings.HasPrefix(screenshot, "http://stash.local/scene/42/screenshot") {
		t.Errorf("screenshot URL path was mangled: %q", screenshot)
	}
	// The sibling t=12 query parameter must survive.
	if !strings.Contains(screenshot, "t=12") {
		t.Errorf("screenshot lost its sibling t=12 parameter: %q", screenshot)
	}
}
