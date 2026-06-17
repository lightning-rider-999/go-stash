package stash

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// importObjectsQuery is the operation sent for [Client.ImportObjects]. It is
// hand-authored rather than taken from the generated client because the Upload
// scalar requires a multipart request, which genqlient's HTTP client cannot
// build. importObjects returns a scalar ID, so no interface or union fields are
// selected and no __typename is needed.
const importObjectsQuery = `mutation ($input: ImportObjectsInput!) { importObjects(input: $input) }`

// importObjectsVariables mirrors the input shape sent in the operations part.
// The file is encoded as JSON null and supplied by the multipart map, per the
// GraphQL multipart request specification.
type importObjectsVariables struct {
	Input importObjectsInputJSON `json:"input"`
}

// importObjectsInputJSON is the JSON form of [ImportObjectsInput] for the
// operations part. The file field is always null on the wire; the actual bytes
// travel as a separate multipart part referenced through the map.
type importObjectsInputJSON struct {
	File                *struct{}            `json:"file"`
	DuplicateBehaviour  ImportDuplicateEnum  `json:"duplicateBehaviour"`
	MissingRefBehaviour ImportMissingRefEnum `json:"missingRefBehaviour"`
}

// ImportObjects uploads an export archive to the server's importObjects
// mutation and returns the identifier of the import job it starts.
//
// Side effects: this is a write. With input.DuplicateBehaviour set to
// [ImportDuplicateEnumOverwrite] (OVERWRITE), objects in the archive that
// already exist on the server are mutated in place — their stored fields are
// overwritten with the imported values, not skipped or duplicated. Use
// [ImportDuplicateEnumIgnore] or [ImportDuplicateEnumFail] to keep existing
// objects untouched.
//
// The request is a GraphQL multipart request (the jaydenseric specification):
// an operations part carrying the query and variables with the file nulled, a
// map part binding the file part to variables.input.file, and the file part
// itself. The file is streamed from input.File.Body, which is read once. The
// request goes through [Client.HTTPClient], so the ApiKey header is injected by
// the same round-tripper used for ordinary requests.
//
// A GraphQL or transport failure is returned through the typed error model (see
// [GraphQLError] and [TransportError]).
func (c *Client) ImportObjects(ctx context.Context, input ImportObjectsInput) (jobID string, err error) {
	body, contentType := c.buildImportBody(ctx, input)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint(), body)
	if err != nil {
		// Nothing will read or close the pipe on this path, so unblock and stop
		// the producer goroutine before returning; otherwise it leaks, parked on
		// its first write into the pipe.
		_ = body.CloseWithError(err)
		return "", &TransportError{err: fmt.Errorf("stash: building import request: %w", err)}
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		return "", &TransportError{err: fmt.Errorf("stash: sending import request: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	return decodeImportResponse(resp)
}

// buildImportBody returns a streaming reader for the multipart request body and
// its Content-Type header. The body is produced by a goroutine writing into an
// io.Pipe so the file content is never fully buffered. A read failure on the
// file body is propagated to the HTTP client through the pipe, so the request
// fails rather than silently sending a truncated upload.
//
// The reader is returned as the concrete [*io.PipeReader] so a caller that never
// hands it to http.Client.Do (for example when request construction fails) can
// CloseWithError to unblock and stop the producer goroutine; http.Client.Do
// itself drains and closes the body on every path it takes.
func (c *Client) buildImportBody(ctx context.Context, input ImportObjectsInput) (*io.PipeReader, string) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	go func() {
		// pw.CloseWithError(nil) closes cleanly; a non-nil error surfaces to the
		// reader. writeImportParts returns the first failure it hits.
		// CloseWithError always returns nil, so the result is discarded.
		_ = pw.CloseWithError(writeImportParts(ctx, mw, input))
	}()

	return pr, mw.FormDataContentType()
}

// writeImportParts writes the operations, map, and file parts in order. The
// context is honoured so a cancelled request stops streaming a large file.
func writeImportParts(ctx context.Context, mw *multipart.Writer, input ImportObjectsInput) (err error) {
	// Close writes the trailing boundary; surface its error only when the parts
	// otherwise wrote cleanly, so a malformed body is never silently sent.
	defer func() {
		if cerr := mw.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("stash: finalising import body: %w", cerr)
		}
	}()

	operations, err := json.Marshal(map[string]any{
		"query": importObjectsQuery,
		"variables": importObjectsVariables{
			Input: importObjectsInputJSON{
				File:                nil,
				DuplicateBehaviour:  input.DuplicateBehaviour,
				MissingRefBehaviour: input.MissingRefBehaviour,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("stash: encoding import operations: %w", err)
	}
	if err := mw.WriteField("operations", string(operations)); err != nil {
		return fmt.Errorf("stash: writing operations part: %w", err)
	}

	mapPart, err := json.Marshal(map[string][]string{"0": {"variables.input.file"}})
	if err != nil {
		return fmt.Errorf("stash: encoding import map: %w", err)
	}
	if err := mw.WriteField("map", string(mapPart)); err != nil {
		return fmt.Errorf("stash: writing map part: %w", err)
	}

	filePart, err := mw.CreateFormFile("0", input.File.Filename)
	if err != nil {
		return fmt.Errorf("stash: creating file part: %w", err)
	}
	if err := copyWithContext(ctx, filePart, input.File.Body); err != nil {
		return fmt.Errorf("stash: streaming import file: %w", err)
	}
	return nil
}

// copyWithContext copies src to dst, aborting if ctx is cancelled. It reads in
// modest chunks so cancellation is observed promptly on a large upload.
func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) error {
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// importResponse is the GraphQL envelope returned by importObjects: a scalar ID
// under data plus an optional errors array.
type importResponse struct {
	Data struct {
		ImportObjects string `json:"importObjects"`
	} `json:"data"`
	Errors gqlerror.List `json:"errors"`
}

// decodeImportResponse turns the HTTP response into a job id or a typed error.
// A non-2xx status becomes a [*TransportError] (carrying the status, plus
// [ErrUnauthorized] for 401/403); when its body is not valid JSON (a gateway
// HTML page, a bare error string) the raw body is embedded, truncated, in the
// error message rather than dropped. A 200 with a GraphQL errors array becomes a
// [*GraphQLError]; a body that cannot be decoded becomes a [*TransportError].
func decodeImportResponse(resp *http.Response) (string, error) {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", &TransportError{StatusCode: resp.StatusCode, err: fmt.Errorf("stash: reading import response: %w", err)}
	}

	// Route a non-2xx status through the same classification used for the
	// generated client: build genqlient's HTTPError so classify can read it.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope graphql.Response
		if err := json.Unmarshal(data, &envelope); err != nil {
			// A non-JSON body (a gateway/proxy 502 HTML page, a bare "boom")
			// would otherwise yield an empty errors array and a status-only
			// error that drops every clue about what the upstream said. Surface
			// the raw body, truncated, so the cause is not lost.
			return "", &TransportError{
				StatusCode: resp.StatusCode,
				retryable:  transportRetryable(resp.StatusCode, nil),
				err:        fmt.Errorf("stash: server returned non-JSON body: %s", truncateBody(data)),
			}
		}
		httpErr := graphql.HTTPError{
			Response:   envelope,
			StatusCode: resp.StatusCode,
		}
		return "", classify(&httpErr)
	}

	var parsed importResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", &TransportError{StatusCode: resp.StatusCode, err: fmt.Errorf("stash: decoding import response: %w", err)}
	}
	if len(parsed.Errors) > 0 {
		return "", classify(parsed.Errors)
	}
	if parsed.Data.ImportObjects == "" {
		// A 200 that decodes with neither errors nor a job id is a malformed
		// success; report it rather than returning an empty id as if it worked,
		// mirroring Version's null-payload guard.
		return "", &TransportError{StatusCode: resp.StatusCode, err: fmt.Errorf("stash: server returned no import job id")}
	}
	return parsed.Data.ImportObjects, nil
}

// maxBodySnippet caps how much of an undecodable response body is embedded in an
// error message, so a large HTML error page does not bloat the error.
const maxBodySnippet = 512

// truncateBody renders body as a single-line snippet for an error message,
// trimming surrounding whitespace and capping the length. An empty body becomes
// a readable placeholder so the error never ends with a bare colon.
func truncateBody(body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "(empty)"
	}
	if len(s) > maxBodySnippet {
		return s[:maxBodySnippet] + "… (truncated)"
	}
	return s
}
