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
//     string (an error message, a server body) that is not JSON. The parameter
//     name is matched case-insensitively (apikey/apiKey/APIKEY).
//
//   - Bare secret fields. The Configuration query exposes apiKey, username, and
//     password as plain scalar strings; the generateAPIKey mutation returns the
//     freshly minted JWT as the bare result field; the interface config exposes
//     handyKey; and a StashBox entry exposes api_key (snake_case). None of those
//     are URLs, so the URL pass cannot catch them; [APIKeys] also redacts the
//     value of any JSON object field whose name (normalised — lower-cased and
//     stripped of underscores) is a known secret (see [secretFields]). The
//     normalisation makes api_key, apiKey, and handyKey match the same set entry
//     regardless of the casing convention a query or mutation happens to use.
//
// [Message] applies both mechanisms to a single free-form string for the CLI
// error path: the [APIKeysInText] URL scrub plus a scrub of bare
// `"secretField":"value"` JSON pairs.
//
// The JWT must never reach stdout or a log regardless of how the payload is
// rendered, so callers run every payload through [APIKeys] (or [Message] for a
// free-form error envelope) before printing it.
//
// Limitation: the configurePlugin and configureUI mutations return Map! values
// (arbitrary, schema-free key/value blobs). A plugin may persist its own
// credentials under field names this redactor cannot enumerate; [secretFields]
// is an explicit allow-list and deliberately does not redact unknown fields, so
// such plugin-stored secrets are NOT scrubbed. Treat raw Map payloads as
// potentially sensitive at the call site.
package redact

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

// redactedValue replaces the secret in a redacted query parameter or field.
const redactedValue = "REDACTED"

// secretFields is the set of normalised JSON object field names whose string
// value is a credential and must be replaced with the REDACTED sentinel
// wholesale, since it is not a URL the query-parameter pass would catch.
//
// Keys are normalised by [normalizeField] (lower-cased, underscores stripped) so
// that every casing convention Stash uses collapses to one entry:
//
//   - apikey       covers Configuration.apiKey and StashBox.api_key
//   - password     Configuration general scalar
//   - generateapikey covers the generateAPIKey mutation's bare result field
//   - username     Configuration general scalar; redacted so a transcript does
//     not identify the instance owner
//   - handykey     ConfigInterface.handyKey (the Handy device token)
//
// The set is explicit on purpose: only these fields are redacted, never an
// arbitrary field name, so non-secret strings pass through untouched.
var secretFields = map[string]bool{
	"apikey":         true,
	"password":       true,
	"generateapikey": true,
	"username":       true,
	"handykey":       true,
}

// normalizeField folds a JSON field name to the form used as a [secretFields]
// key: lower-cased and with underscores removed. This makes apiKey, api_key,
// APIKEY, handyKey, and generate_api_key all match their canonical entry, so a
// server that drifts a field's casing convention cannot reopen a leak.
func normalizeField(key string) string {
	return strings.ReplaceAll(strings.ToLower(key), "_", "")
}

// isSecretField reports whether key (in any casing/underscore convention) names
// a known credential field.
func isSecretField(key string) bool {
	return secretFields[normalizeField(key)]
}

// APIKeys walks the decoded JSON value recursively and scrubs the instance
// credential out of every place it can travel: the value of a known secret field
// (apiKey/api_key, password, generateAPIKey, username, handyKey) and the apikey
// query parameter (case-insensitive) of every pre-signed media URL.
//
// The walk carries the JSON object key of each value. A string is rewritten when
// either it is the value of a secret-named field (replaced by REDACTED) or it
// parses as a URL carrying an apikey parameter (only that parameter's value is
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
		// than risk mangling output we do not understand. Returning a nil error
		// here is intentional — an undecodable payload is passed through unchanged
		// by contract (free-form strings are scrubbed via [APIKeysInText]/[Message]).
		//nolint:nilerr // intentional pass-through of undecodable input; see doc above
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
		if isSecretField(key) && t != "" {
			return redactedValue
		}
		return redactURLString(t)
	default:
		return v
	}
}

// redactURLString returns s with the value of an apikey query parameter
// (matched case-insensitively) replaced by REDACTED, or s unchanged when it does
// not parse as a URL carrying that parameter. Only the apikey parameter is
// touched; other parameters and the rest of the URL are preserved.
func redactURLString(s string) string {
	// Cheap guard: skip the URL parse entirely unless the marker is present in
	// some casing.
	if !containsFold(s, "apikey") {
		return s
	}

	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	q := u.Query()
	changed := false
	for name := range q {
		if strings.EqualFold(name, "apikey") {
			q.Set(name, redactedValue)
			changed = true
		}
	}
	if !changed {
		return s
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// containsFold reports whether substr (already lower-case) appears in s under
// ASCII case-folding. Used as the cheap pre-check before a URL parse.
func containsFold(s, lowerSubstr string) bool {
	return strings.Contains(strings.ToLower(s), lowerSubstr)
}

// apikeyParamRe matches an apikey query parameter (case-insensitive name) up to
// its delimited value, for the free-form text scan. The value runs until the
// first byte that cannot belong to an apikey value in prose (see
// [isAPIKeyValueByte]); the regexp is only used to locate the case-insensitive
// `apikey=` marker, after which the value extent is found byte by byte.
var apikeyParamRe = regexp.MustCompile(`(?i)apikey=`)

// APIKeysInText scrubs the API key out of any pre-signed URL embedded in a
// free-form string — a server error body or a GraphQL error message — that is
// not structured JSON the [APIKeys] walk would reach.
//
// It does a direct, delimiter-aware rewrite of every apikey=<value> occurrence
// (the parameter name matched case-insensitively) rather than a url.Parse,
// because a URL embedded in prose is routinely glued to trailing punctuation (a
// period, a semicolon, a quote) that defeats url.Query — notably a `;` makes
// url.Query silently drop the parameter, which would leak the JWT. The value
// runs from after the marker to the next `&`, `;`, or run of non-URL-value byte
// (whitespace, quote, angle bracket, comma), and is replaced with REDACTED; the
// surrounding text and any sibling parameters are left intact. A string with no
// apikey parameter is returned unchanged.
func APIKeysInText(s string) string {
	if !apikeyParamRe.MatchString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for {
		loc := apikeyParamRe.FindStringIndex(s)
		if loc == nil {
			b.WriteString(s)
			break
		}
		i, markerEnd := loc[0], loc[1]
		end := markerEnd
		for end < len(s) && isAPIKeyValueByte(s[end]) {
			end++
		}
		b.WriteString(s[:i])
		// Preserve the original marker casing (apikey=/apiKey=/APIKEY=).
		b.WriteString(s[i:markerEnd])
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

// jsonSecretFieldRe matches a bare JSON string pair "<field>":"<value>" where
// <field> is any name (captured for an allow-list check) and <value> is a JSON
// string body. Optional whitespace around the colon is tolerated. The value
// capture stops at the first unescaped quote, so an escaped quote inside the
// value does not end it early.
var jsonSecretFieldRe = regexp.MustCompile(`"([^"\\]+)"\s*:\s*"((?:[^"\\]|\\.)*)"`)

// Message redacts a free-form string for the CLI error path. It applies both
// redaction mechanisms:
//
//   - [APIKeysInText], to scrub the apikey value out of any pre-signed URL
//     embedded in the message; and
//   - a scan for bare JSON pairs "<secretField>":"<value>", replacing the value
//     of any known secret field (matched case- and underscore-insensitively via
//     [isSecretField]) with REDACTED.
//
// This is what the CLI runs on a GraphQL error envelope message, which may carry
// either a signed URL (as text) or a serialised config fragment with a bare
// secret field that the structured [APIKeys] walk never sees because the message
// is a plain string. Non-secret fields and all other text are preserved.
func Message(s string) string {
	if s == "" {
		return s
	}
	s = jsonSecretFieldRe.ReplaceAllStringFunc(s, func(match string) string {
		sub := jsonSecretFieldRe.FindStringSubmatch(match)
		if sub == nil || !isSecretField(sub[1]) {
			return match
		}
		// Rebuild with the original field name and the REDACTED sentinel,
		// dropping whatever spacing the source used around the colon.
		return `"` + sub[1] + `":"` + redactedValue + `"`
	})
	return APIKeysInText(s)
}
