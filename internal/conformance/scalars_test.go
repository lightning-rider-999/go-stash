package conformance

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lightning-rider-999/go-stash/stash"
)

// TestScalarBindings is gate 3: each of Stash's custom scalars must be bound by
// genqlient to the exact Go type the SDK's §10 contract (and genqlient.yaml)
// promises. Each subtest does two things:
//
//  1. ANCHORS the assertion to the ACTUAL generated binding — it reflects on a
//     real generated input/response field (or generated function parameter)
//     whose GraphQL type is that scalar, and asserts the Go type genqlient chose.
//     This is the load-bearing check: if genqlient.yaml re-bound a scalar (say
//     Int64 -> string, or Map -> any), the generated field's type changes and
//     the reflect assertion fails. A round-trip over a literal stdlib value
//     alone could not catch that, because it never touches the generated surface.
//  2. ROUND-TRIPS a value of that bound type through encoding/json, proving the
//     binding is actually JSON-marshalable in both directions.
//
// The anchor fields are picked from stable generated types and named here so a
// reader can see exactly which SDL field pins each scalar:
//
//	Time            -> BasicFileFields.Mod_time            (file.mod_time: Time!)
//	Int64           -> BasicFileFields.Size                (file.size: Int64!)
//	Timestamp       -> ScanMetaDataFilterInput.MinModTime  (minModTime: Timestamp)
//	Map             -> ...ConfigResult.Ui                   (config.ui: Map!)
//	Any             -> RunPluginOperationResponse.RunPluginOperation (: Any)
//	BoolMap         -> SetPluginsEnabled arg enabledMap    (enabledMap: BoolMap!)
//	PluginConfigMap -> ...ConfigResult.Plugins             (config.plugins: PluginConfigMap!)
func TestScalarBindings(t *testing.T) {
	t.Run("Time_binds_time.Time", func(t *testing.T) {
		assertFieldType(t, reflect.TypeOf(stash.BasicFileFields{}), "Mod_time", reflect.TypeOf(time.Time{}))

		in := time.Date(2026, 6, 15, 12, 34, 56, 0, time.UTC)
		var out time.Time
		roundTrip(t, in, &out)
		if !in.Equal(out) {
			t.Errorf("Time round-trip: got %v, want %v", out, in)
		}
	})

	t.Run("Int64_binds_int64", func(t *testing.T) {
		assertFieldType(t, reflect.TypeOf(stash.BasicFileFields{}), "Size", reflect.TypeOf(int64(0)))

		// A value beyond int32 range proves the binding is a full-width int64.
		in := int64(9_007_199_254_740_993)
		var out int64
		roundTrip(t, in, &out)
		if in != out {
			t.Errorf("Int64 round-trip: got %d, want %d", out, in)
		}
	})

	t.Run("Timestamp_binds_string", func(t *testing.T) {
		// minModTime is nullable, so genqlient makes the field *string; the
		// scalar binding is the element type, which must be string.
		assertPointerElemFieldType(t, reflect.TypeOf(stash.ScanMetaDataFilterInput{}), "MinModTime", reflect.TypeOf(""))

		in := "2026-06-15T12:34:56Z"
		var out string
		roundTrip(t, in, &out)
		if in != out {
			t.Errorf("Timestamp round-trip: got %q, want %q", out, in)
		}
	})

	t.Run("Map_binds_json.RawMessage", func(t *testing.T) {
		assertFieldType(t, reflect.TypeOf(stash.ConfigurationConfigurationConfigResult{}), "Ui", reflect.TypeOf(json.RawMessage(nil)))
		assertRawMessageRoundTrip(t)
	})

	t.Run("Any_binds_json.RawMessage", func(t *testing.T) {
		// runPluginOperation: Any is nullable, so the field is *json.RawMessage;
		// the scalar binding is the element type.
		assertPointerElemFieldType(t, reflect.TypeOf(stash.RunPluginOperationResponse{}), "RunPluginOperation", reflect.TypeOf(json.RawMessage(nil)))
		assertRawMessageRoundTrip(t)
	})

	t.Run("BoolMap_binds_map_string_bool", func(t *testing.T) {
		// BoolMap appears only on setPluginsEnabled(enabledMap: BoolMap!); the
		// generated input struct is unexported, but the exported SetPluginsEnabled
		// function takes the same bound type as its enabledMap parameter.
		fnType := reflect.TypeOf(stash.SetPluginsEnabled)
		// (ctx, client, enabledMap) -> enabledMap is parameter index 2.
		if fnType.NumIn() < 3 {
			t.Fatalf("stash.SetPluginsEnabled has %d params, want >= 3", fnType.NumIn())
		}
		if got, want := fnType.In(2), reflect.TypeOf(map[string]bool(nil)); got != want {
			t.Errorf("BoolMap bound to %s, want %s (genqlient.yaml re-bound BoolMap)", got, want)
		}

		in := map[string]bool{"organized": true, "interactive": false}
		var out map[string]bool
		roundTrip(t, in, &out)
		if !reflect.DeepEqual(in, out) {
			t.Errorf("BoolMap round-trip: got %#v, want %#v", out, in)
		}
	})

	t.Run("PluginConfigMap_binds_map_string_any", func(t *testing.T) {
		assertFieldType(t, reflect.TypeOf(stash.ConfigurationConfigurationConfigResult{}), "Plugins", reflect.TypeOf(map[string]interface{}(nil)))

		in := map[string]interface{}{
			"enabled":   true,
			"threshold": 0.75,
			"label":     "default",
			"tags":      []interface{}{"a", "b"},
		}
		var out map[string]interface{}
		roundTrip(t, in, &out)
		if !reflect.DeepEqual(in, out) {
			t.Errorf("PluginConfigMap round-trip: got %#v, want %#v", out, in)
		}
	})

	t.Run("Upload_binds_stash.Upload", func(t *testing.T) {
		// Upload is the multipart transport scalar; it is not JSON-marshalled, so
		// the contract is structural. Anchor the binding to ImportObjectsInput.File,
		// the single generated field typed Upload, and confirm its Filename/Body
		// shape via reflection (mirrors how gate 9 asserts its SDL placement).
		assertFieldType(t, reflect.TypeOf(stash.ImportObjectsInput{}), "File", reflect.TypeOf(stash.Upload{}))

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

// assertFieldType fails the test unless struct type st declares a field named
// field whose Go type is exactly want. The error names the actual type so a
// re-binding is obvious.
func assertFieldType(t *testing.T, st reflect.Type, field string, want reflect.Type) {
	t.Helper()
	sf, ok := st.FieldByName(field)
	if !ok {
		t.Fatalf("%s has no field %q; the generated surface changed", st.Name(), field)
	}
	if sf.Type != want {
		t.Errorf("%s.%s is bound to %s, want %s (a scalar binding changed in genqlient.yaml)", st.Name(), field, sf.Type, want)
	}
}

// assertPointerElemFieldType fails the test unless struct type st declares a
// field named field that is a pointer whose element type is exactly want. A
// nullable scalar field is generated as a pointer (optional: pointer), so the
// scalar binding is the pointee type.
func assertPointerElemFieldType(t *testing.T, st reflect.Type, field string, want reflect.Type) {
	t.Helper()
	sf, ok := st.FieldByName(field)
	if !ok {
		t.Fatalf("%s has no field %q; the generated surface changed", st.Name(), field)
	}
	if sf.Type.Kind() != reflect.Pointer {
		t.Fatalf("%s.%s is %s, want a pointer (optional: pointer should make a nullable scalar a pointer)", st.Name(), field, sf.Type)
	}
	if elem := sf.Type.Elem(); elem != want {
		t.Errorf("%s.%s is bound to *%s, want *%s (a scalar binding changed in genqlient.yaml)", st.Name(), field, elem, want)
	}
}

// assertRawMessageRoundTrip exercises a json.RawMessage value round-trip; both
// the Map and Any scalars bind to encoding/json.RawMessage and share this check.
func assertRawMessageRoundTrip(t *testing.T) {
	t.Helper()
	in := json.RawMessage(`{"k":[1,2,3],"nested":{"x":true}}`)
	var out json.RawMessage
	roundTrip(t, in, &out)
	if !json.Valid(out) {
		t.Fatalf("Map/Any round-trip produced invalid JSON: %s", out)
	}
	if !sameJSON(t, in, out) {
		t.Errorf("Map/Any round-trip changed the JSON value: got %s, want %s", out, in)
	}
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
