#!/usr/bin/env bash
#
# Refresh the vendored Stash GraphQL SDL at the pinned release tag recorded in
# schema/version.txt, then re-stamp schema/version_gen.go. Idempotent: because
# the tag is immutable, re-running produces byte-identical files (no git diff).
#
# Pinned by design — never tracks `develop`. To target a new Stash release, bump
# schema/version.txt and re-run (`task schema`).
#
# Uses `gh api` to read the public stashapp/stash repository. Portable to the
# bash 3.2 shipped on macOS (no mapfile / associative arrays).
set -euo pipefail

repo="stashapp/stash"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
schema_dir="$root/schema"
ref="$(tr -d '[:space:]' <"$schema_dir/version.txt")"

if [ -z "$ref" ]; then
	echo "refresh-schema: schema/version.txt is empty" >&2
	exit 1
fi

echo "Refreshing SDL from $repo at $ref ..."

# Enumerate every .graphql blob under graphql/schema/ at the pinned tag.
paths="$(
	gh api "repos/$repo/git/trees/$ref?recursive=1" \
		--jq '.tree[]
			| select(.type=="blob")
			| select(.path|startswith("graphql/schema/"))
			| select(.path|endswith(".graphql"))
			| .path' |
		sort
)"

if [ -z "$paths" ]; then
	echo "refresh-schema: no .graphql files found at $ref" >&2
	exit 1
fi

# Atomic refresh: stage the full new SDL set in a temp dir first, sanity-check
# it, and only then swap it into schema/. A mid-loop fetch failure (or an empty
# response) aborts before any tracked file is touched, so the working tree is
# never left with a half-written or empty schema/ — the stale-but-valid SDL
# stays in place until a complete, validated set is ready to replace it.
stage="$(mktemp -d 2>/dev/null || mktemp -d -t refresh-schema)"
if [ -z "$stage" ] || [ ! -d "$stage" ]; then
	echo "refresh-schema: could not create a temporary staging directory" >&2
	exit 1
fi
trap 'rm -rf "$stage"' EXIT INT HUP TERM

# 1. Fetch every file into the staging tree, mirroring its path under
#    graphql/schema/. set -o pipefail + the explicit non-empty check below mean
#    a failed `gh api` (404, transport error) or an empty body is fatal.
count=0
while IFS= read -r p; do
	[ -z "$p" ] && continue
	rel="${p#graphql/schema/}"
	dest="$stage/$rel"
	mkdir -p "$(dirname "$dest")"
	if ! gh api "repos/$repo/contents/$p?ref=$ref" \
		-H "Accept: application/vnd.github.raw" >"$dest"; then
		echo "refresh-schema: failed to fetch $p at $ref — aborting, schema/ left unchanged" >&2
		exit 1
	fi
	count=$((count + 1))
done < <(printf '%s\n' "$paths")

if [ "$count" -eq 0 ]; then
	echo "refresh-schema: fetched 0 files — aborting, schema/ left unchanged" >&2
	exit 1
fi

# 2. Sanity-check the staged set: every file must be a non-empty regular file.
#    A zero-byte SDL file would silently break codegen, so fail loudly here
#    rather than stamping a broken schema into the tree.
while IFS= read -r staged; do
	[ -z "$staged" ] && continue
	if [ ! -s "$staged" ]; then
		echo "refresh-schema: staged file is empty: ${staged#"$stage"/} — aborting, schema/ left unchanged" >&2
		exit 1
	fi
done < <(find "$stage" -type f -name '*.graphql')

# 3. Swap atomically per-file: replace the previously vendored SDL only now that
#    a complete, validated set exists. Drop stale *.graphql first (so an
#    upstream-removed file cannot linger), then move the new set in. Only
#    *.graphql are touched — never version.txt, the *.go stamp, or catalog.json.
rm -f "$schema_dir"/*.graphql "$schema_dir"/types/*.graphql
while IFS= read -r staged; do
	[ -z "$staged" ] && continue
	rel="${staged#"$stage"/}"
	dest="$schema_dir/$rel"
	mkdir -p "$(dirname "$dest")"
	mv "$staged" "$dest"
done < <(find "$stage" -type f -name '*.graphql')

echo "Vendored $count SDL files into schema/."

# Re-stamp the version constant from version.txt.
(cd "$schema_dir" && go run gen.go)
echo "Stamped schema/version_gen.go ($ref)."
