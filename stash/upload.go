package stash

import (
	"errors"
	"io"
)

// Upload is a file to send through GraphQL's multipart upload (the Upload
// scalar). genqlient binds the Upload scalar to this type; importObjects, the
// sole Upload-taking mutation, is sent by a hand-wired multipart island that
// reads these fields.
type Upload struct {
	// Filename is the name reported to the server for the part.
	Filename string
	// Body is the file content. It is consumed once when the request is sent.
	Body io.Reader
}

// ErrUploadNotJSONEncodable is returned by [Upload.MarshalJSON]. An Upload's
// bytes travel as a separate multipart part per the GraphQL multipart request
// specification, never inline in JSON, so any attempt to JSON-encode an Upload
// is a programming error rather than something to silently misencode.
var ErrUploadNotJSONEncodable = errors.New(
	"stash: Upload cannot be JSON-encoded; use Client.ImportObjects for the multipart upload path")

// MarshalJSON always fails with [ErrUploadNotJSONEncodable] instead of
// producing a JSON object that omits the file bytes.
//
// The generated genqlient operation [ImportObjects] JSON-marshals its variables,
// which would encode an Upload as its exported fields only: Body is an io.Reader
// interface, so json.Marshal serialises the concrete value's exported fields
// (commonly none, yielding "{}") and the file content is silently dropped. That
// generated path is therefore a broken upload. Failing here turns it into a
// loud error and steers callers to [Client.ImportObjects], which builds the
// multipart body by hand and never marshals an Upload.
func (u Upload) MarshalJSON() ([]byte, error) {
	return nil, ErrUploadNotJSONEncodable
}
