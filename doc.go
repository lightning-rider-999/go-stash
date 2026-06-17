// Package gostash is the module root for go-stash, a Go client library
// and command-line interface for Stash's GraphQL API.
//
// Stash (https://github.com/stashapp/stash) is a self-hosted organiser whose
// API is served at <base>/graphql and authenticated with an ApiKey header.
//
// The reusable SDK lives in the stash package
// (github.com/lightning-rider-999/go-stash/stash); the agent-first CLI is
// under cmd/stash. The typed GraphQL surface is generated from Stash's own
// vendored SDL (the schema/ directory, stamped with the version it came from)
// by the internal gen command, which drives the external genops compiler
// (github.com/trackness/graphql-opgen), together with genqlient, so a server
// upgrade that drifts a field is a red build rather than a silent nil.
//
// # Generated names mirror the SDL verbatim
//
// Field and argument names in the generated surface follow Stash's GraphQL SDL
// exactly, including its casing. genqlient maps a GraphQL name to an exported Go
// identifier by upper-casing only the first rune, so an SDL field such as
// per_page, build_time, or api_key becomes Per_page, Build_time, or Api_key
// rather than the Go-idiomatic PerPage, BuildTime, or APIKey. This is a
// deliberate fidelity choice: keeping the generated names one-to-one with the
// SDL makes a server-side rename a compile error instead of a silent mismatch,
// so consumers should expect the non-idiomatic casing and not mistake it for an
// oversight.
//
// # The generated surface is public API
//
// Everything the codegen emits is part of this module's public API surface: the
// operation functions (for example
// [github.com/lightning-rider-999/go-stash/stash.FindScenes]), the input and response
// types and their nested fragment types, the per-operation query-string
// constants (the *_Operation values), and the generated Get* accessor methods.
// Because that surface is regenerated from the vendored SDL, an operation that
// drifts when Stash is upgraded — a renamed field, a changed argument, a removed
// root operation — can change or remove an exported symbol, which is a breaking
// change for code that imported it. Pinning the module pins the SDL it was
// generated against; reviewing the regenerated diff after a schema refresh is
// how such breaks are caught before release.
package gostash
