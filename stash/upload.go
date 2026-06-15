package stash

import "io"

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
