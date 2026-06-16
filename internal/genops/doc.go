// Package genops compiles Stash's vendored GraphQL SDL into a typed surface:
// one genqlient operation per root field, a thin manifest indexing those
// operations, and a machine-facing catalog of inputs, enums, and deprecations.
//
// The compiler reads strictly from the schema AST (gqlparser/v2). It never
// carries a hand-maintained list of fields or edges, so a server upgrade that
// drifts a field is a red build rather than a silent nil.
//
// Field enumeration distinguishes two surfaces:
//
//   - Root operations ([RootFields]) include every field of Query, Mutation,
//     and Subscription, deprecated ones included, so every operation stays
//     reachable from the CLI, with deprecations flagged in the catalog.
//   - Entity selections ([Edges] and [Scalars]) exclude @deprecated fields, so
//     the canonical fragment types carry the clean, current surface; the
//     deprecated fields are still recorded in the catalog.
//
// genops runs at build time only (see the cmd/genops main and the //go:generate
// directives that drive it). It is not imported by the SDK or CLI at runtime.
//
// See README.md for the design rationale and extraction notes.
package genops
