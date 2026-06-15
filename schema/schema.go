// Package schema holds the vendored Stash GraphQL SDL — the *.graphql files in
// this directory and in types/ — pinned to the release tag recorded in
// version.txt and exposed programmatically as [SchemaVersion].
//
// The typed Go surface is generated from these files by genqlient (see the
// repository's genqlient.yaml). After upgrading the target Stash release,
// refresh both the SDL and the stamped constant with `task schema`.
//
//go:generate go run gen.go
package schema
