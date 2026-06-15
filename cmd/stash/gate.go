package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// confirmFlag is the name of the persistent flag that authorises a destructive
// operation. Its value is deliberately a full clause rather than a terse --force
// so an agent (or a human) must spell out intent: a destructive op is one that
// can drop, overwrite, or anonymise data and there is no undo.
const confirmFlag = "yes-i-understand"

// exitCodeError forces a specific frozen exit code regardless of the underlying
// error's shape. Some failures are not inferable from an SDK error — a refused
// destructive op, a failed async job, a wait timeout — so the command path that
// detects them wraps the cause in an exitCodeError and classifyExit returns the
// carried code. The cause stays in the chain via Unwrap, so the error envelope
// still reports a useful message and a caller can errors.As down to it.
type exitCodeError struct {
	code ExitCode
	err  error
}

// newExitCodeError pairs an error with the exact exit code it must produce. A nil
// err is allowed: the envelope then reports the code name alone, which is how a
// pure refusal (no underlying failure) is surfaced.
func newExitCodeError(code ExitCode, err error) *exitCodeError {
	return &exitCodeError{code: code, err: err}
}

// Error renders the carried cause, or the code name when there is no cause.
func (e *exitCodeError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return e.code.Name
}

// Unwrap exposes the cause so errors.As/Is reach the original error.
func (e *exitCodeError) Unwrap() error { return e.err }

// addDestructiveFlag registers the confirmation flag on a destructive leaf. The
// flag is local to the leaf (not persistent on the root) so it surfaces in that
// command's own --help with hazard-specific wording and cannot be set on a
// command where it would be meaningless.
func addDestructiveFlag(leaf *cobra.Command, spec commandSpec) {
	if !spec.Destructive {
		return
	}
	leaf.Flags().Bool(confirmFlag, false,
		"required to run this DESTRUCTIVE operation: it can drop, overwrite, or "+
			"anonymise data and cannot be undone. Without it the command refuses "+
			"and exits "+fmt.Sprint(ExitDestructiveRefused.Code)+" (destructive-refused).")
}

// checkDestructiveGate enforces the confirmation contract before a destructive
// operation runs. For a non-destructive spec it is a no-op. For a destructive
// one it returns nil only when --yes-i-understand is set; otherwise it returns an
// exitCodeError carrying destructive-refused so the op never executes and the
// process exits 8. The refusal is detected before any client call, so no request
// reaches the server.
func checkDestructiveGate(cmd *cobra.Command, spec commandSpec) error {
	if !spec.Destructive {
		return nil
	}
	confirmed, _ := cmd.Flags().GetBool(confirmFlag)
	if confirmed {
		return nil
	}
	op := strings.Join(spec.Path, " ")
	return newExitCodeError(ExitDestructiveRefused, fmt.Errorf(
		"%s is destructive and was refused: pass --%s to confirm", op, confirmFlag))
}
