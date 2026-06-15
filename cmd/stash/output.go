package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// writeJSON pretty-prints raw JSON to w with a 2-space indent and a trailing
// newline. The CLI is agent-first, so JSON is the default output regardless of
// whether stdout is a terminal.
//
// TODO(Task 17): add NDJSON, table, and YAML formats selected by --output.
func writeJSON(w io.Writer, raw json.RawMessage) error {
	if len(raw) == 0 {
		// A successful operation with a null/empty data field still produces a
		// valid document; emit JSON null so the output is always parseable.
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
