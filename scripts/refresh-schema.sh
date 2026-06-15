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

# Drop the previously vendored SDL so an upstream-removed file cannot linger.
# Only *.graphql are touched — never version.txt, the *.go stamp, or catalog.json.
rm -f "$schema_dir"/*.graphql "$schema_dir"/types/*.graphql

count=0
while IFS= read -r p; do
	[ -z "$p" ] && continue
	dest="$schema_dir/${p#graphql/schema/}"
	mkdir -p "$(dirname "$dest")"
	gh api "repos/$repo/contents/$p?ref=$ref" -H "Accept: application/vnd.github.raw" >"$dest"
	count=$((count + 1))
done < <(printf '%s\n' "$paths")

echo "Vendored $count SDL files into schema/."

# Re-stamp the version constant from version.txt.
(cd "$schema_dir" && go run gen.go)
echo "Stamped schema/version_gen.go ($ref)."
