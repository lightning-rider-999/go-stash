package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/lightning-rider-999/go-stashapp/stash"
)

// TestExitCodeTaxonomyFrozen guards the (name, integer) pairs against an
// accidental renumber. The catalog and agents depend on these exact values.
func TestExitCodeTaxonomyFrozen(t *testing.T) {
	want := []ExitCode{
		{"ok", 0}, {"internal", 1}, {"usage", 2}, {"auth", 3},
		{"transport", 4}, {"validation", 5}, {"server-fault", 6},
		{"not-found", 7}, {"destructive-refused", 8}, {"job-failed", 9},
		{"still-running", 10}, {"unconfirmed", 11},
	}
	got := []ExitCode{
		ExitOK, ExitInternal, ExitUsage, ExitAuth,
		ExitTransport, ExitValidation, ExitServerFault,
		ExitNotFound, ExitDestructiveRefused, ExitJobFailed,
		ExitStillRunning, ExitUnconfirmed,
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("taxonomy[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

// gqlError builds a *stash.GraphQLError carrying one message.
func gqlError(msg string) *stash.GraphQLError {
	return &stash.GraphQLError{Errors: gqlerror.List{{Message: msg}}}
}

// TestClassifyExit covers every classification path the SDK error model feeds.
func TestClassifyExit(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ExitCode
	}{
		{"nil is ok", nil, ExitOK},
		{"usage", newUsageError(fmt.Errorf("unknown flag --bogus")), ExitUsage},
		{"auth via sentinel", fmt.Errorf("denied: %w", stash.ErrUnauthorized), ExitAuth},
		{"auth wins over gql shape", fmt.Errorf("%w: %w", gqlError("forbidden"), stash.ErrUnauthorized), ExitAuth},
		{"sdk transport", &stash.TransportError{StatusCode: 503}, ExitTransport},
		{"cli transport", &transportError{statusCode: 500}, ExitTransport},
		{"not found", gqlError("scene not found"), ExitNotFound},
		{"does not exist", gqlError("performer with id 9 does not exist"), ExitNotFound},
		{"validation invalid", gqlError("title is invalid"), ExitValidation},
		{"validation must be", gqlError("rating100 must be between 0 and 100"), ExitValidation},
		{"generic server fault", gqlError("internal database error"), ExitServerFault},
		{"no-url is usage", fmt.Errorf("build client: %w", stash.ErrNoURL), ExitUsage},
		{"unknown is internal", fmt.Errorf("something odd"), ExitInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyExit(tc.err); got != tc.want {
				t.Errorf("classifyExit = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestErrorEnvelopeShape asserts the JSON envelope on stderr names the taxonomy
// code, carries the message, and lists the GraphQL error messages.
func TestErrorEnvelopeShape(t *testing.T) {
	var buf bytes.Buffer
	err := gqlError("scene not found")
	code := classifyExit(err)
	writeErrorEnvelope(&buf, code, err)

	// One compact line, newline-terminated.
	line := buf.Bytes()
	if n := bytes.Count(line, []byte("\n")); n != 1 || line[len(line)-1] != '\n' {
		t.Errorf("envelope is not a single newline-terminated line: %q", buf.String())
	}

	var env struct {
		Code          string   `json:"code"`
		Message       string   `json:"message"`
		GraphQLErrors []string `json:"graphqlErrors"`
		Retryable     bool     `json:"retryable"`
	}
	if err := json.Unmarshal(line, &env); err != nil {
		t.Fatalf("envelope not JSON: %v\n%s", err, buf.String())
	}
	if env.Code != "not-found" {
		t.Errorf("code = %q, want not-found", env.Code)
	}
	if env.Message == "" {
		t.Error("message is empty")
	}
	if len(env.GraphQLErrors) != 1 || env.GraphQLErrors[0] != "scene not found" {
		t.Errorf("graphqlErrors = %v, want [scene not found]", env.GraphQLErrors)
	}
}

// TestErrorEnvelopeRedactsAPIKey: an error whose message and GraphQL errors echo
// a pre-signed ?apikey=<JWT> URL must come out of writeErrorEnvelope with the JWT
// scrubbed — the error path on stderr holds the same no-leak invariant as the
// success path on stdout.
func TestErrorEnvelopeRedactsAPIKey(t *testing.T) {
	const jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.sig"
	msgURL := "http://stash.local/scene/42/stream?apikey=" + jwt
	err := &stash.GraphQLError{Errors: gqlerror.List{
		{Message: "fetch failed for " + msgURL},
		{Message: "retry against " + msgURL + " also failed"},
	}}

	var buf bytes.Buffer
	writeErrorEnvelope(&buf, classifyExit(err), err)
	s := buf.String()

	if bytes.Contains(buf.Bytes(), []byte(jwt)) {
		t.Errorf("error envelope leaked the JWT:\n%s", s)
	}
	if !bytes.Contains(buf.Bytes(), []byte("apikey=REDACTED")) {
		t.Errorf("error envelope is missing apikey=REDACTED:\n%s", s)
	}
	// The surrounding message text and URL path must survive.
	if !bytes.Contains(buf.Bytes(), []byte("http://stash.local/scene/42/stream")) {
		t.Errorf("redaction mangled the URL in the envelope:\n%s", s)
	}
}

// TestErrorEnvelopeRedactsBareSecretFields: a non-2xx transport error whose body
// is echoed into the message as bare JSON secret pairs ("apiKey"/"api_key"/
// "password") must come out of writeErrorEnvelope with every secret value
// scrubbed. The URL-only pass (redact.APIKeysInText) never caught these — only
// redact.Message does — so this is the regression guard for redact-3: a server
// error body carrying inline credentials must not reach stderr.
func TestErrorEnvelopeRedactsBareSecretFields(t *testing.T) {
	const (
		secretCamel = "sk-camelCASEsecret123"
		secretSnake = "sk-snake_case_secret_456"
		secretPass  = "hunter2-very-secret"
	)
	// A realistic non-2xx body the server might echo back, carrying every casing
	// convention redact.Message normalises (camelCase, snake_case, password).
	body := `{"apiKey":"` + secretCamel + `","api_key":"` + secretSnake + `","password":"` + secretPass + `"}`
	// Build the SDK transport type via its public constructor (status 502), so the
	// secret-bearing body rides into env.Message exactly as the live path would.
	err := stash.NewTransportError(http.StatusBadGateway, fmt.Errorf("server rejected request: %s", body))

	var buf bytes.Buffer
	writeErrorEnvelope(&buf, classifyExit(err), err)
	s := buf.String()

	for name, secret := range map[string]string{
		"apiKey":   secretCamel,
		"api_key":  secretSnake,
		"password": secretPass,
	} {
		if bytes.Contains(buf.Bytes(), []byte(secret)) {
			t.Errorf("envelope leaked the %s secret %q:\n%s", name, secret, s)
		}
	}
	// The scrub replaces the value with REDACTED rather than dropping the field.
	if !bytes.Contains(buf.Bytes(), []byte("REDACTED")) {
		t.Errorf("envelope is missing the REDACTED sentinel:\n%s", s)
	}
	// And the envelope is still one valid JSON line naming the transport code.
	line := buf.Bytes()
	if n := bytes.Count(line, []byte("\n")); n != 1 || line[len(line)-1] != '\n' {
		t.Errorf("envelope is not a single newline-terminated line:\n%s", s)
	}
	var env struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(line, &env); err != nil {
		t.Fatalf("envelope not JSON: %v\n%s", err, s)
	}
	if env.Code != "transport" {
		t.Errorf("code = %q, want transport", env.Code)
	}
}

// TestExitCodes is the per-path golden for the wired error path: each error
// shape, driven through the live MakeRequest classifier where it has a wire
// form, must yield the right code/integer AND a JSON envelope naming that code.
func TestExitCodes(t *testing.T) {
	cases := []struct {
		name      string
		reply     string
		status    int
		wantCode  string
		wantExit  int
		wantInEnv string
	}{
		{
			name:      "auth",
			reply:     `{"errors":[{"message":"not authenticated","extensions":{"code":"UNAUTHENTICATED"}}]}`,
			status:    200,
			wantCode:  "auth",
			wantExit:  3,
			wantInEnv: `"code":"auth"`,
		},
		{
			name:      "transport",
			reply:     `{"errors":[{"message":"boom"}]}`,
			status:    500,
			wantCode:  "transport",
			wantExit:  4,
			wantInEnv: `"code":"transport"`,
		},
		{
			name:      "not-found",
			reply:     `{"errors":[{"message":"scene not found"}]}`,
			status:    200,
			wantCode:  "not-found",
			wantExit:  7,
			wantInEnv: `"code":"not-found"`,
		},
		{
			name:      "server-fault",
			reply:     `{"errors":[{"message":"panic: nil pointer"}]}`,
			status:    200,
			wantCode:  "server-fault",
			wantExit:  6,
			wantInEnv: `"code":"server-fault"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := newFakeServerStatus(t, tc.status, tc.reply)
			c := fs.client(t)

			spec := commandSpec{
				Path:   []string{"scene", "get"},
				OpName: "FindScene",
				Query:  stash.FindScene_Operation,
				Kind:   "query",
			}
			var out bytes.Buffer
			err := runOperation(context.Background(), c, spec, nil, "json", &out)
			if err == nil {
				t.Fatal("expected an error from runOperation")
			}

			code := classifyExit(err)
			if code.Name != tc.wantCode || code.Code != tc.wantExit {
				t.Errorf("classifyExit = %+v, want {%s %d}", code, tc.wantCode, tc.wantExit)
			}

			var env bytes.Buffer
			writeErrorEnvelope(&env, code, err)
			if !bytes.Contains(env.Bytes(), []byte(tc.wantInEnv)) {
				t.Errorf("envelope %q missing %q", env.String(), tc.wantInEnv)
			}
		})
	}

	t.Run("success exits 0", func(t *testing.T) {
		fs := newFakeServer(t, `{"data":{"findScene":{"id":"1"}}}`)
		c := fs.client(t)
		spec := commandSpec{
			Path:       []string{"scene", "get"},
			OpName:     "FindScene",
			Query:      stash.FindScene_Operation,
			Kind:       "query",
			ReturnType: "Scene",
		}
		var out bytes.Buffer
		if err := runOperation(context.Background(), c, spec, nil, "json", &out); err != nil {
			t.Fatalf("runOperation: %v", err)
		}
		if got := classifyExit(nil); got != ExitOK {
			t.Errorf("success code = %+v, want ok/0", got)
		}
	})
}
