// Package gostashapp is the module root for go-stashapp, a Go client library
// and command-line interface for Stash's GraphQL API.
//
// Stash (https://github.com/stashapp/stash) is a self-hosted organiser whose
// API is served at <base>/graphql and authenticated with an ApiKey header.
//
// The reusable SDK lives in the stash package
// (github.com/lightning-rider-999/go-stashapp/stash); the agent-first CLI is
// under cmd/stash. The typed GraphQL surface is generated from Stash's own
// vendored SDL (the schema/ directory, stamped with the version it came from)
// by the internal genops compiler together with genqlient, so a server upgrade
// that drifts a field is a red build rather than a silent nil.
package gostashapp
