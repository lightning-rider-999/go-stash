//go:build integration

// Package stash's integration tests exercise a live Stash instance. They are
// excluded from the default `go test ./...` and compile only under
// `-tags integration`. Each test skips with a clear message when the
// environment that points at an instance is unset, so
// `go test -tags integration ./...` is safe to run without one.
//
// Configuration comes from the same variables the library and CLI use:
//
//	STASHAPP_URL      base UI URL of the instance (GraphQL at <url>/graphql)
//	STASHAPP_API_KEY  API key, if the instance requires authentication
//
// The destructive ImportObjects path is gated further behind STASH_IMPORT_FILE,
// since it mutates the instance; it stays skipped unless a file is named.
package stash_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/lightning-rider-999/go-stash/schema"
	"github.com/lightning-rider-999/go-stash/stash"
)

// liveClient builds a client against the instance named by STASHAPP_URL, or
// skips the test when it is unset. STASHAPP_API_KEY is forwarded when present;
// an unauthenticated instance works without it.
func liveClient(t *testing.T) *stash.Client {
	t.Helper()
	url := os.Getenv("STASHAPP_URL")
	if url == "" {
		t.Skip("integration: STASHAPP_URL is unset; skipping live-instance test")
	}
	opts := []stash.Option{stash.WithURL(url)}
	if key := os.Getenv("STASHAPP_API_KEY"); key != "" {
		opts = append(opts, stash.WithAPIKey(key))
	}
	c, err := stash.NewClient(opts...)
	if err != nil {
		t.Fatalf("integration: building client: %v", err)
	}
	return c
}

// TestLiveVersionHandshake confirms the live instance answers the Version query
// with a non-empty release, and logs how that release compares to the schema
// version this library was generated against. A mismatch is logged, not failed:
// the library still talks to a drifted server, and the conformance tier guards
// the schema itself.
func TestLiveVersionHandshake(t *testing.T) {
	c := liveClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	compatible, info, err := c.CheckCompatibility(ctx)
	if err != nil {
		t.Fatalf("CheckCompatibility against live instance: %v", err)
	}
	if info == nil || info.Version == "" {
		t.Fatalf("live instance returned an empty version: %+v", info)
	}
	t.Logf("live version = %q (hash %q, built %q); schema version = %q; compatible = %v",
		info.Version, info.Hash, info.BuildTime, schema.SchemaVersion, compatible)
	if !compatible {
		t.Logf("note: live version %q differs from generated schema %q", info.Version, schema.SchemaVersion)
	}
}

// TestLiveFindScenesSmoke runs a small FindScenes query and asserts it decodes
// into the canonical generated types without error. It requests a tiny page so
// it is cheap regardless of library size, and tolerates an empty library.
func TestLiveFindScenesSmoke(t *testing.T) {
	c := liveClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Only per_page is set; every other nullable field stays a nil pointer and
	// marshals to JSON null. In particular Direction is null, not "" — the live
	// server rejects an empty SortDirectionEnum, which optional: pointer fixes.
	filter := &stash.FindFilterType{Per_page: new(3)}
	resp, err := stash.FindScenes(ctx, c.GraphQL(), nil, nil, filter)
	if err != nil {
		t.Fatalf("FindScenes against live instance: %v", err)
	}
	if resp == nil || resp.FindScenes == nil {
		t.Fatal("FindScenes returned a nil result type")
	}
	got := len(resp.FindScenes.Scenes)
	t.Logf("FindScenes returned count=%d, decoded %d scene(s) in this page", resp.FindScenes.Count, got)
	if got > 3 {
		t.Errorf("decoded %d scenes for per_page=3", got)
	}
	// Decoding a scene's required fields proves the canonical types match the
	// live schema; an empty library leaves nothing to inspect, which is fine.
	for _, s := range resp.FindScenes.Scenes {
		if s.Id == "" {
			t.Error("a decoded scene has an empty id")
		}
	}
}

// TestLiveImportObjects is the destructive path: it mutates the instance, so it
// stays skipped unless STASH_IMPORT_FILE names an export archive to upload. When
// set, it streams the file through ImportObjects and asserts a job id comes
// back. The behaviour is the least destructive available (skip duplicates, fail
// on a missing reference rather than inventing one).
func TestLiveImportObjects(t *testing.T) {
	c := liveClient(t)

	path := os.Getenv("STASH_IMPORT_FILE")
	if path == "" {
		t.Skip("integration: STASH_IMPORT_FILE is unset; skipping destructive import test")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening import file %q: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	jobID, err := c.ImportObjects(ctx, stash.ImportObjectsInput{
		File:                stash.Upload{Filename: "import.zip", Body: f},
		DuplicateBehaviour:  stash.ImportDuplicateEnumIgnore,
		MissingRefBehaviour: stash.ImportMissingRefEnumFail,
	})
	if err != nil {
		t.Fatalf("ImportObjects against live instance: %v", err)
	}
	if jobID == "" {
		t.Error("ImportObjects returned an empty job id")
	}
	t.Logf("ImportObjects started job %q", jobID)
}
