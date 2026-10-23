#!/bin/sh
# Usage: release-notes.sh <tag> [CHANGELOG.md]
# Prints the section "## <tag>" of the changelog (without the heading) for the
# release notes. A pre-release tag (v2.0.0-rc1) without a section of its own
# uses the section of its release (v2.0.0). Fails when there is no non-empty
# section, so a release is never published without notes.
set -eu
tag=${1:?usage: release-notes.sh <tag> [CHANGELOG.md]}
file=${2:-CHANGELOG.md}
[ -r "$file" ] || { echo "release-notes: cannot read $file" >&2; exit 1; }

section() {
	awk -v h="## $1" '
		$0 == h { on = 1; next }
		on && /^## / { exit }
		on { print }
	' "$file" | sed -e '/./,$!d' # leading blank lines; $() drops the trailing ones
}

notes=$(section "$tag")
if [ -z "$notes" ] && [ "${tag%%-*}" != "$tag" ]; then
	notes=$(section "${tag%%-*}")
fi
if [ -z "$notes" ]; then
	echo "release-notes: no section \"## $tag\" in $file" >&2
	exit 1
fi
printf '%s\n' "$notes"
