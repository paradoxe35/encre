#!/usr/bin/env bash
# Prints the changes in a release, from the commits since the release before it, grouped by kind.
# Usage: scripts/release-notes.sh v0.1.9
set -euo pipefail

tag=${1:?usage: release-notes.sh TAG}
previous=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' "$tag^" 2>/dev/null || true)
subjects=$(git log --no-merges --format=%s "${previous:+$previous..}$tag")

# section TITLE FILTER: the subjects grep keeps with FILTER, without their prefix, as a list.
section() {
	local lines
	lines=$(grep $2 <<<"$subjects" | sed -E 's/^[a-z]+(\([^)]*\))?!?: *//; s/^([a-z]+)( |$)/\u\1\2/; s/^/- /' || true)
	if [ -n "$lines" ]; then
		printf '## %s\n\n%s\n\n' "$1" "$lines"
	fi
}

section "New" "-E ^feat(\(.+\))?!?:"
section "Fixes" "-E ^fix(\(.+\))?!?:"
section "Other changes" "-vE ^(feat|fix)(\(.+\))?!?:"
