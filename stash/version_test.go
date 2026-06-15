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
	var gqlErr *GraphQLError
	if !errors.As(err, &gqlErr) {
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
	var te *TransportError
	if !errors.As(err, &te) {
		t.Errorf("want *TransportError, got %T", err)
	}
}
