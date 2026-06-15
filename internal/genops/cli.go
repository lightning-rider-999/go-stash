package genops

import (
	"bytes"
	"fmt"
	"go/format"
	"sort"
	"strings"
)

// Command is the CLI routing record for one operation: a cobra command path
// plus the hazard flags and type names the runtime needs to execute it. It is
// derived deterministically from a [ManifestEntry] by [BuildCommands].
type Command struct {
	// Path is the cobra command path, resource-then-verb, e.g.
	// ["scene", "list"]. The final element is the leaf command name; the
	// preceding elements are group commands.
	Path []string
	// OpName is the exported operation name (FindScenes), which is also the
	// genqlient query const name: stash.<OpName>_Operation.
	OpName string
	// Field is the schema root-field name (findScenes).
	Field string
	// Kind is "query", "mutation", or "subscription".
	Kind string
	// InputType is the base type of the "input" argument, or "" if none.
	InputType string
	// ReturnType is the base named type the field returns.
	ReturnType string
	// Destructive flags an operation the overlay marked as data-destroying.
	Destructive bool
	// JobReturning flags an operation that enqueues an async job.
	JobReturning bool
	// Deprecated flags a field carrying @deprecated in the schema.
	Deprecated bool
}

// pluralEntity maps an irregular or multi-word plural noun, as it appears in a
// root field name, to its singular kebab-case resource group. The list is
// deliberately small and curated: Stash's entity vocabulary is regular enough
// that only the irregular plurals (Galleries) and multi-word nouns
// (SceneMarkers, SavedFilters) need stating. Regular plurals (Scenes, Tags) are
// handled by the trailing-s rule in singularEntity, so they are not listed.
var pluralEntity = map[string]string{
	"Galleries":    "gallery",
	"SceneMarkers": "scene-marker",
	"SavedFilters": "saved-filter",
}

// singularEntity maps a singular noun, as it appears in a root field name, to
// its kebab-case resource group. Multi-word singulars get an explicit entry;
// single-word ones fall through to a lower-cased default in entityGroup.
var singularEntity = map[string]string{
	"SceneMarker": "scene-marker",
	"SavedFilter": "saved-filter",
}

// entityGroup returns the resource group for a noun extracted from a field
// name. It consults the curated irregular maps first, then falls back to the
// regular rules: a trailing "s" makes the noun plural (drop it, lower-case the
// stem), otherwise the noun is taken singular and lower-cased. A multi-word
// CamelCase noun is kebab-cased throughout.
func entityGroup(noun string) string {
	if g, ok := pluralEntity[noun]; ok {
		return g
	}
	if g, ok := singularEntity[noun]; ok {
		return g
	}
	// Regular plural: a trailing "s" on a noun longer than one rune that is not
	// itself part of the stem (e.g. Scenes, Tags, Studios -> scene, tag,
	// studio). "ss" endings keep their final "s" handled by kebab anyway, but no
	// Stash entity hits that case.
	if strings.HasSuffix(noun, "s") && len(noun) > 1 {
		return kebab(noun[:len(noun)-1])
	}
	return kebab(noun)
}

// kebab converts a CamelCase or mixed identifier to kebab-case: each
// upper-case run starts a new segment, runs of upper-case letters (acronyms
// like DLNA, URL, SQL, API) stay together, and segments join with a hyphen.
// Examples: metadataScan->metadata-scan (caller strips the prefix),
// CleanGenerated->clean-generated, DLNAStatus->dlna-status, querySQL->query-sql.
func kebab(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	var b strings.Builder
	for i, c := range r {
		isUpper := c >= 'A' && c <= 'Z'
		if isUpper && i > 0 {
			prev := r[i-1]
			prevUpper := prev >= 'A' && prev <= 'Z'
			next := rune(0)
			if i+1 < len(r) {
				next = r[i+1]
			}
			nextLower := next >= 'a' && next <= 'z'
			// Start a new segment at a lower->upper boundary (scanFoo) or at the
			// last letter of an acronym that begins a new word (DLNAStatus: the
			// S before "tatus" starts "status").
			if !prevUpper || nextLower {
				b.WriteByte('-')
			}
		}
		if isUpper {
			b.WriteRune(c - 'A' + 'a')
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// subscriptionPaths pins the three subscription fields to their CLI paths. The
// task fixes these explicitly: jobsSubscribe watches the job queue, etc.
var subscriptionPaths = map[string][]string{
	"jobsSubscribe":         {"job", "watch"},
	"loggingSubscribe":      {"log", "tail"},
	"scanCompleteSubscribe": {"scan", "watch"},
}

// verbSuffixes are the entity-mutation verbs recognised on a <entity><Verb>
// field. Each maps to its kebab-case leaf command name. Order matters for
// matching the longest suffix first is not needed here as the verbs are
// distinct words, but the map gives a single source of truth.
var verbSuffixes = map[string]string{
	"Create":  "create",
	"Update":  "update",
	"Destroy": "destroy",
	"Merge":   "merge",
}

// derivePath returns the cobra path for one operation field. It is total and
// deterministic: every field resolves to a path by the first matching rule, and
// the fallback guarantees a path for anything the structured rules miss.
//
// The rules, in order:
//
//   - Subscriptions use the pinned subscriptionPaths map.
//   - find<E>s / find<E> -> [entity, "list"] / [entity, "get"].
//   - all<E>s -> [entity, "all"].
//   - bulk<E>Update -> [entity, "bulk-update"].
//   - <E>sDestroy / <E>sUpdate (plural) -> [entity, "destroy-many" / "update-many"].
//   - <entity><Verb> for Verb in {Create,Update,Destroy,Merge} -> [entity, verb].
//   - metadata<X> -> ["metadata", kebab(X)].
//   - configure<X> / configuration -> ["config", ...].
//   - scrape<X> -> ["scrape", kebab(X)].
//   - stashBox<X> / submitStashBox<X> -> ["stash-box", kebab(X)].
//   - Fallback: ["misc", kebab(field)] — a deterministic, unique two-segment
//     path that never collides with a structured group (no entity, metadata,
//     config, scrape, or stash-box leaf is ever named the same as a kebab'd
//     whole field).
func derivePath(field, kind string) []string {
	if kind == "subscription" {
		if p, ok := subscriptionPaths[field]; ok {
			return p
		}
	}

	if p := deriveFind(field); p != nil {
		return p
	}
	if p := deriveAll(field); p != nil {
		return p
	}
	if p := deriveBulk(field); p != nil {
		return p
	}
	if p := derivePluralMutation(field); p != nil {
		return p
	}
	if p := deriveEntityVerb(field); p != nil {
		return p
	}
	if p := derivePrefixGroup(field); p != nil {
		return p
	}
	// Fallback: a stable two-segment path under a reserved "misc" group. This
	// catches the long tail (stats, version, systemStatus, querySQL, plugins,
	// directory, logs, setup, ...) without a hardcoded list, and cannot collide
	// with a structured path because "misc" is never produced by any rule above.
	return []string{"misc", kebab(field)}
}

// deriveFind handles the find<E>s / find<E> family. A plural target (the noun
// after "find" ends in a plural the entity vocabulary recognises) lists; a
// singular target gets. The handful of irregular finds (findDuplicateScenes,
// findScenesByPathRegex, findSceneByHash, findDefaultFilter, findFile,
// findFiles) are matched by exact rule so they route sensibly and uniquely.
func deriveFind(field string) []string {
	if !strings.HasPrefix(field, "find") {
		return nil
	}
	rest := field[len("find"):]
	switch field {
	case "findDuplicateScenes":
		return []string{"scene", "list-duplicates"}
	case "findScenesByPathRegex":
		return []string{"scene", "list-by-path-regex"}
	case "findSceneByHash":
		return []string{"scene", "get-by-hash"}
	case "findDefaultFilter":
		return []string{"saved-filter", "get-default"}
	case "findJob":
		return []string{"job", "get"}
	}
	// Plural -> list, singular -> get, using the entity vocabulary to decide.
	if g, ok := pluralEntity[rest]; ok {
		return []string{g, "list"}
	}
	if strings.HasSuffix(rest, "s") && len(rest) > 1 {
		return []string{entityGroup(rest), "list"}
	}
	return []string{entityGroup(rest), "get"}
}

// deriveAll handles all<E>s -> [entity, "all"].
func deriveAll(field string) []string {
	if !strings.HasPrefix(field, "all") || len(field) == len("all") {
		return nil
	}
	rest := field[len("all"):]
	if rest[0] < 'A' || rest[0] > 'Z' {
		return nil
	}
	return []string{entityGroup(rest), "all"}
}

// deriveBulk handles bulk<E>Update -> [entity, "bulk-update"].
func deriveBulk(field string) []string {
	if !strings.HasPrefix(field, "bulk") || !strings.HasSuffix(field, "Update") {
		return nil
	}
	mid := field[len("bulk") : len(field)-len("Update")]
	if mid == "" {
		return nil
	}
	return []string{entityGroup(mid), "bulk-update"}
}

// pluralMutationLeaf names the plural batch-mutation verbs and their leaf
// command names. These act on many entities at once (scenesDestroy,
// imagesUpdate, tagsMerge), so the leaf carries a "-many" suffix to set them
// apart from the singular [entity, verb] commands.
var pluralMutationLeaf = map[string]string{
	"Destroy": "destroy-many",
	"Update":  "update-many",
	"Merge":   "merge-many",
}

// derivePluralMutation handles plural batch mutations <E>sDestroy / <E>sUpdate /
// <E>sMerge (scenesDestroy, imagesUpdate, tagsMerge) -> [entity, "<verb>-many"].
// The noun ends in "s" before the verb; singular <entity><Verb> is left to
// deriveEntityVerb. galleriesUpdate is the irregular plural of gallery.
func derivePluralMutation(field string) []string {
	for verb, leaf := range pluralMutationLeaf {
		if !strings.HasSuffix(field, verb) {
			continue
		}
		noun := field[:len(field)-len(verb)]
		// galleriesUpdate -> gallery; scenes/images/etc. -> drop trailing s.
		if noun == "galleries" {
			return []string{"gallery", leaf}
		}
		if strings.HasSuffix(noun, "s") && len(noun) > 1 && isLowerWord(noun) {
			return []string{kebab(noun[:len(noun)-1]), leaf}
		}
	}
	return nil
}

// deriveEntityVerb handles <entity><Verb> for the four entity mutation verbs.
// The entity is the camelCase prefix before the verb; it is lower-cased and
// kebab-cased (sceneMarkerCreate -> [scene-marker, create]).
func deriveEntityVerb(field string) []string {
	for verb, leaf := range verbSuffixes {
		if !strings.HasSuffix(field, verb) || len(field) == len(verb) {
			continue
		}
		ent := field[:len(field)-len(verb)]
		// The prefix must be a lower-camelCase entity name (sceneCreate,
		// galleryChapterUpdate). A field that merely ends in the verb word but
		// is not an entity mutation (e.g. reorderSubGroups) is excluded because
		// its prefix would be empty or the verb is not a true suffix.
		if ent == "" {
			continue
		}
		return []string{kebab(ent), leaf}
	}
	return nil
}

// derivePrefixGroup handles the prefix-keyed groups: metadata*, configure* /
// configuration, scrape*, and the stash-box submission family.
func derivePrefixGroup(field string) []string {
	switch {
	case field == "configuration":
		return []string{"config", "get"}
	case strings.HasPrefix(field, "configure") && len(field) > len("configure"):
		return []string{"config", kebab(field[len("configure"):])}
	case strings.HasPrefix(field, "metadata") && len(field) > len("metadata"):
		return []string{"metadata", kebab(field[len("metadata"):])}
	case strings.HasPrefix(field, "scrape") && len(field) > len("scrape"):
		return []string{"scrape", kebab(field[len("scrape"):])}
	case strings.HasPrefix(field, "submitStashBox"):
		return []string{"stash-box", "submit-" + kebab(field[len("submitStashBox"):])}
	case strings.HasPrefix(field, "stashBox") && len(field) > len("stashBox"):
		return []string{"stash-box", kebab(field[len("stashBox"):])}
	}
	return nil
}

// isLowerWord reports whether s starts with a lower-case ASCII letter, marking
// a camelCase root-field prefix (as opposed to an exported type name).
func isLowerWord(s string) bool {
	return s != "" && s[0] >= 'a' && s[0] <= 'z'
}

// BuildCommands derives one [Command] per manifest entry, deterministically and
// with every path unique. It fails if two operations resolve to the same path,
// rather than silently overwriting one — a collision means the derivation rules
// need a disambiguator, and a red build is the right signal. The result is
// sorted by OpName for stable output.
func BuildCommands(m *Manifest) ([]Command, error) {
	cmds := make([]Command, 0, len(m.Operations))
	for _, e := range m.Operations {
		cmds = append(cmds, Command{
			Path:         derivePath(e.Field, e.Kind),
			OpName:       e.Name,
			Field:        e.Field,
			Kind:         e.Kind,
			InputType:    e.InputType,
			ReturnType:   e.ReturnType,
			Destructive:  e.Destructive,
			JobReturning: e.JobReturning,
			Deprecated:   e.Deprecated,
		})
	}
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].OpName < cmds[j].OpName })

	seen := make(map[string]string, len(cmds))
	for _, c := range cmds {
		key := strings.Join(c.Path, " ")
		if prev, ok := seen[key]; ok {
			return nil, fmt.Errorf("genops: command path collision %q: %s and %s", key, prev, c.OpName)
		}
		seen[key] = c.OpName
	}
	return cmds, nil
}

// EmitCommands renders cmd/stash/gen_commands.go: the generated table of
// commandSpec literals the CLI runtime assembles into its cobra tree. Each spec
// references the genqlient query const stash.<OpName>_Operation rather than
// re-embedding the query text, so the two generated surfaces cannot drift. The
// output is run through go/format, so it is gofmt-clean.
func EmitCommands(m *Manifest) ([]byte, error) {
	cmds, err := BuildCommands(m)
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString("// Code generated by genops; DO NOT EDIT.\n\n")
	b.WriteString("package main\n\n")
	b.WriteString("import \"github.com/lightning-rider-999/go-stashapp/stash\"\n\n")
	b.WriteString("// generatedCommands is the full operation table, one spec per Stash root\n")
	b.WriteString("// field, sorted by OpName. buildRootCommand assembles these into the cobra\n")
	b.WriteString("// tree. Query is the genqlient operation const, so the query text lives in\n")
	b.WriteString("// exactly one place.\n")
	b.WriteString("var generatedCommands = []commandSpec{\n")
	for _, c := range cmds {
		fmt.Fprintf(&b, "\t{Path: %s, OpName: %q, Query: stash.%s_Operation, Kind: %q, InputType: %q, ReturnType: %q, Destructive: %t, JobReturning: %t, Deprecated: %t},\n",
			pathLiteral(c.Path), c.OpName, c.OpName, c.Kind, c.InputType, c.ReturnType, c.Destructive, c.JobReturning, c.Deprecated)
	}
	b.WriteString("}\n")

	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("genops: formatting gen_commands.go: %w", err)
	}
	return src, nil
}

// pathLiteral renders a path slice as a Go composite literal:
// []string{"scene", "list"}.
func pathLiteral(path []string) string {
	var b bytes.Buffer
	b.WriteString("[]string{")
	for i, seg := range path {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", seg)
	}
	b.WriteString("}")
	return b.String()
}
