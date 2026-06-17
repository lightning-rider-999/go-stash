package gostash

// Code generation pipeline. genops compiles the vendored SDL into genqlient
// operations and fragments, the operation manifest, and the catalog; genqlient
// then turns the operations and fragments into the typed stash client. The two
// directives run in order, genops first.

//go:generate go run ./internal/gen
//go:generate go run github.com/Khan/genqlient
