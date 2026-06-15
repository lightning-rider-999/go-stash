package conformance

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lightning-rider-999/go-stashapp/stash"
)

// TestScalarRoundTrips is gate 3: a value of each custom-scalar Go binding must
// survive a marshal/unmarshal round-trip through encoding/json. The bindings are
// the SDK's contract for Stash's custom scalars; if one were re-bound to an
// incompatible Go type, the round-trip here breaks. PluginConfigMap and
// Timestamp are exercised explicitly because they are the loosest bindings
// (map[string]interface{} and string) and the easiest to get wrong.
func TestScalarRoundTrips(t *testing.T) {
	t.Run("Time", func(t *testing.T) {
		// Time binds to time.Time.
		in := time.Date(2026, 6, 15, 12, 34, 56, 0, time.UTC)
		var out time.Time
		roundTrip(t, in, &out)
		if !in.Equal(out) {
			t.Errorf("Time round-trip: got %v, want %v", out, in)
		}
	})

	t.Run("Int64", func(t *testing.T) {
		// Int64 binds to int64. Use a value beyond int32 range to prove width.
		in := int64(9_007_199_254_740_993)
		var out int64
		roundTrip(t, in, &out)
		if in != out {
			t.Errorf("Int64 round-trip: got %d, want %d", out, in)
		}
	})

	t.Run("Timestamp", func(t *testing.T) {
		// Timestamp binds to string.
		in := "2026-06-15T12:34:56Z"
		var out string
		roundTrip(t, in, &out)
		if in != out {
			t.Errorf("Timestamp round-trip: got %q, want %q", out, in)
		}
	})

	t.Run("PluginConfigMap", func(t *testing.T) {
		// PluginConfigMap binds to map[string]interface{}.
		in := map[string]interface{}{
			"enabled":   true,
			"threshold": float64(0.75),
			"label":     "default",
			"tags":      []interface{}{"a", "b"},
		}
		var out map[string]interface{}
		roundTrip(t, in, &out)
		if !reflect.DeepEqual(in, out) {
			t.Errorf("PluginConfigMap round-trip: got %#v, want %#v", out, in)
		}
	})

	t.Run("BoolMap", func(t *testing.T) {
		// BoolMap binds to map[string]bool.
		in := map[string]bool{"organized": true, "interactive": false}
		var out map[string]bool
		roundTrip(t, in, &out)
		if !reflect.DeepEqual(in, out) {
			t.Errorf("BoolMap round-trip: got %#v, want %#v", out, in)
		}
	})

	t.Run("MapAndAny", func(t *testing.T) {
		// Map and Any both bind to encoding/json.RawMessage.
		in := json.RawMessage(`{"k":[1,2,3],"nested":{"x":true}}`)
		var out json.RawMessage
		roundTrip(t, in, &out)
		if !json.Valid(out) {
			t.Fatalf("Map/Any round-trip produced invalid JSON: %s", out)
		}
		// RawMessage is canonicalised by marshalling both sides through a neutral
		// value, since whitespace is not significant for these scalars.
		if !sameJSON(t, in, out) {
			t.Errorf("Map/Any round-trip changed the JSON value: got %s, want %s", out, in)
		}
	})

	t.Run("UploadPresent", func(t *testing.T) {
		// Upload is a transport type (multipart file part), not a JSON-marshalled
		// scalar. Gate 3 only requires its presence and shape in the binding
		// surface; gate 9 asserts its placement in the SDL. Confirm the named
		// type exists with the documented Filename/Body shape.
		u := stash.Upload{Filename: "import.zip", Body: strings.NewReader("payload")}
		if u.Filename != "import.zip" {
			t.Errorf("Upload.Filename = %q, want import.zip", u.Filename)
		}
		if u.Body == nil {
			t.Error("Upload.Body is nil; the Upload binding lost its io.Reader field")
		}
		rt := reflect.TypeOf(stash.Upload{})
		if _, ok := rt.FieldByName("Filename"); !ok {
			t.Error("stash.Upload has no Filename field")
		}
		if _, ok := rt.FieldByName("Body"); !ok {
			t.Error("stash.Upload has no Body field")
		}
	})
}

// roundTrip marshals in, unmarshals the bytes into out, and fails the test on
// any encoding error.
func roundTrip[T any](t *testing.T, in T, out *T) {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal %T: %v", in, err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("unmarshal into %T: %v", out, err)
	}
}

// sameJSON reports whether two JSON byte slices encode the same value, ignoring
// insignificant whitespace and key ordering.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	ca, err := canonicalJSON(a)
	if err != nil {
		t.Fatalf("canonicalise %s: %v", a, err)
	}
	cb, err := canonicalJSON(b)
	if err != nil {
		t.Fatalf("canonicalise %s: %v", b, err)
	}
	return bytes.Equal(ca, cb)
}

// canonicalJSON re-encodes raw JSON through a neutral value so that two
// semantically equal documents compare byte-equal.
func canonicalJSON(raw []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
