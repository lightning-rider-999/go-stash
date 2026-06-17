// Package conformance is the project's authoritative quality gate: it asserts
// that the generated surface (operations, fragments, manifest, catalog, typed
// Go bindings) stays faithful to the vendored Stash SDL. Any drift in the
// generated artefacts (a new field, a renamed type, a dropped operation) must
// turn one of these tests red rather than slip through silently.
//
// All behaviour lives in the package's tests; this package ships no production
// code and is imported by nothing. Each test realises one of the conformance
// gates: completeness against the schema, abstract-type coverage, scalar and
// enum handling, partial-update three-state shapes, redaction, upload wiring,
// determinism, and drift detection. The shared fixtures load the schema,
// overlay, and compiled surface once per test binary so new gates are cheap to
// add.
package conformance
