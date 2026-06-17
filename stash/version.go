package stash

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/lightning-rider-999/go-stash/schema"
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
	// Version.version is nullable in the SDL (the release tag may be absent on a
	// dev build); hash and build_time are non-null.
	version := ""
	if resp.Version.Version != nil {
		version = *resp.Version.Version
	}
	return &VersionInfo{
		Version:   version,
		Hash:      resp.Version.Hash,
		BuildTime: resp.Version.Build_time,
	}, nil
}

// VersionRelation describes how the server's release compares to the schema
// version this library was generated against.
type VersionRelation int

const (
	// VersionUnknown means the server reported no usable release tag — an empty
	// version, or a dev/hash-only build with no semantic version. Compatibility
	// cannot be judged, so this is neither a match nor a definite mismatch.
	VersionUnknown VersionRelation = iota
	// VersionEqual means server and schema share the same major.minor.
	VersionEqual
	// VersionServerNewer means the server's major.minor is ahead of the schema.
	VersionServerNewer
	// VersionServerOlder means the server's major.minor is behind the schema.
	VersionServerOlder
)

// String renders the relation for logs and messages.
func (r VersionRelation) String() string {
	switch r {
	case VersionEqual:
		return "equal"
	case VersionServerNewer:
		return "server-newer"
	case VersionServerOlder:
		return "server-older"
	default:
		return "unknown"
	}
}

// Compatibility is the result of comparing the server's release against the
// schema version this library was generated against.
type Compatibility struct {
	// Compatible reports whether the server is safe to talk to: true only when
	// the server's major.minor equals the schema's. An unknown server version is
	// not compatible.
	Compatible bool
	// Relation states the direction of any difference (equal, server newer or
	// older, or unknown). It lets a caller distinguish "server ahead of this
	// build" from "server behind".
	Relation VersionRelation
	// Server is the version the server reported.
	Server *VersionInfo
	// SchemaVersion is the schema tag this library was generated against, echoed
	// for messaging.
	SchemaVersion string
}

// Compatibility compares the server's reported release against the schema
// version this library was generated against ([schema.SchemaVersion]) using
// semantic-version rules: a leading "v" is tolerated on either side and only
// major.minor is compared, so a patch-level drift (v0.31.1 vs v0.31.4) is still
// compatible. A server that reports no usable semantic version (empty, or a
// dev/hash-only build) yields [VersionUnknown] rather than a flat mismatch.
//
// A version difference is not an error: the result carries the relation and the
// server's [VersionInfo] so a caller can decide how to proceed. Only a GraphQL
// or transport failure produces a non-nil error, in which case the result is
// the zero value.
func (c *Client) Compatibility(ctx context.Context) (Compatibility, error) {
	info, err := c.Version(ctx)
	if err != nil {
		return Compatibility{}, err
	}

	result := Compatibility{
		Server:        info,
		SchemaVersion: schema.SchemaVersion,
	}

	serverV := normalizeSemver(info.Version)
	schemaV := normalizeSemver(schema.SchemaVersion)
	if !semver.IsValid(serverV) || !semver.IsValid(schemaV) {
		// No usable semantic version on at least one side (a dev/hash-only or
		// empty server build): compatibility is unknown, not a mismatch.
		result.Relation = VersionUnknown
		return result, nil
	}

	switch semver.Compare(semver.MajorMinor(serverV), semver.MajorMinor(schemaV)) {
	case 0:
		result.Relation = VersionEqual
		result.Compatible = true
	case 1:
		result.Relation = VersionServerNewer
	default:
		result.Relation = VersionServerOlder
	}
	return result, nil
}

// normalizeSemver makes a Stash release tag comparable with golang.org/x/mod/
// semver, which requires a leading "v". It trims surrounding space and adds the
// "v" when missing; it does not otherwise validate the string (the caller uses
// semver.IsValid for that), so an empty or non-semantic input stays invalid.
func normalizeSemver(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return v
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

// CheckCompatibility reports whether the server's release matches the schema
// version this library was generated against ([schema.SchemaVersion]).
//
// It is a thin wrapper over [Client.Compatibility]: compatibility is judged on
// major.minor under semantic-version rules (a leading "v" is tolerated and a
// patch-level drift is still compatible), and an unknown server version (empty
// or a dev/hash-only build) reports compatible=false. A version difference is
// not an error: the call returns compatible=false with the server's reported
// VersionInfo so a caller can decide how to proceed. Only a GraphQL or
// transport failure produces a non-nil error, in which case compatible is false
// and server is nil. Use [Client.Compatibility] when the direction of the
// difference (server newer/older) or the unknown case must be distinguished.
func (c *Client) CheckCompatibility(ctx context.Context) (compatible bool, server *VersionInfo, err error) {
	result, err := c.Compatibility(ctx)
	if err != nil {
		return false, nil, err
	}
	return result.Compatible, result.Server, nil
}
