package stash

import (
	"context"
	"fmt"

	"github.com/lightning-rider-999/go-stashapp/schema"
)

// VersionInfo holds the Stash server's reported build identity.
type VersionInfo struct {
	// Version is the server release tag, for example "v0.31.1".
	Version string
	// Hash is the git commit the server was built from.
	Hash string
	// BuildTime is the server's reported build timestamp.
	BuildTime string
}

// Version queries the server for its build identity. A GraphQL or transport
// failure is returned through the typed error model (see [classify]); a null
// version payload is reported as an error rather than a nil dereference.
func (c *Client) Version(ctx context.Context) (*VersionInfo, error) {
	resp, err := Version(ctx, c.GraphQL())
	if err != nil {
		return nil, classify(err)
	}
	if resp.Version == nil {
		return nil, fmt.Errorf("stash: server returned a null version")
	}
	return &VersionInfo{
		Version:   resp.Version.Version,
		Hash:      resp.Version.Hash,
		BuildTime: resp.Version.Build_time,
	}, nil
}

// CheckCompatibility reports whether the server's release matches the schema
// version this library was generated against ([schema.SchemaVersion]).
//
// A mismatch is not an error: the call returns compatible=false with the
// server's reported VersionInfo, so a caller can decide how to proceed. The CLI
// surfaces a mismatch with a distinct exit code and field rather than failing
// the command outright. Only a GraphQL or transport failure produces a non-nil
// error, in which case compatible is false and server is nil.
func (c *Client) CheckCompatibility(ctx context.Context) (compatible bool, server *VersionInfo, err error) {
	info, err := c.Version(ctx)
	if err != nil {
		return false, nil, err
	}
	return info.Version == schema.SchemaVersion, info, nil
}
