package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lightning-rider-999/go-stash/stash"
)

// destructiveSpec is a minimal destructive mutation spec for gating tests. The
// query text is irrelevant: a refused op must never reach the transport, and a
// confirmed one only needs the request to be made.
func destructiveSpec() commandSpec {
	return commandSpec{
		Path:        []string{"sql", "exec"},
		OpName:      "ExecSQL",
		Query:       `mutation ExecSQL($sql: String!) { execSQL(sql: $sql) { rows } }`,
		Kind:        "mutation",
		InputType:   "",
		ReturnType:  "SQLExecResult",
		Destructive: true,
	}
}

// hitCountServer records how many GraphQL requests it received, so a test can
// assert that a refused destructive op never reaches the server.
func hitCountServer(t *testing.T) (*stash.Client, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"execSQL":{"rows":[]}}}`))
	}))
	t.Cleanup(srv.Close)
	c, err := stash.NewClient(stash.WithURL(srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c, &hits
}

func TestDestructiveGateRefusesWithoutFlag(t *testing.T) {
	spec := destructiveSpec()
	leaf := newLeafCommand(spec)
	leaf.SetArgs(nil)
	leaf.SilenceErrors = true
	leaf.SilenceUsage = true

	_, hits := hitCountServer(t)
	// Point the leaf at the server via the root flags it would inherit; here we
	// invoke RunE directly through Execute with the url flag unset, because the
	// gate must fire before any client is built. Drive the gate check directly.
	err := checkDestructiveGate(leaf, spec)
	if err == nil {
		t.Fatal("expected refusal without --yes-i-understand, got nil")
	}
	if got := classifyExit(err); got != ExitDestructiveRefused {
		t.Fatalf("exit code = %v, want %v", got, ExitDestructiveRefused)
	}
	if hits.Load() != 0 {
		t.Fatalf("server was hit %d times; a refused op must not execute", hits.Load())
	}
}

func TestDestructiveGateProceedsWithFlag(t *testing.T) {
	spec := destructiveSpec()
	leaf := newLeafCommand(spec)
	if err := leaf.Flags().Set(confirmFlag, "true"); err != nil {
		t.Fatalf("set flag: %v", err)
	}

	if err := checkDestructiveGate(leaf, spec); err != nil {
		t.Fatalf("gate refused a confirmed op: %v", err)
	}
}

// TestDestructiveGateEndToEnd drives the full leaf RunE: without the flag the
// command must refuse and never touch the server; with it, the request is made.
func TestDestructiveGateEndToEnd(t *testing.T) {
	spec := destructiveSpec()
	c, hits := hitCountServer(t)

	run := func(confirm bool) error {
		root := &cobra.Command{Use: "stash"}
		root.PersistentFlags().String("url", "", "")
		root.PersistentFlags().String("api-key", "", "")
		root.PersistentFlags().StringP("output", "o", "json", "")
		root.PersistentFlags().String("input", "", "")
		leaf := newLeafCommandWithClient(spec, c)
		root.AddCommand(leaf)
		args := []string{"exec", "--input", "-"}
		if confirm {
			args = append(args, "--"+confirmFlag)
		}
		root.SetArgs(args)
		root.SetIn(bytes.NewBufferString(`{"sql":"VACUUM"}`))
		root.SetOut(&bytes.Buffer{})
		root.SilenceErrors = true
		root.SilenceUsage = true
		return root.ExecuteContext(context.Background())
	}

	// Refused: no flag.
	if err := run(false); err == nil || classifyExit(err) != ExitDestructiveRefused {
		t.Fatalf("refused path: err=%v code=%v", err, classifyExit(err))
	}
	if hits.Load() != 0 {
		t.Fatalf("refused op reached the server (%d hits)", hits.Load())
	}

	// Confirmed: flag set, request goes through.
	if err := run(true); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("confirmed path failed: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("confirmed op hit the server %d times, want 1", hits.Load())
	}
}
