package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

// token is a stand-in JWT-shaped secret used across the tests. It is base64url
// with two dots, like a real Stash-signed apikey value, and contains no byte
// that isAPIKeyValueByte treats as a terminator.
const token = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.s3cr3t-Sig_Value123"

// containsToken reports whether the raw secret survived into out.
func containsToken(out string) bool {
	return strings.Contains(out, token)
}

func TestAPIKeysInText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		// wantGone: the raw token must not appear in the output.
		wantGone bool
		// wantRedacted: REDACTED must appear in the output.
		wantRedacted bool
		// wantContains: substrings that must survive (siblings, prose, params).
		wantContains []string
		// wantEqual, when non-empty, asserts the exact output.
		wantEqual string
	}{
		{
			name:      "empty string passthrough",
			in:        "",
			wantEqual: "",
		},
		{
			name:      "no apikey passthrough",
			in:        "https://host/scene/42/stream?foo=bar",
			wantEqual: "https://host/scene/42/stream?foo=bar",
		},
		{
			name:         "bare url",
			in:           "https://host/scene/42/stream?apikey=" + token,
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{"https://host/scene/42/stream?apikey="},
		},
		{
			name:         "trailing semicolon (the documented leak)",
			in:           "https://host/s?apikey=" + token + ";next=1",
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{";next=1"},
		},
		{
			name:         "trailing ampersand sibling param survives",
			in:           "https://host/s?apikey=" + token + "&t=preview",
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{"&t=preview"},
		},
		{
			name:         "trailing whitespace",
			in:           "url is https://host/s?apikey=" + token + " end",
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{"url is https://host/s?apikey=", " end"},
		},
		{
			name:         "trailing double quote",
			in:           `{"stream":"https://host/s?apikey=` + token + `"}`,
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{`"}`},
		},
		{
			name:         "trailing single quote",
			in:           "src='https://host/s?apikey=" + token + "'",
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{"'"},
		},
		{
			name:         "trailing angle bracket",
			in:           "<a href=https://host/s?apikey=" + token + ">link</a>",
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{">link</a>"},
		},
		{
			name:         "trailing comma",
			in:           "urls: https://host/s?apikey=" + token + ", and more",
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{", and more"},
		},
		{
			name:         "trailing period",
			in:           "See https://host/s?apikey=" + token + ".",
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{"See https://host/s?apikey="},
		},
		{
			name:         "multiple occurrences in one string",
			in:           "a=https://host/s?apikey=" + token + " b=https://host/t?apikey=" + token,
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{"a=https://host/s?apikey=", "b=https://host/t?apikey="},
		},
		{
			name:         "prose-embedded url",
			in:           "the failing request was GET https://host/scene/9/stream?apikey=" + token + " returned 500",
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{"the failing request was GET", "returned 500"},
		},
		{
			name:         "uppercase param name (sec-5)",
			in:           "https://host/s?APIKEY=" + token,
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{"APIKEY="},
		},
		{
			name:         "mixed-case param name (sec-5)",
			in:           "https://host/s?apiKey=" + token,
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{"apiKey="},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := APIKeysInText(tt.in)
			if tt.wantEqual != "" || tt.in == "" {
				if got != tt.wantEqual {
					t.Fatalf("APIKeysInText(%q) = %q, want %q", tt.in, got, tt.wantEqual)
				}
				return
			}
			if tt.wantGone && containsToken(got) {
				t.Errorf("token survived: %q", got)
			}
			if tt.wantRedacted && !strings.Contains(got, redactedValue) {
				t.Errorf("missing %q sentinel: %q", redactedValue, got)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("expected substring %q to survive in %q", want, got)
				}
			}
		})
	}
}

func TestAPIKeys_SecretFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		// fields whose value must become REDACTED.
		wantRedactedKeys []string
		// fields whose value must survive unchanged.
		wantKeptKeys map[string]string
	}{
		{
			name:             "camelCase apiKey (Configuration)",
			in:               `{"apiKey":"` + token + `"}`,
			wantRedactedKeys: []string{"apiKey"},
		},
		{
			name:             "snake_case api_key (StashBox, redact-1)",
			in:               `{"api_key":"` + token + `"}`,
			wantRedactedKeys: []string{"api_key"},
		},
		{
			name:             "handyKey (redact-2)",
			in:               `{"handyKey":"` + token + `"}`,
			wantRedactedKeys: []string{"handyKey"},
		},
		{
			name:             "generateAPIKey bare result",
			in:               `{"generateAPIKey":"` + token + `"}`,
			wantRedactedKeys: []string{"generateAPIKey"},
		},
		{
			name:             "password and username",
			in:               `{"password":"hunter2","username":"owner"}`,
			wantRedactedKeys: []string{"password", "username"},
		},
		{
			name:             "uppercase APIKEY field (medium normalisation)",
			in:               `{"APIKEY":"` + token + `"}`,
			wantRedactedKeys: []string{"APIKEY"},
		},
		{
			name:             "nested secret inside stash_boxes array",
			in:               `{"stash_boxes":[{"name":"box","api_key":"` + token + `"}]}`,
			wantRedactedKeys: []string{"api_key"},
			wantKeptKeys:     map[string]string{"name": "box"},
		},
		{
			name:         "non-secret field preserved (explicit allow-list)",
			in:           `{"title":"My Scene","studio":"Acme"}`,
			wantKeptKeys: map[string]string{"title": "My Scene", "studio": "Acme"},
		},
		{
			name:         "empty secret value left empty (not REDACTED)",
			in:           `{"apiKey":""}`,
			wantKeptKeys: map[string]string{"apiKey": ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, err := APIKeys(json.RawMessage(tt.in))
			if err != nil {
				t.Fatalf("APIKeys error: %v", err)
			}
			if containsToken(string(out)) {
				t.Errorf("token survived: %s", out)
			}
			got := decodeFlat(t, out)
			for _, k := range tt.wantRedactedKeys {
				if got[k] != redactedValue {
					t.Errorf("field %q = %q, want %q\nfull: %s", k, got[k], redactedValue, out)
				}
			}
			for k, want := range tt.wantKeptKeys {
				if got[k] != want {
					t.Errorf("field %q = %q, want preserved %q\nfull: %s", k, got[k], want, out)
				}
			}
		})
	}
}

// decodeFlat decodes a JSON object (possibly containing one nested object inside
// a one-element array, as the stash_boxes case does) into a flat map of the
// leaf string fields, so a test can assert on field values regardless of depth.
func decodeFlat(t *testing.T, b []byte) map[string]string {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("re-decode: %v\n%s", err, b)
	}
	out := map[string]string{}
	var walk func(any)
	walk = func(x any) {
		switch m := x.(type) {
		case map[string]any:
			for k, val := range m {
				if s, ok := val.(string); ok {
					out[k] = s
				}
				walk(val)
			}
		case []any:
			for _, el := range m {
				walk(el)
			}
		}
	}
	walk(v)
	return out
}

func TestAPIKeys_URLPass(t *testing.T) {
	t.Parallel()

	in := `{"paths":{"stream":"https://host/scene/42/stream?apikey=` + token + `&resolution=ORIGINAL"}}`
	out, err := APIKeys(json.RawMessage(in))
	if err != nil {
		t.Fatalf("APIKeys error: %v", err)
	}
	s := string(out)
	if containsToken(s) {
		t.Errorf("token survived in URL value: %s", s)
	}
	if !strings.Contains(s, redactedValue) {
		t.Errorf("missing %q sentinel: %s", redactedValue, s)
	}
	if !strings.Contains(s, "resolution=ORIGINAL") {
		t.Errorf("sibling query param dropped: %s", s)
	}
}

func TestAPIKeys_LargeIntPreserved(t *testing.T) {
	t.Parallel()

	in := `{"size":9223372036854775807}`
	out, err := APIKeys(json.RawMessage(in))
	if err != nil {
		t.Fatalf("APIKeys error: %v", err)
	}
	if !strings.Contains(string(out), "9223372036854775807") {
		t.Errorf("large int rounded: %s", out)
	}
}

func TestAPIKeys_EmptyAndUndecodablePassthrough(t *testing.T) {
	t.Parallel()

	for _, in := range []json.RawMessage{nil, json.RawMessage(""), json.RawMessage("not json at all")} {
		out, err := APIKeys(in)
		if err != nil {
			t.Fatalf("APIKeys(%q) error: %v", in, err)
		}
		if string(out) != string(in) {
			t.Errorf("APIKeys(%q) = %q, want passthrough", in, out)
		}
	}
}

func TestMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		in           string
		wantEqual    string
		wantGone     bool
		wantRedacted bool
		wantContains []string
	}{
		{
			name:      "empty passthrough",
			in:        "",
			wantEqual: "",
		},
		{
			name:      "plain prose passthrough",
			in:        "scene not found",
			wantEqual: "scene not found",
		},
		{
			name:         "url in message (APIKeysInText path)",
			in:           "stream failed: https://host/s?apikey=" + token + ";",
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{"stream failed:", ";"},
		},
		{
			name:         "bare json secret field camelCase",
			in:           `error: {"apiKey":"` + token + `"}`,
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{`"apiKey":"REDACTED"`},
		},
		{
			name:         "bare json secret field snake_case (redact-1 in text)",
			in:           `config dump {"api_key":"` + token + `","name":"box"}`,
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{`"api_key":"REDACTED"`, `"name":"box"`},
		},
		{
			name:         "bare json handyKey (redact-2 in text)",
			in:           `{"handyKey":"` + token + `"}`,
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{`"handyKey":"REDACTED"`},
		},
		{
			name:      "non-secret json field preserved",
			in:        `{"title":"my-scene","studio":"acme"}`,
			wantEqual: `{"title":"my-scene","studio":"acme"}`,
		},
		{
			name:         "both mechanisms in one message",
			in:           `failed {"password":"hunter2"} fetching https://host/s?apikey=` + token,
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{`"password":"REDACTED"`, "fetching https://host/s?apikey="},
		},
		{
			name:         "spacing around colon tolerated",
			in:           `{"apiKey" : "` + token + `"}`,
			wantGone:     true,
			wantRedacted: true,
			wantContains: []string{`"apiKey":"REDACTED"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Message(tt.in)
			if tt.wantEqual != "" || tt.in == "" {
				if got != tt.wantEqual {
					t.Fatalf("Message(%q) = %q, want %q", tt.in, got, tt.wantEqual)
				}
				return
			}
			if tt.wantGone && strings.Contains(got, token) {
				t.Errorf("token survived: %q", got)
			}
			if tt.wantRedacted && !strings.Contains(got, redactedValue) {
				t.Errorf("missing %q sentinel: %q", redactedValue, got)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("expected substring %q in %q", want, got)
				}
			}
		})
	}
}

func TestNormalizeField(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"apiKey":           "apikey",
		"api_key":          "apikey",
		"APIKEY":           "apikey",
		"handyKey":         "handykey",
		"generateAPIKey":   "generateapikey",
		"generate_api_key": "generateapikey",
	}
	for in, want := range cases {
		if got := normalizeField(in); got != want {
			t.Errorf("normalizeField(%q) = %q, want %q", in, got, want)
		}
	}
}

// FuzzAPIKeysInText plants the known token as an apikey value inside arbitrary
// surrounding prose and fails if the token survives the scrub. Seeded with the
// doc-comment edge cases (trailing punctuation that defeats url.Query).
func FuzzAPIKeysInText(f *testing.F) {
	seeds := []string{
		"",
		"https://host/s?apikey=" + token,
		"https://host/s?apikey=" + token + ";",
		"https://host/s?apikey=" + token + "&t=1",
		"https://host/s?apikey=" + token + ".",
		`{"x":"https://host/s?apikey=` + token + `"}`,
		"a?apikey=" + token + " b?apikey=" + token,
		"prose https://host/s?APIKEY=" + token + " more",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, prefix string) {
		// The invariant is about the secret the redactor is responsible for: the
		// value of an apikey parameter. Strip the token and the apikey marker out
		// of the fuzzer-controlled prefix so the only occurrence of the token in
		// the input is the apikey value we plant. (A prefix that merely happens to
		// equal the token is arbitrary prose, not a parameter value, and is
		// correctly left alone — asserting otherwise would test the fuzzer, not
		// the code.)
		prefix = strings.ReplaceAll(prefix, token, "")
		prefix = removeFold(prefix, "apikey=")
		in := prefix + "?apikey=" + token
		out := APIKeysInText(in)
		if strings.Contains(out, token) {
			t.Fatalf("token survived APIKeysInText\n in: %q\nout: %q", in, out)
		}
	})
}

// removeFold deletes every case-insensitive (ASCII) occurrence of sub from s.
// Used to keep a fuzzer-controlled prefix from smuggling its own apikey= marker
// into a fuzz input, so the only redactable secret is the one the harness
// plants. It is byte-safe: it scans s directly rather than its lower-cased copy,
// so an invalid-UTF-8 byte (whose ToLower may change the byte length) cannot
// throw the slice index off — that was a real bug a fuzzer-supplied \xc8 hit.
func removeFold(s, sub string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if i+len(sub) <= len(s) && strings.EqualFold(s[i:i+len(sub)], sub) {
			i += len(sub)
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// FuzzAPIKeys plants the token as the value of a known secret field inside an
// arbitrary-keyed JSON object and fails if it survives. The marshalled input is
// always valid JSON so the walk runs (an undecodable blob is passed through by
// contract and would not exercise redaction).
func FuzzAPIKeys(f *testing.F) {
	for _, field := range []string{"apiKey", "api_key", "handyKey", "password", "generateAPIKey", "username"} {
		f.Add(field, "sibling")
	}
	f.Add("apiKey", "harmless")
	f.Fuzz(func(t *testing.T, siblingKey, siblingVal string) {
		// The invariant: the token, placed only as the value of known secret
		// fields, must never survive. Keep the fuzzer-controlled sibling from
		// re-introducing the token itself — a sibling key or non-secret value
		// equal to the token is arbitrary data the redactor is not asked to
		// scrub, so it would be a false positive, not a real leak.
		siblingKey = strings.ReplaceAll(siblingKey, token, "")
		siblingVal = strings.ReplaceAll(siblingVal, token, "")
		// Build a valid JSON object: known secret fields hold the token, plus a
		// fuzzed sibling field holding non-secret text. A blank or secret-named
		// sibling key would collide with the planted fields; harmless either way.
		obj := map[string]string{
			"apiKey":   token,
			"api_key":  token,
			"handyKey": token,
		}
		if siblingKey != "" && !isSecretField(siblingKey) {
			obj[siblingKey] = siblingVal
		}
		in, err := json.Marshal(obj)
		if err != nil {
			t.Skip()
		}
		out, err := APIKeys(in)
		if err != nil {
			t.Fatalf("APIKeys error: %v", err)
		}
		if strings.Contains(string(out), token) {
			t.Fatalf("token survived APIKeys\n in: %s\nout: %s", in, out)
		}
	})
}
