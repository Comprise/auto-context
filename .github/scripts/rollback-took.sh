#!/bin/sh
# Usage: rollback-took.sh <tag> <rev>
# Exits 0 when a commit in the history of rev is a rollback that took the
# version of tag (rollback.yml writes the subject "rollback to vA.B.C as <tag>"),
# else 1. A missing rev (no release branch yet) counts as not taken.
set -eu
tag=${1:?usage: rollback-took.sh <tag> <rev>}
rev=${2:?usage: rollback-took.sh <tag> <rev>}
git rev-parse -q --verify "$rev^{commit}" >/dev/null || exit 1
git log --format=%s "$rev" | awk -v s=" as $tag" '
	length($0) > length(s) && substr($0, length($0) - length(s) + 1) == s && /^rollback to / { found = 1 }
	END { exit !found }'
