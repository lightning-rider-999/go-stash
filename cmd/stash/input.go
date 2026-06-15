package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/spf13/cobra"
)

// resolveVariables builds the GraphQL variables object for one operation.
//
// The variables are a JSON object whose keys are the operation's own variable
// names (input for a typical mutation; scene_filter / ids / filter for a list
// query). Two sources feed it, in this precedence:
//
//  1. --input <file> or --input - (stdin) supplies the FULL variables object as
//     JSON. Its values are carried as json.RawMessage and never decoded into a
//     typed Go struct, so a mutation input round-trips byte-for-byte: a present
//     field with a value stays, an omitted field stays omitted, and an explicit
//     null stays null. That present / absent / null three-state is the whole
//     point — a partial-update mutation distinguishes "clear this field" (null)
//     from "leave it unchanged" (absent), which a typed struct cannot express.
//  2. Convenience flags (--id, --query, --page, --per-page, --sort, --direction)
//     are a small, read/list-only shorthand. They merge UNDER the --input keys:
//     any key --input already set wins, so the raw JSON is always authoritative.
//
// Convenience flags never inject a mutation input key. They are registered only
// on query leaves (see addConvenienceFlags) and, defensively, resolveVariables
// applies them only for spec.Kind == "query" and only for variables the
// operation actually declares (looked up in the embedded catalog). An operation
// with no --input and no applicable flags is sent with empty variables.
func resolveVariables(cmd *cobra.Command, spec commandSpec) (map[string]json.RawMessage, error) {
	vars, err := readInputVariables(cmd)
	if err != nil {
		return nil, err
	}

	if err := applyConvenienceFlags(cmd, spec, vars); err != nil {
		return nil, err
	}
	return vars, nil
}

// readInputVariables reads the --input source (file path, or "-" for stdin) into
// a variables map. Values stay json.RawMessage so they are never re-encoded. An
// empty or absent --input yields an empty, non-nil map ready for flag merging.
func readInputVariables(cmd *cobra.Command) (map[string]json.RawMessage, error) {
	input, _ := cmd.Flags().GetString("input")
	if input == "" {
		return map[string]json.RawMessage{}, nil
	}

	var (
		data []byte
		err  error
	)
	if input == "-" {
		data, err = io.ReadAll(cmd.InOrStdin())
	} else {
		data, err = os.ReadFile(input)
	}
	if err != nil {
		return nil, fmt.Errorf("reading --input: %w", err)
	}
	if len(data) == 0 {
		return map[string]json.RawMessage{}, nil
	}

	var vars map[string]json.RawMessage
	if err := json.Unmarshal(data, &vars); err != nil {
		return nil, fmt.Errorf("--input must be a JSON object of variables: %w", err)
	}
	if vars == nil {
		return map[string]json.RawMessage{}, nil
	}
	return vars, nil
}

// convenienceFlagFilter is the FindFilterType field set a query may accept via
// flags. The key is the cobra flag name; filterField is the GraphQL field it
// writes into the filter object; enumValues, when non-empty, restricts the
// accepted values (validated against the vendored schema).
var convenienceFilterFlags = []struct {
	flag        string
	usage       string
	filterField string
	enumValues  []string
}{
	{flag: "query", usage: "list filter: free-text query (filter.q)", filterField: "q"},
	{flag: "page", usage: "list filter: page number (filter.page)", filterField: "page"},
	{flag: "per-page", usage: "list filter: results per page, -1 for all (filter.per_page)", filterField: "per_page"},
	{flag: "sort", usage: "list filter: sort field (filter.sort)", filterField: "sort"},
	{flag: "direction", usage: "list filter: sort direction ASC or DESC (filter.direction)", filterField: "direction", enumValues: []string{"ASC", "DESC"}},
}

// addConvenienceFlags registers the read/list-only convenience flags on a leaf,
// but ONLY for a query whose declared arguments actually accept them. A mutation
// never gets them, so the flags cannot inject an input key. --id is offered when
// the op declares an ids ([ID!]) or id argument; the filter flags are offered
// only when the op declares a filter (FindFilterType) argument. Anything the op
// does not declare is simply not added, which keeps the surface defensive: an
// unknown shorthand is a usage error rather than a silently dropped flag.
func addConvenienceFlags(leaf *cobra.Command, spec commandSpec) {
	if spec.Kind != "query" {
		return
	}
	args := operationArgNames(spec.OpName)

	if args["ids"] || args["id"] {
		leaf.Flags().String("id", "", "convenience: select a single object by ID")
	}
	if args["filter"] {
		for _, f := range convenienceFilterFlags {
			leaf.Flags().String(f.flag, "", f.usage)
		}
	}
}

// applyConvenienceFlags merges any set convenience flags into vars, under the
// --input keys (an --input key is never overwritten). It is a no-op for a
// mutation or for an op that declares none of the relevant arguments, so a
// convenience flag can never become a mutation input key.
func applyConvenienceFlags(cmd *cobra.Command, spec commandSpec, vars map[string]json.RawMessage) error {
	if spec.Kind != "query" {
		return nil
	}
	args := operationArgNames(spec.OpName)

	if err := applyIDFlag(cmd, args, vars); err != nil {
		return err
	}
	return applyFilterFlags(cmd, args, vars)
}

// applyIDFlag binds --id to the operation's ids list or singular id argument.
// An op declaring ids ([ID!]) receives ids: ["<id>"]; one declaring a scalar id
// receives id: "<id>". The ID is emitted as a JSON string, matching Stash's ID
// scalar. A pre-set --input key wins and short-circuits.
func applyIDFlag(cmd *cobra.Command, args map[string]bool, vars map[string]json.RawMessage) error {
	if !cmd.Flags().Changed("id") {
		return nil
	}
	id, _ := cmd.Flags().GetString("id")
	encoded, err := json.Marshal(id)
	if err != nil {
		return fmt.Errorf("encoding --id: %w", err)
	}

	switch {
	case args["ids"]:
		if _, ok := vars["ids"]; ok {
			return nil
		}
		list, err := json.Marshal([]json.RawMessage{json.RawMessage(encoded)})
		if err != nil {
			return fmt.Errorf("encoding --id list: %w", err)
		}
		vars["ids"] = list
	case args["id"]:
		if _, ok := vars["id"]; ok {
			return nil
		}
		vars["id"] = encoded
	default:
		return fmt.Errorf("--id is not accepted by this operation")
	}
	return nil
}

// applyFilterFlags merges the set FindFilterType flags into the filter variable.
// It starts from any filter object the --input already supplied (decoded only to
// a json.RawMessage map, so untouched fields round-trip verbatim) and adds a
// field per set flag without overwriting a field --input already provided. With
// no filter flags set it leaves vars untouched.
func applyFilterFlags(cmd *cobra.Command, args map[string]bool, vars map[string]json.RawMessage) error {
	if !args["filter"] {
		return nil
	}

	// Decode an existing filter (from --input) into a raw-valued map so its
	// fields survive byte-for-byte; absent yields an empty object.
	filter := map[string]json.RawMessage{}
	if raw, ok := vars["filter"]; ok {
		if err := json.Unmarshal(raw, &filter); err != nil {
			return fmt.Errorf("--input filter must be a JSON object: %w", err)
		}
	}

	changed := false
	for _, f := range convenienceFilterFlags {
		if !cmd.Flags().Changed(f.flag) {
			continue
		}
		val, _ := cmd.Flags().GetString(f.flag)
		if len(f.enumValues) > 0 && !slices.Contains(f.enumValues, val) {
			return fmt.Errorf("--%s must be one of %v, got %q", f.flag, f.enumValues, val)
		}
		// An --input field wins over the flag.
		if _, ok := filter[f.filterField]; ok {
			continue
		}
		encoded, err := encodeFilterValue(f.filterField, val)
		if err != nil {
			return err
		}
		filter[f.filterField] = encoded
		changed = true
	}

	if !changed {
		return nil
	}
	merged, err := json.Marshal(filter)
	if err != nil {
		return fmt.Errorf("encoding filter: %w", err)
	}
	vars["filter"] = merged
	return nil
}

// numericFilterFields are FindFilterType fields typed as Int in the schema; they
// must be emitted as JSON numbers, not strings.
var numericFilterFields = []string{"page", "per_page"}

// encodeFilterValue encodes a filter field's string flag value as the JSON type
// the schema expects: a number for Int fields, a string otherwise.
func encodeFilterValue(field, val string) (json.RawMessage, error) {
	if slices.Contains(numericFilterFields, field) {
		// A bare integer literal is valid JSON for an Int field; reject anything
		// that is not, so a typo fails loud rather than reaching the server as a
		// string.
		if !json.Valid([]byte(val)) {
			return nil, fmt.Errorf("--%s must be an integer, got %q", filterFlagFor(field), val)
		}
		var n int
		if err := json.Unmarshal([]byte(val), &n); err != nil {
			return nil, fmt.Errorf("--%s must be an integer, got %q", filterFlagFor(field), val)
		}
		return json.RawMessage(val), nil
	}
	encoded, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("encoding --%s: %w", filterFlagFor(field), err)
	}
	return encoded, nil
}

// filterFlagFor reports the flag name that writes a given filter field, for use
// in error messages. It falls back to the field name if no flag maps to it.
func filterFlagFor(field string) string {
	for _, f := range convenienceFilterFlags {
		if f.filterField == field {
			return f.flag
		}
	}
	return field
}

// operationArgNames returns the set of argument names the operation declares,
// read from the embedded catalog. The catalog is the same source of truth the
// command table is generated from, so the convenience flags can never offer an
// argument the operation does not actually accept. An unknown OpName yields an
// empty set.
func operationArgNames(opName string) map[string]bool {
	entry, ok := catalogEntry(opName)
	if !ok {
		return map[string]bool{}
	}
	names := make(map[string]bool, len(entry.Args))
	for _, a := range entry.Args {
		names[a.Name] = true
	}
	return names
}
