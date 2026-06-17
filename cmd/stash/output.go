package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"go.yaml.in/yaml/v3"
)

// outputFormats is the set of values accepted by --output, in help order. It is
// the single source of truth for the valid set: writeOutput's switch, the
// unknown-format error message, and isValidOutputFormat all read it, so a caller
// (e.g. a command PreRunE in commands.go) can pre-validate against the same set.
var outputFormats = []string{"json", "ndjson", "table", "yaml"}

// isValidOutputFormat reports whether format is one writeOutput can render. The
// empty string is accepted because it selects the json default. A PreRunE in
// commands.go calls this so a bad --output fails as a usage error (exit 2)
// before any request is sent.
func isValidOutputFormat(format string) bool {
	return format == "" || slices.Contains(outputFormats, format)
}

// writeOutput renders an operation's response data to w in the requested
// format. data is the GraphQL response data object, shaped {"<rootField>":
// <result>}; spec.ReturnType tells the list-streaming and table renderers how
// to find the primary list (see streamItems). The format is one of
// outputFormats; an unrecognised value is an error naming the valid set.
//
// API key redaction runs first, for every format: Stash pre-signs media URLs
// with the instance API key as an `apikey` query parameter, and that JWT must
// never reach stdout or a log regardless of how the payload is rendered.
//
// json is the default and is always emitted: there is no TTY detection, so an
// agent gets stable machine-readable output whether or not a terminal is
// attached. The other formats are conveniences layered on the same data.
func writeOutput(w io.Writer, format string, spec commandSpec, data json.RawMessage) error {
	data, err := redactAPIKeys(data)
	if err != nil {
		return fmt.Errorf("redacting api keys: %w", err)
	}

	switch format {
	case "", "json":
		return writeJSON(w, data)
	case "ndjson":
		return writeNDJSON(w, spec, data)
	case "table":
		return writeTable(w, spec, data)
	case "yaml":
		return writeYAML(w, data)
	default:
		// A bad --output is the caller's mistake: classify it as a usage error
		// (exit 2), not an internal failure (exit 1).
		return newUsageError(fmt.Errorf("unknown output format %q: valid formats are %s", format, strings.Join(outputFormats, ", ")))
	}
}

// writeJSON pretty-prints raw JSON to w with a 2-space indent and a trailing
// newline. The CLI is agent-first, so JSON is the default output regardless of
// whether stdout is a terminal. A null or empty data field renders as the
// literal "null" so the output is always a parseable document.
func writeJSON(w io.Writer, raw json.RawMessage) error {
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return fmt.Errorf("formatting response JSON: %w", err)
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}

// writeNDJSON streams the primary list of a list-returning operation as one
// compact JSON object per line. See streamItems for how the list is located.
// When the payload is not list-shaped, the single result value is emitted as
// one line, so ndjson is always valid for any operation.
func writeNDJSON(w io.Writer, spec commandSpec, data json.RawMessage) error {
	items, ok, err := streamItems(spec, data)
	if err != nil {
		return err
	}
	if !ok {
		// Not list-shaped: emit the unwrapped result as a single line.
		v, err := unwrapValue(data)
		if err != nil {
			return err
		}
		return writeNDJSONLine(w, v)
	}
	for _, it := range items {
		if err := writeNDJSONLine(w, it); err != nil {
			return err
		}
	}
	return nil
}

// writeNDJSONLine writes one compact JSON value and a trailing newline.
func writeNDJSONLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n")
	return err
}

// streamItems locates the primary list of a response for ndjson/table.
//
// The response data is {"<rootField>": <result>}. The single root field is
// unwrapped first. Detection then splits on the operation's return type:
//
//   - result-wrapper return (spec.ReturnType ends in "ResultType"): the result
//     is an object such as {count, duration, scenes:[...]}. The items are the
//     elements of its single array-valued field (here "scenes"); the scalar
//     wrapper fields like count are metadata and are skipped. With no array
//     field, no list is reported.
//   - bare list return (e.g. allScenes -> [Scene!]!): the unwrapped result is
//     itself a JSON array; its elements are the items.
//
// Any other shape (a single object, a scalar, null) reports ok=false so the
// caller falls back to a single-line / key-value rendering. A malformed payload
// surfaces as a non-nil error rather than being silently treated as not
// list-shaped. Returning a slice of decoded values keeps the renderers
// format-agnostic.
func streamItems(spec commandSpec, data json.RawMessage) ([]any, bool, error) {
	result, err := unwrapValue(data)
	if err != nil {
		return nil, false, err
	}

	// Bare list return: the result is already an array.
	if arr, ok := result.([]any); ok {
		return arr, true, nil
	}

	// Result-wrapper return: find the single array-valued field.
	if strings.HasSuffix(spec.ReturnType, "ResultType") {
		if obj, ok := result.(map[string]any); ok {
			if arr, ok := soleArrayField(obj); ok {
				return arr, true, nil
			}
		}
	}
	return nil, false, nil
}

// soleArrayField returns the array value of the object's array-valued field.
// A well-formed Stash result wrapper has exactly one such field (scenes,
// images, ...) alongside scalar metadata (count, duration). When more than one
// array field is present the first by sorted key wins, which keeps the choice
// deterministic for an unexpected schema shape.
func soleArrayField(obj map[string]any) ([]any, bool) {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if arr, ok := obj[k].([]any); ok {
			return arr, true
		}
	}
	return nil, false
}

// unwrapValue strips the single {"<rootField>": <result>} wrapper that every
// GraphQL response data object carries and returns the decoded inner value. When
// data is not a single-field object it is returned decoded as-is. A JSON decode
// failure is surfaced as the error rather than swallowed, so a malformed payload
// no longer collapses to a silent nil — the renderers in this file route through
// this variant so a bad payload becomes a reported error.
func unwrapValue(data json.RawMessage) (any, error) {
	v, err := decodeAny(data)
	if err != nil {
		return nil, err
	}
	if obj, ok := v.(map[string]any); ok && len(obj) == 1 {
		for _, inner := range obj {
			return inner, nil
		}
	}
	return v, nil
}

// unwrapResult is the error-discarding shim kept for the job-id extraction path
// in wait.go, which treats a decode failure (nil) the same as an absent id. New
// code should call [unwrapValue] and handle the error; rendering already does.
func unwrapResult(data json.RawMessage) any {
	v, _ := unwrapValue(data)
	return v
}

// decodeAny decodes JSON into a generic value with number preservation enabled,
// so a custom Int64 scalar above 2^53 (BaseFile.size, SQLExecResult row counts)
// stays a json.Number and survives re-encoding verbatim instead of being rounded
// through float64. The cell and writeNDJSONLine renderers already handle
// json.Number alongside float64.
func decodeAny(data json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// writeYAML renders data as YAML. The JSON is decoded to a generic value and
// re-encoded with yaml v3, so nested maps and lists survive intact.
func writeYAML(w io.Writer, data json.RawMessage) error {
	if len(data) == 0 {
		_, err := io.WriteString(w, "null\n")
		return err
	}
	v, err := decodeAny(data)
	if err != nil {
		return fmt.Errorf("decoding response data: %w", err)
	}
	b, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding yaml: %w", err)
	}
	_, err = w.Write(b)
	return err
}

// nestedPlaceholder stands in for a value that a table cell cannot show flat.
const nestedPlaceholder = "{…}"

// writeTable renders a best-effort aligned text table for human eyes. For a
// list result it prints one row per item with a column per scalar key (the
// union across items, sorted); nested objects and arrays render as a
// placeholder rather than inlined JSON, and a missing key renders blank. For a
// single object it prints a two-column key/value table. It is deliberately
// simple and tolerant of missing or extra keys — the machine formats are json
// and ndjson.
func writeTable(w io.Writer, spec commandSpec, data json.RawMessage) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)

	items, ok, err := streamItems(spec, data)
	if err != nil {
		return err
	}
	var rows [][]string
	if ok {
		rows = listTableRows(items)
	} else {
		// Single value: key/value table when it is an object, else one cell.
		single, err := unwrapValue(data)
		if err != nil {
			return err
		}
		if obj, ok := single.(map[string]any); ok {
			rows = kvTableRows(obj)
		} else {
			rows = [][]string{{cell(single)}}
		}
	}

	// Tab-join each row and write the block in one call; the only error that
	// matters is the tabwriter's Flush, which aligns and emits the columns.
	if _, err := io.WriteString(tw, joinRows(rows)); err != nil {
		return err
	}
	return tw.Flush()
}

// joinRows renders rows as tab-separated lines, each newline-terminated.
func joinRows(rows [][]string) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(strings.Join(r, "\t"))
		b.WriteByte('\n')
	}
	return b.String()
}

// listTableRows builds a header of scalar columns and one row per item. Nested
// objects/arrays render as a placeholder and a missing key renders blank. With
// no scalar columns it falls back to one single-cell row per item.
func listTableRows(items []any) [][]string {
	cols := scalarColumns(items)
	if len(cols) == 0 {
		rows := make([][]string, 0, len(items))
		for _, it := range items {
			rows = append(rows, []string{cell(it)})
		}
		return rows
	}
	rows := make([][]string, 0, len(items)+1)
	rows = append(rows, cols)
	for _, it := range items {
		obj, _ := it.(map[string]any)
		row := make([]string, len(cols))
		for i, c := range cols {
			if obj == nil {
				continue
			}
			if v, ok := obj[c]; ok {
				row[i] = cell(v)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// kvTableRows builds a two-column key/value table for one object.
func kvTableRows(obj map[string]any) [][]string {
	rows := make([][]string, 0, len(obj)+1)
	rows = append(rows, []string{"KEY", "VALUE"})
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rows = append(rows, []string{k, cell(obj[k])})
	}
	return rows
}

// scalarColumns returns the sorted union of top-level keys whose value is a
// scalar in at least one item. Keys that are only ever nested objects/arrays
// are dropped from the header entirely.
func scalarColumns(items []any) []string {
	seen := map[string]bool{}
	for _, it := range items {
		obj, ok := it.(map[string]any)
		if !ok {
			continue
		}
		for k, v := range obj {
			if isScalar(v) {
				seen[k] = true
			}
		}
	}
	cols := make([]string, 0, len(seen))
	for k := range seen {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	return cols
}

// isScalar reports whether v is a flat value (string, number, bool, null) as
// opposed to a nested map or slice.
func isScalar(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return false
	default:
		return true
	}
}

// cell formats a single value for a table cell. Scalars print plainly; nested
// objects and arrays collapse to a placeholder so a row stays one line.
func cell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case map[string]any, []any:
		return nestedPlaceholder
	case bool:
		return strconv.FormatBool(t)
	case json.Number:
		return t.String()
	case float64:
		// json.Unmarshal decodes numbers as float64; render integers without a
		// trailing .0 for a cleaner table.
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}
