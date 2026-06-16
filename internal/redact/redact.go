// Package redact scrubs the instance API key (and adjacent config secrets) out
// of payloads before they reach stdout, stderr, or a log.
//
// Two independent leaks are covered, by two mechanisms:
//
//   - Pre-signed media URLs. Stash signs Scene.paths.stream and friends with the
//     instance API key as an `apikey` query parameter
//     (http://host/scene/42/stream?apikey=<JWT>). That JWT travels in the default
//     scene payload. [APIKeys] rewrites the parameter value wherever such a URL
//     appears as a JSON string; [APIKeysInText] does the same for a free-form
//     string (an error message, a server body) that is not JSON.
//
//   - Bare secret fields. The Configuration query exposes apiKey, username, and
//     password as plain scalar strings, and the generateAPIKey mutation returns
//     the freshly minted JWT as the bare result field. None of those are URLs, so
//     the URL pass cannot catch them; [APIKeys] also redacts the value of any
//     JSON object field whose name is a known secret (see [secretFields]).
//
// The JWT must never reach stdout or a log regardless of how the payload is
// rendered, so callers run every payload through [APIKeys] before printing it.
package redact

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
)

// redactedValue replaces the secret in a redacted query parameter or field.
const redactedValue = "REDACTED"

// secretFields is the set of JSON object field names whose string value is a
// credential and must be replaced with the REDACTED sentinel wholesale, since it
// is not a URL the query-parameter pass would catch. apiKey and password are the
// Configuration general scalars; generateAPIKey is the bare result field of the
// generateAPIKey mutation ({"generateAPIKey":"<jwt>"}). username sits in the same
// general config block as adjacent secret material, so it is redacted too rather
// than left to identify the instance owner in a transcript.
var secretFields = map[string]bool{
	"apiKey":         true,
	"password":       true,
	"generateAPIKey": true,
	"username":       true,
}

// APIKeys walks the decoded JSON value recursively and scrubs the instance
// credential out of every place it can travel: the value of a known secret field
// (apiKey, password, generateAPIKey, username) and the `apikey` query parameter
// of every pre-signed media URL.
//
// The walk carries the JSON object key of each value. A string is rewritten when
// either it is the value of a secret-named field (replaced by REDACTED) or it
// parses as a URL carrying an `apikey` parameter (only that parameter's value is
// swapped, leaving scheme, host, path, and sibling parameters in their decoded
// form). Other strings, and non-string values, pass through untouched.
//
// Numbers are decoded with [json.Decoder.UseNumber], so a JSON integer larger
// than 2^53 (a custom Int64 scalar such as BaseFile.size) survives the
// decode/encode round-trip verbatim rather than being rounded through float64.
// On any decode failure the original bytes pass through unchanged.
func APIKeys(data json.RawMessage) (json.RawMessage, error) {
	if len(data) == 0 {
		return data, nil
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	// Preserve large integers: without UseNumber every JSON number decodes to
	// float64, which silently rounds an Int64 above 2^53 on re-encode.
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		// Not decodable as a JSON value: leave it exactly as received rather
		// than risk mangling output we do not understand.
		return data, nil
	}

	// The top-level value has no enclosing field name.
	redacted := redactValue("", v)

	out, err := json.Marshal(redacted)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// redactValue recurses through the decoded JSON shape (maps, slices, strings)
// redacting secrets in place. key is the name of the object field this value was
// read from ("" for the top level or a slice element); it lets a secret-named
// field's string value be replaced wholesale. json.Number and other non-string
// scalars are returned as-is, so a large integer survives untouched.
func redactValue(key string, v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = redactValue(k, val)
		}
		return t
	case []any:
		for i, val := range t {
			// A slice element has no field name of its own; it keeps the parent
			// key so a string array under a secret field is still redacted.
			t[i] = redactValue(key, val)
		}
		return t
	case string:
		if secretFields[key] && t != "" {
			return redactedValue
		}
		return redactURLString(t)
	default:
		return v
	}
}

// redactURLString returns s with the value of an `apikey` query parameter
// replaced by REDACTED, or s unchanged when it does not parse as a URL carrying
// that parameter. Only the apikey parameter is touched; other parameters and
// the rest of the URL are preserved.
func redactURLString(s string) string {
	// Cheap guard: skip the URL parse entirely unless the marker is present.
	if !strings.Contains(s, "apikey") {
		return s
	}

	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	q := u.Query()
	if _, ok := q["apikey"]; !ok {
		return s
	}
	q.Set("apikey", redactedValue)
	u.RawQuery = q.Encode()
	return u.String()
}

// APIKeysInText scrubs the API key out of any pre-signed URL embedded in a
// free-form string — a server error body or a GraphQL error message — that is
// not structured JSON the [APIKeys] walk would reach.
//
// It does a direct, delimiter-aware rewrite of every `apikey=<value>` occurrence
// rather than a url.Parse, because a URL embedded in prose is routinely glued to
// trailing punctuation (a period, a semicolon, a quote) that defeats url.Query —
// notably a `;` makes url.Query silently drop the parameter, which would leak the
// JWT. The value runs from after `apikey=` to the next `&`, `;`, or run of
// non-URL-value byte (whitespace, quote, angle bracket), and is replaced with
// REDACTED; the surrounding text and any sibling parameters are left intact. A
// string with no apikey parameter is returned unchanged.
func APIKeysInText(s string) string {
	const marker = "apikey="
	if !strings.Contains(s, marker) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for {
		i := strings.Index(s, marker)
		if i < 0 {
			b.WriteString(s)
			break
		}
		end := i + len(marker)
		for end < len(s) && isAPIKeyValueByte(s[end]) {
			end++
		}
		b.WriteString(s[:i])
		b.WriteString(marker)
		b.WriteString(redactedValue)
		s = s[end:]
	}
	return b.String()
}

// isAPIKeyValueByte reports whether b can be part of an apikey value in
// free-form text. The value ends at a query-parameter separator (& or ;), at any
// ASCII whitespace, or at a delimiter that commonly abuts a URL in prose (quotes,
// angle brackets, a trailing comma). A JWT is base64url with dots, all of which
// pass; a sentence's trailing period would end the value, but a JWT does not end
// in a bare period so this does not under-redact a real key.
func isAPIKeyValueByte(b byte) bool {
	switch b {
	case '&', ';', ' ', '\t', '\n', '\r', '\f', '\v', '"', '\'', '<', '>', ',':
		return false
	default:
		return true
	}
}
