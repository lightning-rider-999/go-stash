package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"sync/atomic"
	"testing"
)

// countingServer is an httptest server that records how many GraphQL requests it
// received, so a test can prove a command failed up front without ever touching
// the wire.
type countingServer struct {
	srv  *httptest.Server
	hits atomic.Int64
}

func newCountingServer(t *testing.T) *countingServer {
	t.Helper()
	cs := &countingServer{}
	cs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cs.hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"findScenes":{"count":0,"scenes":[]}}}`)
	}))
	t.Cleanup(cs.srv.Close)
	return cs
}

// runRoot drives the whole root command for argv and returns the classified exit
// code, exercising the same PersistentPreRunE / Args wiring the binary uses.
func runRoot(t *testing.T, argv ...string) ExitCode {
	t.Helper()
	root := buildRootCommand()
	root.SetArgs(argv)
	root.SetOut(discard{})
	root.SetErr(discard{})
	if err := root.Execute(); err != nil {
		return classifyExit(err)
	}
	return ExitOK
}

// TestOutputFlagValidatedBeforeClientCall proves the --output validation in the
// root PersistentPreRunE runs before any client call: an invalid format on a
// real (query) leaf pointed at a live fake server must classify as a usage error
// (exit 2) and the server must never be hit. This is the cli sub-2 fix — without
// it, a bad --output on a write/--wait op would run the operation first and only
// then error.
func TestOutputFlagValidatedBeforeClientCall(t *testing.T) {
	cs := newCountingServer(t)

	got := runRoot(t, "scene", "list", "--url", cs.srv.URL, "--output", "bogus")
	if got != ExitUsage {
		t.Errorf("invalid --output: classifyExit = %+v, want %+v", got, ExitUsage)
	}
	if n := cs.hits.Load(); n != 0 {
		t.Errorf("server was hit %d time(s); a bad --output must be rejected before any request", n)
	}
}

// TestOutputFlagValidForEveryFormat is a guard that every advertised format
// passes the pre-run validator (no false rejection). It stops short of a real
// request by relying on a server that would answer if reached; the point here is
// only that validation does not reject a valid format.
func TestOutputFlagValidForEveryFormat(t *testing.T) {
	cs := newCountingServer(t)
	for _, f := range outputFormats {
		got := runRoot(t, "scene", "list", "--url", cs.srv.URL, "--output", f)
		if got != ExitOK {
			t.Errorf("format %q: exit = %+v, want OK", f, got)
		}
	}
}

// TestLeafRejectsExtraArgs proves a generated leaf rejects stray positional
// arguments (cli sub-4): `scene list junk` is a usage error (exit 2), not a
// silently ignored argument. The catalog command keeps its MaximumNArgs(1) and
// is not covered here.
func TestLeafRejectsExtraArgs(t *testing.T) {
	got := runRoot(t, "scene", "list", "junk")
	if got != ExitUsage {
		t.Errorf("`scene list junk`: classifyExit = %+v, want %+v", got, ExitUsage)
	}
}

// TestCatalogStillAcceptsOneArg guards that the NoArgs change on generated leaves
// did not touch the catalog command, which legitimately takes an optional OpName.
func TestCatalogStillAcceptsOneArg(t *testing.T) {
	got := runRoot(t, "catalog", "FindScenes")
	if got == ExitUsage {
		t.Errorf("`catalog FindScenes` was rejected as a usage error; it must accept one arg")
	}
}

// TestResolveBuildInfo covers the release sub-1 fallback: a `go install
// module@version` build carries no ldflags (version stays "dev") but embeds the
// module version and VCS settings, which resolveBuildInfo must surface. A release
// build (version already set) is left untouched.
func TestResolveBuildInfo(t *testing.T) {
	mkInfo := func(modVer, rev, when string) *debug.BuildInfo {
		bi := &debug.BuildInfo{}
		bi.Main.Version = modVer
		if rev != "" {
			bi.Settings = append(bi.Settings, debug.BuildSetting{Key: "vcs.revision", Value: rev})
		}
		if when != "" {
			bi.Settings = append(bi.Settings, debug.BuildSetting{Key: "vcs.time", Value: when})
		}
		return bi
	}

	t.Run("go install fills version, commit, date", func(t *testing.T) {
		v, c, d := resolveBuildInfo("dev", "none", "unknown",
			mkInfo("v1.2.3", "abc123", "2026-06-16T00:00:00Z"), true)
		if v != "v1.2.3" || c != "abc123" || d != "2026-06-16T00:00:00Z" {
			t.Errorf("got (%q,%q,%q), want (v1.2.3, abc123, 2026-06-16T00:00:00Z)", v, c, d)
		}
	})

	t.Run("release ldflags win, build info ignored", func(t *testing.T) {
		v, c, d := resolveBuildInfo("v9.9.9", "deadbeef", "2025-01-01",
			mkInfo("v1.2.3", "abc123", "2026-06-16T00:00:00Z"), true)
		if v != "v9.9.9" || c != "deadbeef" || d != "2025-01-01" {
			t.Errorf("release build was overwritten: got (%q,%q,%q)", v, c, d)
		}
	})

	t.Run("devel module version does not replace dev", func(t *testing.T) {
		v, c, d := resolveBuildInfo("dev", "none", "unknown",
			mkInfo("(devel)", "abc123", ""), true)
		if v != "dev" {
			t.Errorf("version = %q, want dev (a (devel) build is no improvement)", v)
		}
		// VCS settings still apply even when the module version is (devel).
		if c != "abc123" {
			t.Errorf("commit = %q, want abc123", c)
		}
		if d != "unknown" {
			t.Errorf("date = %q, want unknown (no vcs.time given)", d)
		}
	})

	t.Run("no build info leaves placeholders", func(t *testing.T) {
		v, c, d := resolveBuildInfo("dev", "none", "unknown", nil, false)
		if v != "dev" || c != "none" || d != "unknown" {
			t.Errorf("got (%q,%q,%q), want the untouched placeholders", v, c, d)
		}
	})
}
