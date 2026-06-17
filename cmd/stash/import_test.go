package main

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// importCapture records what an import-objects command actually sent to the
// server, so a test can prove the CLI used the multipart upload path (carrying
// the file bytes) rather than the generic JSON dispatch (which cannot).
type importCapture struct {
	hits         atomic.Int64
	contentType  string
	fileContents string
	operations   string
}

// newImportServer answers every request with a fixed import job id and records
// the multipart parts of the request it saw.
func newImportServer(t *testing.T, cap *importCapture) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.hits.Add(1)
		cap.contentType = r.Header.Get("Content-Type")
		if mediaType, params, err := mime.ParseMediaType(cap.contentType); err == nil && strings.HasPrefix(mediaType, "multipart/") {
			mr := multipart.NewReader(r.Body, params["boundary"])
			for {
				part, perr := mr.NextPart()
				if perr != nil {
					break
				}
				body, _ := io.ReadAll(part)
				switch part.FormName() {
				case "0":
					cap.fileContents = string(body)
				case "operations":
					cap.operations = string(body)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"importObjects":"job-77"}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runRootOut drives the whole root command for argv and returns the classified
// exit code plus captured stdout, exercising the same wiring the binary uses.
func runRootOut(t *testing.T, argv ...string) (ExitCode, string) {
	t.Helper()
	root := buildRootCommand()
	var out bytes.Buffer
	root.SetArgs(argv)
	root.SetOut(&out)
	root.SetErr(discard{})
	if err := root.Execute(); err != nil {
		return classifyExit(err), out.String()
	}
	return ExitOK, out.String()
}

// TestImportObjectsCommandUploadsFile proves the import-objects CLI command can
// actually upload a file: it routes through the multipart Client.ImportObjects
// path (Content-Type multipart/form-data, file bytes carried) rather than the
// generic JSON dispatch, which cannot express the required file: Upload! input.
func TestImportObjectsCommandUploadsFile(t *testing.T) {
	const body = "performers,scenes\nfoo,bar\n"
	path := filepath.Join(t.TempDir(), "export.zip")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	var capture importCapture
	srv := newImportServer(t, &capture)

	code, out := runRootOut(t, "misc", "import-objects",
		"--url", srv.URL,
		"--file", path,
		"--duplicate-behaviour", "OVERWRITE",
		"--missing-ref-behaviour", "CREATE")

	if code != ExitOK {
		t.Fatalf("exit = %+v, want OK", code)
	}
	if !strings.HasPrefix(capture.contentType, "multipart/form-data") {
		t.Errorf("Content-Type = %q, want multipart/form-data (the CLI must use the upload path, not JSON)", capture.contentType)
	}
	if capture.fileContents != body {
		t.Errorf("uploaded file = %q, want %q", capture.fileContents, body)
	}
	if !strings.Contains(capture.operations, "OVERWRITE") || !strings.Contains(capture.operations, "CREATE") {
		t.Errorf("operations part missing the chosen behaviours: %s", capture.operations)
	}
	if !strings.Contains(out, "job-77") {
		t.Errorf("stdout = %q, want the returned import job id", out)
	}
}

// TestImportObjectsCommandRequiresFile proves a missing --file is the caller's
// usage error (exit 2), refused before any request is sent.
func TestImportObjectsCommandRequiresFile(t *testing.T) {
	var capture importCapture
	srv := newImportServer(t, &capture)

	code, _ := runRootOut(t, "misc", "import-objects",
		"--url", srv.URL,
		"--duplicate-behaviour", "IGNORE",
		"--missing-ref-behaviour", "IGNORE")

	if code != ExitUsage {
		t.Errorf("missing --file: exit = %+v, want usage", code)
	}
	if n := capture.hits.Load(); n != 0 {
		t.Errorf("server was hit %d time(s); a missing --file must fail before any request", n)
	}
}

// TestImportObjectsCommandRejectsBadEnum proves an out-of-range behaviour flag is
// a usage error (exit 2), refused before any request is sent.
func TestImportObjectsCommandRejectsBadEnum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.zip")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var capture importCapture
	srv := newImportServer(t, &capture)

	code, _ := runRootOut(t, "misc", "import-objects",
		"--url", srv.URL,
		"--file", path,
		"--duplicate-behaviour", "BOGUS",
		"--missing-ref-behaviour", "IGNORE")

	if code != ExitUsage {
		t.Errorf("bad --duplicate-behaviour: exit = %+v, want usage", code)
	}
	if n := capture.hits.Load(); n != 0 {
		t.Errorf("server was hit %d time(s); a bad enum must fail before any request", n)
	}
}
