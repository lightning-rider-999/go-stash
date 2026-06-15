package genops

import "sort"

// pathNamedAllowlist is the explicit, audited set of object/union/interface
// types that genops emits as path-named inline selections rather than canonical
// fragment types (spike finding A3). The "no path-named struct" invariant is
// relaxed for exactly these shapes:
//
//   - mixed-selection junction wrappers — no id (so not ref-able) yet carrying
//     an object edge (so not a flat value type); inlined with the inner entity
//     edge flattened to a Ref;
//   - union- and interface-typed fields — decoded by a __typename-keyed
//     UnmarshalJSON (A5), so they cannot collapse to a single fragment spread;
//   - value-type cycles in the file/folder graph — terminated scalars-only to
//     keep the fragment DAG acyclic (B6).
//
// Anything path-named and absent from this list is a generation drift: the
// conformance suite (Task 22) fails, forcing a human to audit the new shape and
// either fix the generator or add the type here with a reason. The map is a
// superset of what any single artifact emits — operation selections (Task 6)
// surface SceneMarkerTag and the BaseFile interface that fragments alone do not.
var pathNamedAllowlist = map[string]string{
	// Mixed-selection junction wrappers (no id + object edge).
	"SceneGroup":       "junction {group: Group!, scene_index} — Scene.groups",
	"SceneMovie":       "junction {movie: Movie!, scene_index} — Scene.movies (deprecated)",
	"GroupDescription": "junction {group: Group!, description} — Group.containing_groups / sub_groups",
	"SceneMarkerTag":   "junction {tag: Tag!, scene_markers: [SceneMarker!]!} — Query.sceneMarkerTags",
	"PluginHook":       "junction {plugin: Plugin!, ...} — plugin hooks",
	"PluginTask":       "junction {plugin: Plugin!, ...} — plugin tasks",

	// Union- and interface-typed fields (__typename-keyed decode, A5/A4).
	"VisualFile": "union VideoFile | ImageFile — Image.visual_files",
	"BaseFile":   "interface (BasicFile|VideoFile|ImageFile|GalleryFile) — findFile / findFiles / Folder.zip_file",

	// Value-type cycles in the file/folder graph (terminated scalars-only, B6).
	"Folder":    "self-referential folder tree — parent_folder / parent_folders / sub_folders",
	"BasicFile": "file metadata cycling via parent_folder / zip_file",
}

// IsPathNamedAllowed reports whether a type is in the audited exception set.
func IsPathNamedAllowed(typeName string) bool {
	_, ok := pathNamedAllowlist[typeName]
	return ok
}

// PathNamedReason returns the audited reason a type is path-named, or "".
func PathNamedReason(typeName string) string {
	return pathNamedAllowlist[typeName]
}

// AllowedPathNamed returns the allowlisted type names in sorted order.
func AllowedPathNamed() []string {
	out := make([]string, 0, len(pathNamedAllowlist))
	for name := range pathNamedAllowlist {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// UnlistedPathNamed returns the path-named types produced by fs that are not in
// the audited allowlist. A non-empty result is a generation drift.
func UnlistedPathNamed(fs *FragmentSet) []string {
	var out []string
	for _, name := range fs.PathNamedTypes() {
		if !IsPathNamedAllowed(name) {
			out = append(out, name)
		}
	}
	return out
}
