package stash

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lightning-rider-999/go-stashapp/schema"
)

func versionServer(t *testing.T, status int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(WithURL(srv.URL), WithAPIKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestVersionMapping(t *testing.T) {
	c := versionServer(t, 200, `{"data":{"version":{"version":"v0.31.1","hash":"deadbeef","build_time":"2025-01-02T03:04:05Z"}}}`)
	info, err := c.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "v0.31.1" {
		t.Errorf("Version = %q, want v0.31.1", info.Version)
	}
	if info.Hash != "deadbeef" {
		t.Errorf("Hash = %q, want deadbeef", info.Hash)
	}
	if info.BuildTime != "2025-01-02T03:04:05Z" {
		t.Errorf("BuildTime = %q, want the build_time field", info.BuildTime)
	}
}

func TestVersionNilPayload(t *testing.T) {
	// version may come back null; that is an error, not a panic.
	c := versionServer(t, 200, `{"data":{"version":null}}`)
	if _, err := c.Version(context.Background()); err == nil {
		t.Fatal("want error for null version payload")
	}
}

func TestVersionGraphQLError(t *testing.T) {
	c := versionServer(t, 200, `{"data":null,"errors":[{"message":"boom"}]}`)
	_, err := c.Version(context.Background())
	if _, ok := errors.AsType[*GraphQLError](err); !ok {
		t.Fatalf("want *GraphQLError, got %T", err)
	}
}

func TestCheckCompatibilityMatch(t *testing.T) {
	body := fmt.Sprintf(`{"data":{"version":{"version":%q,"hash":"h","build_time":"b"}}}`, schema.SchemaVersion)
	c := versionServer(t, 200, body)
	compatible, server, err := c.CheckCompatibility(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !compatible {
		t.Errorf("compatible = false for matching version %q", schema.SchemaVersion)
	}
	if server == nil || server.Version != schema.SchemaVersion {
		t.Errorf("server info = %+v, want version %q", server, schema.SchemaVersion)
	}
}

func TestCheckCompatibilityMismatchNotError(t *testing.T) {
	c := versionServer(t, 200, `{"data":{"version":{"version":"v9.99.0","hash":"h","build_time":"b"}}}`)
	compatible, server, err := c.CheckCompatibility(context.Background())
	if err != nil {
		t.Fatalf("a version mismatch must not be an error, got %v", err)
	}
	if compatible {
		t.Error("compatible = true for mismatched version v9.99.0")
	}
	if server == nil || server.Version != "v9.99.0" {
		t.Errorf("server info = %+v, want the reported version", server)
	}
}

func TestCheckCompatibilityTransportError(t *testing.T) {
	c := versionServer(t, http.StatusInternalServerError, `{"errors":[{"message":"down"}]}`)
	compatible, _, err := c.CheckCompatibility(context.Background())
	if err == nil {
		t.Fatal("a transport failure must surface as an error")
	}
	if compatible {
		t.Error("compatible = true despite a transport error")
	}
	if _, ok := errors.AsType[*TransportError](err); !ok {
		t.Errorf("want *TransportError, got %T", err)
	}
}

// versionBody builds a Version response whose version field is the given JSON
// literal (use "null" for an absent tag, or a quoted string for a value).
func versionBody(versionLiteral string) string {
	return fmt.Sprintf(`{"data":{"version":{"version":%s,"hash":"h","build_time":"b"}}}`, versionLiteral)
}

// TestCompatibilitySemver exercises the semantic-version comparison: v-prefix
// tolerance, patch-drift compatibility, minor/major drift with direction, and
// the unknown case for an empty or dev/hash-only server version. The schema this
// build was generated against is v0.31.1.
func TestCompatibilitySemver(t *testing.T) {
	tests := []struct {
		name           string
		serverVersion  string // value of the version field, "null" or a quoted literal
		wantCompatible bool
		wantRelation   VersionRelation
	}{
		{
			name:           "exact match",
			serverVersion:  `"v0.31.1"`,
			wantCompatible: true,
			wantRelation:   VersionEqual,
		},
		{
			name:           "missing v prefix still matches",
			serverVersion:  `"0.31.1"`,
			wantCompatible: true,
			wantRelation:   VersionEqual,
		},
		{
			name:           "patch drift is compatible",
			serverVersion:  `"v0.31.9"`,
			wantCompatible: true,
			wantRelation:   VersionEqual,
		},
		{
			name:           "older patch is compatible",
			serverVersion:  `"v0.31.0"`,
			wantCompatible: true,
			wantRelation:   VersionEqual,
		},
		{
			name:           "minor ahead is server-newer and incompatible",
			serverVersion:  `"v0.32.0"`,
			wantCompatible: false,
			wantRelation:   VersionServerNewer,
		},
		{
			name:           "minor behind is server-older and incompatible",
			serverVersion:  `"v0.30.5"`,
			wantCompatible: false,
			wantRelation:   VersionServerOlder,
		},
		{
			name:           "major ahead is server-newer",
			serverVersion:  `"v1.0.0"`,
			wantCompatible: false,
			wantRelation:   VersionServerNewer,
		},
		{
			name:           "null version is unknown",
			serverVersion:  `null`,
			wantCompatible: false,
			wantRelation:   VersionUnknown,
		},
		{
			name:           "empty version is unknown",
			serverVersion:  `""`,
			wantCompatible: false,
			wantRelation:   VersionUnknown,
		},
		{
			name:           "dev hash-only version is unknown",
			serverVersion:  `"3f8a1c9-dev"`,
			wantCompatible: false,
			wantRelation:   VersionUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := versionServer(t, http.StatusOK, versionBody(tt.serverVersion))
			result, err := c.Compatibility(context.Background())
			if err != nil {
				t.Fatalf("Compatibility: %v", err)
			}
			if result.Compatible != tt.wantCompatible {
				t.Errorf("Compatible = %v, want %v", result.Compatible, tt.wantCompatible)
			}
			if result.Relation != tt.wantRelation {
				t.Errorf("Relation = %v, want %v", result.Relation, tt.wantRelation)
			}
			if result.SchemaVersion != schema.SchemaVersion {
				t.Errorf("SchemaVersion = %q, want %q", result.SchemaVersion, schema.SchemaVersion)
			}
			if result.Server == nil {
				t.Fatal("Server info should be populated")
			}

			// CheckCompatibility must agree on the boolean.
			compatible, server, err := c.CheckCompatibility(context.Background())
			if err != nil {
				t.Fatalf("CheckCompatibility: %v", err)
			}
			if compatible != tt.wantCompatible {
				t.Errorf("CheckCompatibility compatible = %v, want %v", compatible, tt.wantCompatible)
			}
			if server == nil {
				t.Error("CheckCompatibility server should be populated")
			}
		})
	}
}

func TestNormalizeSemver(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"v0.31.1", "v0.31.1"},
		{"0.31.1", "v0.31.1"},
		{"  v0.31.1  ", "v0.31.1"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := normalizeSemver(tt.in); got != tt.want {
			t.Errorf("normalizeSemver(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestVersionRelationString(t *testing.T) {
	cases := map[VersionRelation]string{
		VersionUnknown:     "unknown",
		VersionEqual:       "equal",
		VersionServerNewer: "server-newer",
		VersionServerOlder: "server-older",
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", r, got, want)
		}
	}
}
