// Command stash is an agent-first command-line client for a self-hosted Stash
// GraphQL server. Every Stash root operation is exposed as a resource-and-verb
// command (for example "stash scene list", "stash metadata scan"), generated
// from the vendored SDL; the command table in gen_commands.go is produced by
// the genops compiler.
//
// Output is machine-readable JSON by default (-o ndjson|table|yaml for other
// shapes); variables are supplied as raw JSON through --input so partial-update
// mutations preserve the present/absent/null three-state. Failures print a
// structured JSON envelope on stderr and exit with a code from the frozen
// taxonomy documented in docs/AGENTS.md. The embedded operation catalog is
// served by "stash catalog"; subscriptions stream NDJSON ("stash job watch",
// "stash log tail", "stash scan watch"); job-returning mutations accept --wait.
//
// Configuration comes from --url/--api-key or the STASHAPP_URL and
// STASHAPP_API_KEY environment variables.
package main
