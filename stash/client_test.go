package stash

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientEndpointNormalisation(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantURL string
		wantWS  string
		wantErr bool
	}{
		{"base host adds graphql", "http://stash.local:9999", "http://stash.local:9999/graphql", "ws://stash.local:9999/graphql", false},
		{"root path adds graphql", "http://stash.local:9999/", "http://stash.local:9999/graphql", "ws://stash.local:9999/graphql", false},
		{"already graphql kept", "http://stash.local:9999/graphql", "http://stash.local:9999/graphql", "ws://stash.local:9999/graphql", false},
		{"trailing slash on graphql kept", "http://stash.local:9999/graphql/", "http://stash.local:9999/graphql", "ws://stash.local:9999/graphql", false},
		{"https maps to wss", "https://stash.example.com", "https://stash.example.com/graphql", "wss://stash.example.com/graphql", false},
		{"custom subpath preserved", "http://host/stash", "http://host/stash/graphql", "ws://host/stash/graphql", false},
		// ws/wss inputs: the GraphQL endpoint folds to http/https; the WebSocket URL keeps ws/wss.
		{"ws folds to http for endpoint", "ws://stash.local:9999", "http://stash.local:9999/graphql", "ws://stash.local:9999/graphql", false},
		{"wss folds to https for endpoint", "wss://stash.example.com", "https://stash.example.com/graphql", "wss://stash.example.com/graphql", false},
		{"ws with subpath folds", "ws://host/stash", "http://host/stash/graphql", "ws://host/stash/graphql", false},
		// Query and fragment are meaningless for a GraphQL POST; they are stripped from both URLs.
		{"query stripped from endpoint", "http://stash.local:9999/?foo=bar", "http://stash.local:9999/graphql", "ws://stash.local:9999/graphql", false},
		{"fragment stripped from endpoint", "http://stash.local:9999/graphql#frag", "http://stash.local:9999/graphql", "ws://stash.local:9999/graphql", false},
		{"query and fragment stripped", "http://host/stash?a=1&b=2#top", "http://host/stash/graphql", "ws://host/stash/graphql", false},
		// Surrounding whitespace is trimmed before parsing.
		{"leading and trailing whitespace trimmed", "  http://stash.local:9999  ", "http://stash.local:9999/graphql", "ws://stash.local:9999/graphql", false},
		{"newline whitespace trimmed", "\thttp://stash.local:9999\n", "http://stash.local:9999/graphql", "ws://stash.local:9999/graphql", false},
		{"no scheme rejected", "stash.local:9999", "", "", true},
		{"empty rejected", "", "", "", true},
		{"whitespace-only rejected", "   ", "", "", true},
		{"no host rejected", "http://", "", "", true},
		{"unsupported scheme rejected", "ftp://stash.local:9999", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewClient(WithURL(tc.raw), WithAPIKey("k"))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error for %q, got none", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := c.Endpoint(); got != tc.wantURL {
				t.Errorf("Endpoint() = %q, want %q", got, tc.wantURL)
			}
			if got := c.WebSocketURL(); got != tc.wantWS {
				t.Errorf("WebSocketURL() = %q, want %q", got, tc.wantWS)
			}
		})
	}
}

func TestClientInjectsAPIKeyHeader(t *testing.T) {
	var gotKey, gotPath, gotMethod string
	var seen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		gotKey = r.Header.Get("ApiKey")
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"version":{"version":"v0.31.1","hash":"abc","build_time":"now"}}}`)
	}))
	defer srv.Close()

	c, err := NewClient(WithURL(srv.URL), WithAPIKey("secret-key-123"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Version(context.Background(), c.GraphQL()); err != nil {
		t.Fatalf("Version: %v", err)
	}
	if seen != 1 {
		t.Fatalf("server saw %d requests, want 1", seen)
	}
	if gotKey != "secret-key-123" {
		t.Errorf("ApiKey header = %q, want %q", gotKey, "secret-key-123")
	}
	if gotPath != "/graphql" {
		t.Errorf("request path = %q, want /graphql", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
}

func TestClientNoAPIKeyOmitsHeader(t *testing.T) {
	var hadKey bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadKey = r.Header["Apikey"]
		if v := r.Header.Get("ApiKey"); v != "" {
			hadKey = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"version":{"version":"v","hash":"h","build_time":"b"}}}`)
	}))
	defer srv.Close()

	c, err := NewClient(WithURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Version(context.Background(), c.GraphQL()); err != nil {
		t.Fatal(err)
	}
	if hadKey {
		t.Error("ApiKey header present when no key configured")
	}
}

func TestClientWithHTTPClientPreservesTimeout(t *testing.T) {
	custom := &http.Client{Timeout: 7 * time.Second}
	c, err := NewClient(WithURL("http://h/graphql"), WithAPIKey("k"), WithHTTPClient(custom))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.HTTPClient().Timeout; got != 7*time.Second {
		t.Errorf("timeout = %v, want 7s (WithHTTPClient must not be overridden)", got)
	}
	// The returned client must still inject the ApiKey via a wrapped transport.
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("ApiKey")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"version":{"version":"v","hash":"h","build_time":"b"}}}`)
	}))
	defer srv.Close()
	c2, err := NewClient(WithURL(srv.URL), WithAPIKey("wrapped-key"), WithHTTPClient(&http.Client{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Version(context.Background(), c2.GraphQL()); err != nil {
		t.Fatal(err)
	}
	if gotKey != "wrapped-key" {
		t.Errorf("ApiKey header = %q via WithHTTPClient, want wrapped-key", gotKey)
	}
}

func TestClientDefaultTimeout(t *testing.T) {
	c, err := NewClient(WithURL("http://h"), WithAPIKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPClient().Timeout == 0 {
		t.Error("default http client has no timeout; want a bounded default")
	}
	c2, err := NewClient(WithURL("http://h"), WithAPIKey("k"), WithTimeout(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if got := c2.HTTPClient().Timeout; got != 3*time.Second {
		t.Errorf("WithTimeout = %v, want 3s", got)
	}
}

func TestClientEnvFallback(t *testing.T) {
	t.Setenv("STASHAPP_URL", "http://env.host:1234")
	t.Setenv("STASHAPP_API_KEY", "env-key")
	c, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Endpoint(); got != "http://env.host:1234/graphql" {
		t.Errorf("Endpoint from env = %q", got)
	}
	if got := c.APIKey(); got != "env-key" {
		t.Errorf("APIKey from env = %q", got)
	}
	// Explicit option must win over env.
	c2, err := NewClient(WithURL("http://explicit.host"), WithAPIKey("explicit"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c2.Endpoint(); got != "http://explicit.host/graphql" {
		t.Errorf("explicit option did not win: %q", got)
	}
	if got := c2.APIKey(); got != "explicit" {
		t.Errorf("explicit key did not win: %q", got)
	}
}

func TestClientMissingURLErrors(t *testing.T) {
	t.Setenv("STASHAPP_URL", "")
	t.Setenv("STASHAPP_API_KEY", "")
	_, err := NewClient()
	if err == nil {
		t.Fatal("want error when no URL configured")
	}
	// The missing-URL condition is a configuration mistake the caller can fix, not
	// an opaque failure: it must be a recognisable sentinel so the CLI can map it
	// to a usage exit code and a library caller can errors.Is it.
	if !errors.Is(err, ErrNoURL) {
		t.Errorf("error %v is not ErrNoURL", err)
	}
}

func TestClientLoggerNeverNil(t *testing.T) {
	c, err := NewClient(WithURL("http://h"), WithAPIKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Logger() == nil {
		t.Fatal("Logger() returned nil")
	}
	custom := slog.New(slog.NewTextHandler(nil, nil))
	_ = custom
}

func TestClientLogValueRedactsAPIKey(t *testing.T) {
	const secret = "TOP-SECRET-KEY-DO-NOT-LEAK"
	c, err := NewClient(WithURL("http://stash.local"), WithAPIKey(secret))
	if err != nil {
		t.Fatal(err)
	}
	// Render exactly how slog would when c is passed as an attr value.
	rendered := fmt.Sprintf("%v", c.LogValue())
	if strings.Contains(rendered, secret) {
		t.Fatalf("api key leaked into log value: %q", rendered)
	}
	if !strings.Contains(rendered, "REDACTED") {
		t.Errorf("expected REDACTED marker in %q", rendered)
	}

	// And through a real slog handler, the key must never appear.
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))
	log.Info("client", "stash", c)
	if strings.Contains(buf.String(), secret) {
		t.Fatalf("api key leaked through slog handler: %q", buf.String())
	}
}

func TestClientLogValueMasksUserinfo(t *testing.T) {
	const password = "hunter2-do-not-leak"
	c, err := NewClient(WithURL("http://admin:"+password+"@stash.local:9999"), WithAPIKey("k"))
	if err != nil {
		t.Fatal(err)
	}

	// The endpoint itself still carries the credential (it must, to authenticate),
	// but LogValue must mask the whole userinfo component before logging.
	if !strings.Contains(c.Endpoint(), password) {
		t.Fatalf("test precondition: endpoint should carry the password, got %q", c.Endpoint())
	}

	rendered := fmt.Sprintf("%v", c.LogValue())
	if strings.Contains(rendered, password) {
		t.Fatalf("password leaked into log value: %q", rendered)
	}
	if strings.Contains(rendered, "admin") {
		t.Errorf("username leaked into log value: %q", rendered)
	}
	if !strings.Contains(rendered, "xxxxx@stash.local:9999") {
		t.Errorf("expected masked userinfo in %q", rendered)
	}

	// Through a real slog handler, neither the username nor the password appears.
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))
	log.Info("client", "stash", c)
	if strings.Contains(buf.String(), password) {
		t.Fatalf("password leaked through slog handler: %q", buf.String())
	}
}
