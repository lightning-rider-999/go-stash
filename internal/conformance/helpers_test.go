package conformance

import (
	"sync"
	"testing"

	"github.com/lightning-rider-999/go-stash/schema"
	genops "github.com/trackness/graphql-opgen"
	"github.com/vektah/gqlparser/v2/ast"
)

// Paths to the vendored SDL and curated overlay, relative to this package's
// directory (internal/conformance).
const (
	schemaDir   = "../../schema"
	overlayPath = "../../internal/gen/overlay.yaml"
)

// fixture bundles the once-loaded schema, overlay, and compiled surface so each
// gate can reach for whichever artefact it needs without re-parsing the SDL.
type fixture struct {
	schema   *ast.Schema
	overlay  *genops.Overlay
	manifest *genops.Manifest
	catalog  *genops.Catalog
	compiled *genops.Compiled
}

var (
	loadOnce sync.Once
	loaded   *fixture
	loadErr  error
)

// load returns the shared fixture, parsing the schema and compiling the surface
// on first use. It fails the test (rather than returning a partial fixture) on
// any error, so every gate starts from a known-good baseline.
func load(t *testing.T) *fixture {
	t.Helper()
	loadOnce.Do(func() {
		f := &fixture{}
		f.schema, loadErr = genops.LoadSchema(schemaDir)
		if loadErr != nil {
			return
		}
		f.overlay, loadErr = genops.LoadOverlay(overlayPath)
		if loadErr != nil {
			return
		}
		f.compiled, loadErr = genops.Compile(schemaDir, overlayPath, schema.SchemaVersion, stashConfig())
		if loadErr != nil {
			return
		}
		f.manifest = f.compiled.Manifest
		f.catalog = f.compiled.Catalog
		loaded = f
	})
	if loadErr != nil {
		t.Fatalf("loading conformance fixture: %v", loadErr)
	}
	return loaded
}

// rootOps is the set of root operation kinds whose fields back the generated
// command surface.
var rootOps = []ast.Operation{ast.Query, ast.Mutation, ast.Subscription}

// stashConfig returns the Stash-specific genops.Config the conformance suite
// drives the schema-agnostic compiler with. It mirrors the configuration
// internal/gen supplies — the same Config that produced the
// committed artefacts — so the gates here validate exactly what ships. The
// values are written as plain literals (the exit-code names mirror
// internal/exitcode's frozen taxonomy) so the conformance package stays free of
// any host-module coupling beyond genops itself.
func stashConfig() genops.Config {
	return genops.Config{
		ExitCodes: genops.ExitCodeProvider{
			Base:               []string{"ok", "usage", "auth", "transport", "validation", "server-fault"},
			NotFound:           "not-found",
			DestructiveRefused: "destructive-refused",
			JobFailed:          "job-failed",
			StillRunning:       "still-running",
			Unconfirmed:        "unconfirmed",
		},
		Naming: genops.NamingRules{
			PluralEntity: map[string]string{
				"Galleries":    "gallery",
				"SceneMarkers": "scene-marker",
				"SavedFilters": "saved-filter",
			},
			SingularEntity: map[string]string{
				"SceneMarker": "scene-marker",
				"SavedFilter": "saved-filter",
			},
			SubscriptionPaths: map[string][]string{
				"jobsSubscribe":         {"job", "watch"},
				"loggingSubscribe":      {"log", "tail"},
				"scanCompleteSubscribe": {"scan", "watch"},
			},
			VerbSuffixes: map[string]string{
				"Create":  "create",
				"Update":  "update",
				"Destroy": "destroy",
				"Merge":   "merge",
			},
			PluralMutationLeaf: map[string]string{
				"Destroy": "destroy-many",
				"Update":  "update-many",
				"Merge":   "merge-many",
			},
			ExactFinds: map[string][]string{
				"findDuplicateScenes":   {"scene", "list-duplicates"},
				"findScenesByPathRegex": {"scene", "list-by-path-regex"},
				"findSceneByHash":       {"scene", "get-by-hash"},
				"findDefaultFilter":     {"saved-filter", "get-default"},
				"findJob":               {"job", "get"},
			},
			IrregularPluralNoun: map[string]string{
				"galleries": "gallery",
			},
			ExactGroups: map[string][]string{
				"configuration": {"config", "get"},
			},
			PrefixGroups: []genops.PrefixGroupRule{
				{Prefix: "configure", Group: "config"},
				{Prefix: "metadata", Group: "metadata"},
				{Prefix: "scrape", Group: "scrape"},
				{Prefix: "submitStashBox", Group: "stash-box", LeafPrefix: "submit-", MatchExact: true},
				{Prefix: "stashBox", Group: "stash-box"},
			},
			FallbackGroup: "misc",
		},
		PathNamedAllowlist: map[string]string{
			"SceneGroup":       "junction {group: Group!, scene_index} — Scene.groups",
			"SceneMovie":       "junction {movie: Movie!, scene_index} — Scene.movies (deprecated)",
			"GroupDescription": "junction {group: Group!, description} — Group.containing_groups / sub_groups",
			"SceneMarkerTag":   "junction {tag: Tag!, scene_markers: [SceneMarker!]!} — Query.sceneMarkerTags",
			"PluginHook":       "junction {plugin: Plugin!, ...} — plugin hooks",
			"PluginTask":       "junction {plugin: Plugin!, ...} — plugin tasks",
			"VisualFile":       "union VideoFile | ImageFile — Image.visual_files",
			"BaseFile":         "interface (BasicFile|VideoFile|ImageFile|GalleryFile) — findFile / findFiles / Folder.zip_file",
			"Folder":           "self-referential folder tree — parent_folder / parent_folders / sub_folders",
			"BasicFile":        "file metadata cycling via parent_folder / zip_file",
		},
		TargetPackageImport:  "github.com/lightning-rider-999/go-stash/stash",
		OperationConstSuffix: "_Operation",
		CommandTableDoc: []string{
			"generatedCommands is the full operation table, one spec per Stash root",
			"field, sorted by OpName. buildRootCommand assembles these into the cobra",
			"tree. Query is the genqlient operation const, so the query text lives in",
			"exactly one place.",
		},
	}
}
