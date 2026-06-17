package stash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

// importServer returns a Client pointed at a handler that records the multipart
// request it receives, so a test can assert the wire shape of the upload.
func importServer(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := NewClient(WithURL(srv.URL), WithAPIKey("secret-key"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// parsedUpload is the decoded multipart request the test server saw.
type parsedUpload struct {
	apiKey       string
	contentType  string
	operations   map[string]any
	mapPart      map[string][]string
	fileName     string
	fileContents string
}

// readMultipart parses an importObjects multipart request into its parts.
func readMultipart(t *testing.T, r *http.Request) parsedUpload {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parsing Content-Type: %v", err)
	}
	if mediaType != "multipart/form-data" {
		t.Fatalf("media type = %q, want multipart/form-data", mediaType)
	}

	got := parsedUpload{
		apiKey:      r.Header.Get("ApiKey"),
		contentType: r.Header.Get("Content-Type"),
	}

	mr := multipart.NewReader(r.Body, params["boundary"])
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("reading next part: %v", err)
		}
		body, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("reading part %q: %v", part.FormName(), err)
		}
		switch part.FormName() {
		case "operations":
			if err := json.Unmarshal(body, &got.operations); err != nil {
				t.Fatalf("decoding operations part: %v\n%s", err, body)
			}
		case "map":
			if err := json.Unmarshal(body, &got.mapPart); err != nil {
				t.Fatalf("decoding map part: %v\n%s", err, body)
			}
		case "0":
			got.fileName = part.FileName()
			got.fileContents = string(body)
		default:
			t.Fatalf("unexpected part %q", part.FormName())
		}
	}
	return got
}

func TestImportObjects(t *testing.T) {
	const fileBody = "performers,scenes\nfoo,bar\n"

	var seen parsedUpload
	c := importServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen = readMultipart(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"importObjects":"job-42"}}`)
	})

	id, err := c.ImportObjects(context.Background(), ImportObjectsInput{
		File:                Upload{Filename: "export.zip", Body: strings.NewReader(fileBody)},
		DuplicateBehaviour:  ImportDuplicateEnumOverwrite,
		MissingRefBehaviour: ImportMissingRefEnumCreate,
	})
	if err != nil {
		t.Fatalf("ImportObjects: %v", err)
	}
	if id != "job-42" {
		t.Errorf("job id = %q, want job-42", id)
	}

	// Auth header is injected by the existing round-tripper.
	if seen.apiKey != "secret-key" {
		t.Errorf("ApiKey header = %q, want secret-key", seen.apiKey)
	}

	// operations part: query text and the variables shape with file nulled.
	query, _ := seen.operations["query"].(string)
	if !strings.Contains(query, "importObjects(input: $input)") {
		t.Errorf("operations query missing importObjects selection: %q", query)
	}
	if !strings.Contains(query, "$input: ImportObjectsInput!") {
		t.Errorf("operations query missing typed variable: %q", query)
	}
	vars, ok := seen.operations["variables"].(map[string]any)
	if !ok {
		t.Fatalf("variables not an object: %#v", seen.operations["variables"])
	}
	input, ok := vars["input"].(map[string]any)
	if !ok {
		t.Fatalf("variables.input not an object: %#v", vars["input"])
	}
	if got, want := input["duplicateBehaviour"], "OVERWRITE"; got != want {
		t.Errorf("duplicateBehaviour = %v, want %v", got, want)
	}
	if got, want := input["missingRefBehaviour"], "CREATE"; got != want {
		t.Errorf("missingRefBehaviour = %v, want %v", got, want)
	}
	// The file variable must be present and null, per the multipart spec.
	if v, present := input["file"]; !present || v != nil {
		t.Errorf("variables.input.file = %v (present=%v), want present and null", v, present)
	}

	// map part: part "0" points at variables.input.file.
	paths := seen.mapPart["0"]
	if len(paths) != 1 || paths[0] != "variables.input.file" {
		t.Errorf("map[\"0\"] = %v, want [variables.input.file]", paths)
	}

	// file part: name and bytes are carried through.
	if seen.fileName != "export.zip" {
		t.Errorf("file name = %q, want export.zip", seen.fileName)
	}
	if seen.fileContents != fileBody {
		t.Errorf("file contents = %q, want %q", seen.fileContents, fileBody)
	}
}

func TestImportObjectsGraphQLError(t *testing.T) {
	c := importServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = readMultipart(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"errors":[{"message":"import failed: bad file"}]}`)
	})

	_, err := c.ImportObjects(context.Background(), ImportObjectsInput{
		File:                Upload{Filename: "x.zip", Body: strings.NewReader("data")},
		DuplicateBehaviour:  ImportDuplicateEnumFail,
		MissingRefBehaviour: ImportMissingRefEnumFail,
	})
	if err == nil {
		t.Fatal("want a GraphQL error, got nil")
	}
	var gqlErr *GraphQLError
	if !errors.As(err, &gqlErr) {
		t.Fatalf("error %v (%T) does not unwrap to *GraphQLError", err, err)
	}
	if msgs := gqlErr.Messages(); len(msgs) != 1 || !strings.Contains(msgs[0], "import failed") {
		t.Errorf("messages = %v, want one mentioning import failed", msgs)
	}
}

func TestImportObjectsUnauthorized(t *testing.T) {
	c := importServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = readMultipart(t, r)
		w.WriteHeader(http.StatusUnauthorized)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"errors":[{"message":"not authenticated"}]}`)
	})

	_, err := c.ImportObjects(context.Background(), ImportObjectsInput{
		File:                Upload{Filename: "x.zip", Body: strings.NewReader("data")},
		DuplicateBehaviour:  ImportDuplicateEnumIgnore,
		MissingRefBehaviour: ImportMissingRefEnumIgnore,
	})
	if err == nil {
		t.Fatal("want an error for a 401, got nil")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("error %v is not ErrUnauthorized", err)
	}
	var te *TransportError
	if !errors.As(err, &te) {
		t.Fatalf("error %v (%T) does not unwrap to *TransportError", err, err)
	}
	if te.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", te.StatusCode)
	}
}

func TestImportObjectsServerError(t *testing.T) {
	c := importServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = readMultipart(t, r)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom")
	})

	_, err := c.ImportObjects(context.Background(), ImportObjectsInput{
		File:                Upload{Filename: "x.zip", Body: strings.NewReader("data")},
		DuplicateBehaviour:  ImportDuplicateEnumIgnore,
		MissingRefBehaviour: ImportMissingRefEnumIgnore,
	})
	if err == nil {
		t.Fatal("want an error for a 500, got nil")
	}
	var te *TransportError
	if !errors.As(err, &te) {
		t.Fatalf("error %v (%T) does not unwrap to *TransportError", err, err)
	}
	if te.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", te.StatusCode)
	}
}

// streamErrReader fails partway through, to prove the body is streamed and a
// read failure surfaces rather than being swallowed.
type streamErrReader struct{ msg string }

func (r *streamErrReader) Read([]byte) (int, error) {
	return 0, fmt.Errorf("%s", r.msg)
}

func TestImportObjectsBodyReadError(t *testing.T) {
	c := importServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Drain whatever arrives; the client side should fail before a useful
		// response is needed.
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"importObjects":"unreachable"}}`)
	})

	_, err := c.ImportObjects(context.Background(), ImportObjectsInput{
		File:                Upload{Filename: "x.zip", Body: &streamErrReader{msg: "disk gone"}},
		DuplicateBehaviour:  ImportDuplicateEnumIgnore,
		MissingRefBehaviour: ImportMissingRefEnumIgnore,
	})
	if err == nil {
		t.Fatal("want an error when the file body read fails, got nil")
	}
	if !strings.Contains(err.Error(), "disk gone") {
		t.Errorf("error %v does not mention the underlying read failure", err)
	}
}

// TestImportObjectsRequestBuildFailureReleasesProducer proves Finding D: when
// http.NewRequestWithContext fails, ImportObjects must close the body pipe so the
// goroutine streaming the multipart body unblocks and exits rather than leaking.
//
// io.Pipe is synchronous: with no consumer, the producer blocks on its very
// first pipe write (the operations part). The fix's body.CloseWithError makes
// that pending write return an error, so writeImportParts returns and the
// goroutine exits. The test confirms the goroutine count returns to the
// pre-call baseline; without the fix it stays one higher forever.
func TestImportObjectsRequestBuildFailureReleasesProducer(t *testing.T) {
	c, err := NewClient(WithURL("http://example.invalid"), WithAPIKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	// Force http.NewRequestWithContext to fail: an endpoint with a control
	// character is rejected with "invalid control character in URL". The field is
	// unexported, set directly from this in-package test.
	c.endpoint = "http://example.invalid/\x7f"

	// Settle, then sample the baseline goroutine count just before the call.
	settle()
	before := runtime.NumGoroutine()

	_, err = c.ImportObjects(context.Background(), ImportObjectsInput{
		File:                Upload{Filename: "x.zip", Body: strings.NewReader("payload")},
		DuplicateBehaviour:  ImportDuplicateEnumIgnore,
		MissingRefBehaviour: ImportMissingRefEnumIgnore,
	})
	if err == nil {
		t.Fatal("want a request-build error, got nil")
	}
	if _, ok := errors.AsType[*TransportError](err); !ok {
		t.Fatalf("error %v (%T) does not unwrap to *TransportError", err, err)
	}

	// The producer goroutine must have exited: the pipe close released its first
	// write. Poll until the count returns to baseline; a persistent +1 is the leak.
	deadline := time.Now().Add(2 * time.Second)
	for {
		settle()
		if runtime.NumGoroutine() <= before {
			return // producer released and exited
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine count stayed at %d (baseline %d): the body-writer goroutine leaked",
				runtime.NumGoroutine(), before)
		}
	}
}

// settle nudges the scheduler so a goroutine that has just been released has a
// chance to run to completion before the count is sampled.
func settle() {
	for range 5 {
		runtime.Gosched()
		time.Sleep(5 * time.Millisecond)
	}
}

// TestUploadMarshalJSONErrors proves the Upload scalar refuses to be
// JSON-encoded. The generated ImportObjects operation marshals its variables,
// which would encode an Upload as its exported fields only and silently drop the
// file bytes (Body is an io.Reader interface; json.Marshal of, e.g., a
// strings.Reader yields "{}"). A failing MarshalJSON turns that broken path into
// a loud error.
func TestUploadMarshalJSONErrors(t *testing.T) {
	u := Upload{Filename: "x.zip", Body: strings.NewReader("payload")}

	// Direct call.
	if _, err := u.MarshalJSON(); !errors.Is(err, ErrUploadNotJSONEncodable) {
		t.Fatalf("Upload.MarshalJSON err = %v, want ErrUploadNotJSONEncodable", err)
	}

	// Via json.Marshal, both directly and nested in the generated input struct,
	// so the generated ImportObjects path (which marshals ImportObjectsInput)
	// fails loudly instead of uploading no bytes.
	if _, err := json.Marshal(u); !errors.Is(err, ErrUploadNotJSONEncodable) {
		t.Errorf("json.Marshal(Upload) err = %v, want ErrUploadNotJSONEncodable", err)
	}
	in := ImportObjectsInput{
		File:                u,
		DuplicateBehaviour:  ImportDuplicateEnumIgnore,
		MissingRefBehaviour: ImportMissingRefEnumIgnore,
	}
	if _, err := json.Marshal(in); !errors.Is(err, ErrUploadNotJSONEncodable) {
		t.Errorf("json.Marshal(ImportObjectsInput) err = %v, want ErrUploadNotJSONEncodable", err)
	}
}

// TestImportObjectsNonJSONErrorBodyCarriesBody proves the decode fallback: a
// non-2xx response whose body is not JSON (a gateway page, a bare string) must
// surface the raw body in the error rather than yielding an empty errors array
// and a status-only message that drops every clue.
func TestImportObjectsNonJSONErrorBody(t *testing.T) {
	const gatewayBody = "<html><body>502 Bad Gateway from upstream proxy</body></html>"
	c := importServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, gatewayBody)
	})

	_, err := c.ImportObjects(context.Background(), ImportObjectsInput{
		File:                Upload{Filename: "x.zip", Body: strings.NewReader("data")},
		DuplicateBehaviour:  ImportDuplicateEnumIgnore,
		MissingRefBehaviour: ImportMissingRefEnumIgnore,
	})
	if err == nil {
		t.Fatal("want an error for a non-JSON 502 body, got nil")
	}
	var te *TransportError
	if !errors.As(err, &te) {
		t.Fatalf("error %v (%T) does not unwrap to *TransportError", err, err)
	}
	if te.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", te.StatusCode)
	}
	if !strings.Contains(err.Error(), "502 Bad Gateway from upstream proxy") {
		t.Errorf("error %v does not carry the raw gateway body", err)
	}
}

// TestImportObjectsEmptyJobID proves Finding E: a 200 that decodes with neither
// errors nor a job id must be a non-nil error, not a silent empty-id success.
func TestImportObjectsEmptyJobID(t *testing.T) {
	c := importServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = readMultipart(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{}}`)
	})

	id, err := c.ImportObjects(context.Background(), ImportObjectsInput{
		File:                Upload{Filename: "x.zip", Body: strings.NewReader("data")},
		DuplicateBehaviour:  ImportDuplicateEnumIgnore,
		MissingRefBehaviour: ImportMissingRefEnumIgnore,
	})
	if err == nil {
		t.Fatalf("want an error for a 200 with no job id, got id=%q nil error", id)
	}
	if id != "" {
		t.Errorf("job id = %q, want empty on error", id)
	}
	if _, ok := errors.AsType[*TransportError](err); !ok {
		t.Fatalf("error %v (%T) does not unwrap to *TransportError", err, err)
	}
	if !strings.Contains(err.Error(), "no import job id") {
		t.Errorf("error %v does not mention the missing job id", err)
	}
}
