package main

import (
	"encoding/json"
	"net/url"
	"strings"
)

// redactedValue replaces the secret in a redacted query parameter.
const redactedValue = "REDACTED"

// redactAPIKeys walks the decoded JSON value recursively and scrubs the API key
// out of every pre-signed media URL it carries. Stash signs Scene.paths.stream
// and friends with the instance API key as an `apikey` query parameter
// (http://host/scene/42/stream?apikey=<JWT>); that JWT travels in the default
// scene payload, so the output layer redacts it before anything is printed or
// logged.
//
// The walk visits every JSON string. A string is rewritten only when it parses
// as a URL that actually carries an `apikey` query parameter, and only that
// parameter's value is swapped for REDACTED — the scheme, host, path, and any
// sibling parameters are preserved byte-for-byte in their decoded form. Strings
// that are not URLs, or URLs without an apikey, are returned untouched. The
// value is re-encoded; on any decode failure the original bytes pass through
// unchanged so redaction can never corrupt a payload.
func redactAPIKeys(data json.RawMessage) (json.RawMessage, error) {
	if len(data) == 0 {
		return data, nil
	}

	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		// Not decodable as a JSON value: leave it exactly as received rather
		// than risk mangling output we do not understand.
		return data, nil
	}

	redacted := redactValue(v)

	out, err := json.Marshal(redacted)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// redactValue recurses through the decoded JSON shape (maps, slices, strings)
// produced by json.Unmarshal into any, redacting apikey-bearing URL strings in
// place. Non-string scalars (numbers, bools, nil) are returned as-is.
func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = redactValue(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = redactValue(val)
		}
		return t
	case string:
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
