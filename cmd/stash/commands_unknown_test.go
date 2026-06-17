package main

import "testing"

// TestUnknownSubcommandIsUsageError verifies an unrecognised command (at the
// root or under a resource group) is a usage error (exit 2), not cobra's
// default silent help dump on a zero exit.
func TestUnknownSubcommandIsUsageError(t *testing.T) {
	for _, args := range [][]string{
		{"bogus-resource"},
		{"scene", "nonexistent-verb"},
	} {
		root := buildRootCommand()
		root.SetArgs(args)
		root.SetOut(discard{})
		root.SetErr(discard{})
		err := root.Execute()
		if err == nil {
			t.Errorf("args %v: expected an error, got nil", args)
			continue
		}
		if got := classifyExit(err); got != ExitUsage {
			t.Errorf("args %v: classifyExit = %+v, want %+v", args, got, ExitUsage)
		}
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
