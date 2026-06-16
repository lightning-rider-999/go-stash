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

// TestApiKeyRedaction is gate 11 (B4): a findScenes-shaped payload whose media
// URLs carry the instance API key as ?apikey=<JWT> must come out of
// redact.APIKeys with the JWT gone, apikey=REDACTED in its place, and the rest
// of every URL intact. This is the conformance-layer guarantee that no payload
// printed or logged leaks the credential, exercised against the same importable
// redaction the CLI uses.
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

	// The rest of each URL survives byte-for-byte after decoding.
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

// TestConfigSecretFieldRedaction guards the field-name mechanism: the
// Configuration query exposes apiKey, username, and password as bare scalar
// strings — no URL the query-parameter pass would catch — so redact.APIKeys must
// replace each value with REDACTED. A leak here lands the raw JWT and the
// cleartext password on stdout (and thus in an agent transcript).
func TestConfigSecretFieldRedaction(t *testing.T) {
	payload := json.RawMessage(`{
		"configuration": {
			"general": {
				"apiKey": "` + sampleJWT + `",
				"username": "admin",
				"password": "hunter2"
			}
		}
	}`)

	out, err := redact.APIKeys(payload)
	if err != nil {
		t.Fatalf("redact.APIKeys: %v", err)
	}
	s := string(out)

	for _, secret := range []string{sampleJWT, "hunter2", "admin"} {
		if strings.Contains(s, secret) {
			t.Errorf("redacted config still contains the secret %q:\n%s", secret, s)
		}
	}
	if strings.Contains(s, "eyJ") {
		t.Errorf("redacted config still contains a JWT token:\n%s", s)
	}
	// All three secret fields must be present and REDACTED.
	var got struct {
		Configuration struct {
			General struct {
				ApiKey   string `json:"apiKey"`
				Username string `json:"username"`
				Password string `json:"password"`
			} `json:"general"`
		} `json:"configuration"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal redacted config: %v", err)
	}
	g := got.Configuration.General
	if g.ApiKey != "REDACTED" || g.Username != "REDACTED" || g.Password != "REDACTED" {
		t.Errorf("secret fields not all REDACTED: apiKey=%q username=%q password=%q", g.ApiKey, g.Username, g.Password)
	}
}

// TestGenerateAPIKeyRedaction guards the bare-result field: the generateAPIKey
// mutation returns the freshly minted JWT as {"generateAPIKey":"<jwt>"}, a plain
// string with no URL, so the field-name pass must scrub it.
func TestGenerateAPIKeyRedaction(t *testing.T) {
	payload := json.RawMessage(`{"generateAPIKey":"` + sampleJWT + `"}`)
	out, err := redact.APIKeys(payload)
	if err != nil {
		t.Fatalf("redact.APIKeys: %v", err)
	}
	s := string(out)
	if strings.Contains(s, sampleJWT) || strings.Contains(s, "eyJ") {
		t.Errorf("generateAPIKey JWT leaked:\n%s", s)
	}
	if !strings.Contains(s, "REDACTED") {
		t.Errorf("generateAPIKey not REDACTED:\n%s", s)
	}
}

// TestRedactPreservesLargeInt64 guards the number-preservation fix: a custom
// Int64 scalar above 2^53 (BaseFile.size, SQLExecResult row counts) emitted as a
// bare JSON number must survive redact.APIKeys verbatim, not be rounded through
// float64.
func TestRedactPreservesLargeInt64(t *testing.T) {
	const big = "12345678901234567" // > 2^53, loses precision as float64
	payload := json.RawMessage(`{"baseFile":{"size":` + big + `}}`)
	out, err := redact.APIKeys(payload)
	if err != nil {
		t.Fatalf("redact.APIKeys: %v", err)
	}
	if !strings.Contains(string(out), big) {
		t.Errorf("large Int64 was corrupted on redaction round-trip:\nwant %s in\n%s", big, out)
	}
}
