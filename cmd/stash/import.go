package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"

	"github.com/lightning-rider-999/go-stashapp/stash"
)

// importObjectsOpName is the one operation the CLI must special-case. Its input
// carries a required file: Upload! that the generic JSON dispatch cannot send (a
// multipart upload is required), so this leaf routes to the hand-wired multipart
// [stash.Client.ImportObjects] island instead of runOperation.
const importObjectsOpName = "ImportObjects"

// The import-objects-only flag names.
const (
	importFileFlag      = "file"
	importDuplicateFlag = "duplicate-behaviour"
	importMissingFlag   = "missing-ref-behaviour"
)

// addImportFlags registers the import-objects-only flags on its leaf: the file
// to upload and the two required behaviour enums. They are local to that leaf,
// so no other command grows a --file flag. The behaviour defaults are the
// non-destructive choices (IGNORE), so the common case is a single --file and an
// OVERWRITE/CREATE has to be asked for explicitly.
func addImportFlags(leaf *cobra.Command, spec commandSpec) {
	if spec.OpName != importObjectsOpName {
		return
	}
	leaf.Flags().String(importFileFlag, "",
		"path to the export archive to import, or \"-\" for stdin (required)")
	leaf.Flags().String(importDuplicateFlag, string(stash.ImportDuplicateEnumIgnore),
		fmt.Sprintf("how to treat objects that already exist, one of %v", stash.AllImportDuplicateEnum))
	leaf.Flags().String(importMissingFlag, string(stash.ImportMissingRefEnumIgnore),
		fmt.Sprintf("how to treat missing referenced objects, one of %v", stash.AllImportMissingRefEnum))
}

// runImportObjects is the import-objects leaf's RunE branch. It builds the
// [stash.ImportObjectsInput] from the --file and behaviour flags and sends it
// through the multipart [stash.Client.ImportObjects] — the only path that can
// carry the required file bytes — then renders the returned job id and, with
// --wait, tracks the job to a terminal state, matching every other
// job-returning leaf.
func runImportObjects(cmd *cobra.Command, resolve clientResolver, spec commandSpec) error {
	input, file, err := importInput(cmd)
	if err != nil {
		return err
	}
	if file != nil {
		defer func() { _ = file.Close() }()
	}

	client, err := resolve(cmd)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	jobID, err := client.ImportObjects(ctx, input)
	if err != nil {
		// SIGINT (or parent cancellation) during the upload is a clean stop (exit
		// 0), the same disposition as the --wait and job-mutation paths — without
		// this, the cancelled context would classify as a transport failure.
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	format, _ := cmd.Flags().GetString("output")
	if err := writeImportOutput(cmd.OutOrStdout(), format, spec, jobID); err != nil {
		return err
	}

	if waitRequested(cmd) {
		return trackJob(cmd, client, jobID)
	}
	return nil
}

// importInput reads the --file and behaviour flags into an ImportObjectsInput.
// The returned *os.File is non-nil when --file names a real file (the caller
// closes it); a "-" file streams stdin and returns a nil file. A missing or
// unreadable file, or an out-of-range behaviour, is a usage error (exit 2),
// refused before any request is built.
func importInput(cmd *cobra.Command) (stash.ImportObjectsInput, *os.File, error) {
	path, _ := cmd.Flags().GetString(importFileFlag)
	if path == "" {
		return stash.ImportObjectsInput{}, nil,
			newUsageError(fmt.Errorf("--%s is required for import-objects", importFileFlag))
	}

	dup, err := importDuplicate(cmd)
	if err != nil {
		return stash.ImportObjectsInput{}, nil, err
	}
	missing, err := importMissing(cmd)
	if err != nil {
		return stash.ImportObjectsInput{}, nil, err
	}

	var (
		body     io.Reader
		filename string
		file     *os.File
	)
	if path == "-" {
		body = cmd.InOrStdin()
		filename = "import.zip"
	} else {
		f, oerr := os.Open(path)
		if oerr != nil {
			// An unreadable --file path is the caller's mistake: usage (exit 2).
			return stash.ImportObjectsInput{}, nil,
				newUsageError(fmt.Errorf("opening --%s: %w", importFileFlag, oerr))
		}
		file = f
		body = f
		filename = filepath.Base(path)
	}

	return stash.ImportObjectsInput{
		File:                stash.Upload{Filename: filename, Body: body},
		DuplicateBehaviour:  dup,
		MissingRefBehaviour: missing,
	}, file, nil
}

// importDuplicate resolves --duplicate-behaviour against the schema enum,
// returning a usage error for an out-of-range value.
func importDuplicate(cmd *cobra.Command) (stash.ImportDuplicateEnum, error) {
	v, _ := cmd.Flags().GetString(importDuplicateFlag)
	e := stash.ImportDuplicateEnum(v)
	if !slices.Contains(stash.AllImportDuplicateEnum, e) {
		return "", newUsageError(fmt.Errorf("--%s must be one of %v, got %q",
			importDuplicateFlag, stash.AllImportDuplicateEnum, v))
	}
	return e, nil
}

// importMissing resolves --missing-ref-behaviour against the schema enum,
// returning a usage error for an out-of-range value.
func importMissing(cmd *cobra.Command) (stash.ImportMissingRefEnum, error) {
	v, _ := cmd.Flags().GetString(importMissingFlag)
	e := stash.ImportMissingRefEnum(v)
	if !slices.Contains(stash.AllImportMissingRefEnum, e) {
		return "", newUsageError(fmt.Errorf("--%s must be one of %v, got %q",
			importMissingFlag, stash.AllImportMissingRefEnum, v))
	}
	return e, nil
}

// writeImportOutput renders the import response as the same {"importObjects":
// "<id>"} shape the generic dispatch would have produced, so the --output
// formats (json/ndjson/table/yaml) and API-key redaction behave identically to
// every other operation.
func writeImportOutput(out io.Writer, format string, spec commandSpec, jobID string) error {
	data, err := json.Marshal(map[string]string{"importObjects": jobID})
	if err != nil {
		return err
	}
	return writeOutput(out, format, spec, data)
}
