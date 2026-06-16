package exitcode

import (
	"slices"
	"testing"
)

// TestTaxonomyFrozen locks the (name, integer) pairs of the whole taxonomy in
// frozen order. A rename or renumber is a red build here, the single lock that
// both cmd/stash and internal/genops sit downstream of.
func TestTaxonomyFrozen(t *testing.T) {
	want := []Code{
		{"ok", 0}, {"internal", 1}, {"usage", 2}, {"auth", 3},
		{"transport", 4}, {"validation", 5}, {"server-fault", 6},
		{"not-found", 7}, {"destructive-refused", 8}, {"job-failed", 9},
		{"still-running", 10}, {"unconfirmed", 11},
	}
	got := []Code{
		OK, Internal, Usage, Auth,
		Transport, Validation, ServerFault,
		NotFound, DestructiveRefused, JobFailed,
		StillRunning, Unconfirmed,
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("taxonomy[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

// TestBaseSet pins the base set — the six codes every command can return, in
// order — and asserts Internal is excluded (it is the catch-all, never an
// advertised outcome). The catalog's baseExitCodes derives from this exact set.
func TestBaseSet(t *testing.T) {
	want := []Code{OK, Usage, Auth, Transport, Validation, ServerFault}
	if !slices.Equal(Base, want) {
		t.Errorf("Base = %+v, want %+v", Base, want)
	}
	if slices.Contains(Base, Internal) {
		t.Error("Base must not contain Internal (exit 1 is the catch-all, not an advertised outcome)")
	}
}
