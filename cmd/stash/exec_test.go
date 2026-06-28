package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lightning-rider-999/go-stash/stash"
)

// fakeServer stands in for a Stash GraphQL endpoint. It records the last request
// body and replies with the configured response.
type fakeServer struct {
	srv      *httptest.Server
	lastBody []byte
}

func newFakeServer(t *testing.T, reply string) *fakeServer {
	t.Helper()
	return newFakeServerStatus(t, http.StatusOK, reply)
}

// newFakeServerStatus is newFakeServer with an explicit HTTP status, so a test
// can drive the non-2xx transport-error path.
func newFakeServerStatus(t *testing.T, status int, reply string) *fakeServer {
	t.Helper()
	fs := &fakeServer{}
	fs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.lastBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(fs.srv.Close)
	return fs
}

func (fs *fakeServer) client(t *testing.T) *stash.Client {
	t.Helper()
	c, err := stash.NewClient(stash.WithURL(fs.srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestRunOperationData(t *testing.T) {
	fs := newFakeServer(t, `{"data":{"version":{"version":"v0.31.1"}}}`)
	c := fs.client(t)

	spec := commandSpec{
		Path:       []string{"misc", "version"},
		OpName:     "Version",
		Query:      stash.Version_Operation,
		Kind:       "query",
		ReturnType: "Version",
	}

	var out bytes.Buffer
	if err := runOperation(context.Background(), c, spec, nil, "json", &out, false); err != nil {
		t.Fatalf("runOperation: %v", err)
	}

	// Output is the unwrapped data object, pretty-printed.
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	ver, ok := got["version"].(map[string]any)
	if !ok || ver["version"] != "v0.31.1" {
		t.Errorf("data = %v, want version.version = v0.31.1", got)
	}
	if !strings.Contains(out.String(), "\n") || !strings.Contains(out.String(), "  ") {
		t.Errorf("output is not pretty-printed: %q", out.String())
	}

	// The operation name was forwarded on the wire.
	if !bytes.Contains(fs.lastBody, []byte(`"operationName":"Version"`)) {
		t.Errorf("request body missing operationName: %s", fs.lastBody)
	}
}

func TestRunOperationForwardsVariables(t *testing.T) {
	fs := newFakeServer(t, `{"data":{"findScene":null}}`)
	c := fs.client(t)

	spec := commandSpec{
		Path:       []string{"scene", "get"},
		OpName:     "FindScene",
		Query:      stash.FindScene_Operation,
		Kind:       "query",
		ReturnType: "Scene",
	}

	vars := map[string]json.RawMessage{"id": json.RawMessage(`"42"`)}
	var out bytes.Buffer
	if err := runOperation(context.Background(), c, spec, vars, "json", &out, false); err != nil {
		t.Fatalf("runOperation: %v", err)
	}
	if !bytes.Contains(fs.lastBody, []byte(`"id":"42"`)) {
		t.Errorf("request body missing forwarded variable: %s", fs.lastBody)
	}
}

func TestRunOperationGraphQLError(t *testing.T) {
	fs := newFakeServer(t, `{"errors":[{"message":"scene not found"}]}`)
	c := fs.client(t)

	spec := commandSpec{
		Path:   []string{"scene", "get"},
		OpName: "FindScene",
		Query:  stash.FindScene_Operation,
		Kind:   "query",
	}

	var out bytes.Buffer
	err := runOperation(context.Background(), c, spec, nil, "json", &out, false)
	if err == nil {
		t.Fatal("expected a GraphQL error, got nil")
	}
	// classifyError now preserves the typed SDK error so the exit classifier and
	// the envelope can inspect it, rather than flattening to a formatted string.
	if _, ok := errors.AsType[*stash.GraphQLError](err); !ok {
		t.Fatalf("error = %T (%v), want a *stash.GraphQLError", err, err)
	}
	if !strings.Contains(err.Error(), "scene not found") {
		t.Errorf("error = %q, want the server message preserved", err)
	}
	// And it classifies to not-found via the message heuristic.
	if got := classifyExit(err); got != ExitNotFound {
		t.Errorf("classifyExit = %v, want %v", got, ExitNotFound)
	}
}

// TestRunOperationUsesStashClassify proves the exec path no longer reimplements
// error typing: a non-2xx HTTP status from MakeRequest must come back normalised
// into a *stash.TransportError (the SDK type produced by stash.Classify, not the
// CLI's own fallback transportError), carrying the status code, and classify to
// the transport exit code via that stash type.
func TestRunOperationUsesStashClassify(t *testing.T) {
	fs := newFakeServerStatus(t, http.StatusBadGateway, `{"errors":[{"message":"upstream down"}]}`)
	c := fs.client(t)

	spec := commandSpec{
		Path:   []string{"scene", "get"},
		OpName: "FindScene",
		Query:  stash.FindScene_Operation,
		Kind:   "query",
	}

	var out bytes.Buffer
	err := runOperation(context.Background(), c, spec, nil, "json", &out, false)
	if err == nil {
		t.Fatal("expected a transport error from runOperation")
	}

	// The SDK type, not the CLI fallback, is what stash.Classify yields.
	var te *stash.TransportError
	if !errors.As(err, &te) {
		t.Fatalf("error = %T (%v), want a *stash.TransportError from stash.Classify", err, err)
	}
	if te.StatusCode != http.StatusBadGateway {
		t.Errorf("StatusCode = %d, want %d", te.StatusCode, http.StatusBadGateway)
	}
	// The CLI's own fallback type must NOT appear on this path anymore.
	if _, ok := errors.AsType[*transportError](err); ok {
		t.Error("exec path produced the CLI transportError; stash.Classify should own this typing")
	}
	if got := classifyExit(err); got != ExitTransport {
		t.Errorf("classifyExit = %+v, want %+v", got, ExitTransport)
	}
}

func TestWriteJSONNullData(t *testing.T) {
	var out bytes.Buffer
	if err := writeJSON(&out, nil); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	if strings.TrimSpace(out.String()) != "null" {
		t.Errorf("empty data = %q, want null", out.String())
	}
}

// TestRunOperationAllowPartial covers the core of --allow-partial: an HTTP-200
// response carrying BOTH data and a GraphQL errors array (the non-null-bubble
// shape, e.g. plugins). With the flag the partial data is written to stdout AND
// the classified error is still returned (so the envelope + non-zero exit are
// unchanged); without it, the data is discarded as before.
func TestRunOperationAllowPartial(t *testing.T) {
	body := `{"data":{"findScene":{"id":"42","title":"keep me"}},` +
		`"errors":[{"message":"the requested element is null which the schema does not allow","path":["findScene","studio"]}]}`
	fs := newFakeServer(t, body)
	c := fs.client(t)
	spec := commandSpec{
		Path:       []string{"scene", "get"},
		OpName:     "FindScene",
		Query:      stash.FindScene_Operation,
		Kind:       "query",
		ReturnType: "Scene",
	}

	// allowPartial=true: partial data on stdout, error still returned.
	var out bytes.Buffer
	err := runOperation(context.Background(), c, spec, nil, "json", &out, true)
	if err == nil {
		t.Fatal("allowPartial must still return the GraphQL error, got nil")
	}
	if _, ok := errors.AsType[*stash.GraphQLError](err); !ok {
		t.Fatalf("error = %T (%v), want *stash.GraphQLError", err, err)
	}
	if got := classifyExit(err); got != ExitServerFault {
		t.Errorf("classifyExit = %+v, want %+v", got, ExitServerFault)
	}
	if !strings.Contains(out.String(), `"keep me"`) {
		t.Errorf("partial data not written to stdout: %q", out.String())
	}

	// allowPartial=false (default): nothing on stdout, same error.
	var off bytes.Buffer
	if err := runOperation(context.Background(), c, spec, nil, "json", &off, false); err == nil {
		t.Fatal("expected the GraphQL error without allowPartial")
	}
	if off.Len() != 0 {
		t.Errorf("default path wrote data to stdout: %q", off.String())
	}
}

// TestRunOperationAllowPartialNullData proves the len(data) > 0 guard: a
// top-level non-null bubble nulls the whole payload, so there is nothing to
// surface and --allow-partial is the unchanged failure.
func TestRunOperationAllowPartialNullData(t *testing.T) {
	fs := newFakeServer(t, `{"data":null,"errors":[{"message":"boom"}]}`)
	c := fs.client(t)
	spec := commandSpec{
		Path:   []string{"scene", "get"},
		OpName: "FindScene",
		Query:  stash.FindScene_Operation,
		Kind:   "query",
	}
	var out bytes.Buffer
	err := runOperation(context.Background(), c, spec, nil, "json", &out, true)
	if err == nil {
		t.Fatal("expected the GraphQL error")
	}
	if out.Len() != 0 {
		t.Errorf("allowPartial wrote output for a null-data response: %q", out.String())
	}
}

// TestRunOperationAllowPartialNonHTTP200 proves the flag is inert on a non-2xx:
// genqlient never decodes data there, so there is nothing to surface and the
// transport failure is unchanged.
func TestRunOperationAllowPartialNonHTTP200(t *testing.T) {
	fs := newFakeServerStatus(t, http.StatusBadGateway,
		`{"data":{"findScene":{"id":"1"}},"errors":[{"message":"upstream"}]}`)
	c := fs.client(t)
	spec := commandSpec{
		Path:   []string{"scene", "get"},
		OpName: "FindScene",
		Query:  stash.FindScene_Operation,
		Kind:   "query",
	}
	var out bytes.Buffer
	err := runOperation(context.Background(), c, spec, nil, "json", &out, true)
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if out.Len() != 0 {
		t.Errorf("allowPartial surfaced data on a non-2xx response: %q", out.String())
	}
	var te *stash.TransportError
	if !errors.As(err, &te) {
		t.Fatalf("error = %T, want *stash.TransportError", err)
	}
}

// TestAllowPartialFlagThreadsThroughRoot is the end-to-end proof that the
// persistent --allow-partial flag reaches runOperation through the real cobra
// tree: a 200 data+errors response prints data on stdout and still exits non-zero.
func TestAllowPartialFlagThreadsThroughRoot(t *testing.T) {
	body := `{"data":{"findScene":{"id":"7","title":"survivor"}},` +
		`"errors":[{"message":"the requested element is null which the schema does not allow"}]}`
	fs := newFakeServer(t, body)
	root := buildRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"scene", "get", "--url", fs.srv.URL, "--allow-partial"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected the GraphQL error to still propagate (non-zero exit)")
	}
	if !strings.Contains(out.String(), "survivor") {
		t.Errorf("partial data not on stdout via --allow-partial: %q", out.String())
	}
}
