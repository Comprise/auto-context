#!/bin/sh
# Usage: go test -json ./... | check-skips.sh [allowed-skips.txt]
# Fails when a test was skipped that the allow list does not name, so a test
# whose setup silently stopped working (it skips instead of failing) is noticed.
# Also fails when the input has a failed test or package (a build failure, a
# panic outside a test) or no test results at all; it then prints their output,
# since with -json this is the only readable log of the run.
# Allow list: one "<package> <test>" per line (a subtest as Test/sub), '#'
# starts a comment.
set -eu
allowed=${1:-$(dirname "$0")/allowed-skips.txt}
[ -r "$allowed" ] || { echo "check-skips: cannot read $allowed" >&2; exit 2; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cat > "$tmp/events"
jq -r 'select(.Test != null and .Action == "skip") | "\(.Package) \(.Test)"' "$tmp/events" | sort -u > "$tmp/skipped"
jq -r 'select(.Test != null and (.Action == "pass" or .Action == "fail" or .Action == "skip")) | .Action' "$tmp/events" > "$tmp/results"
jq -r 'select(.Action == "fail") | "  \(.Package) \(.Test // "(package)")"' "$tmp/events" > "$tmp/failed"
sed -e 's/#.*//' -e 's/[[:space:]]*$//' -e '/^$/d' "$allowed" | sort -u > "$tmp/allowed"

if [ -s "$tmp/failed" ]; then
	# The output of each failed test and package, and any build output.
	jq -sj '[.[] | select(.Action == "fail") | {p: .Package, t: (.Test // "")}] as $f
		| .[] | select(.Action == "build-output"
			or (.Action == "output" and ({p: .Package, t: (.Test // "")} as $k | any($f[]; . == $k))))
		| .Output' "$tmp/events" >&2
	echo "check-skips: failed:" >&2
	cat "$tmp/failed" >&2
	exit 1
fi
if [ ! -s "$tmp/results" ]; then
	echo "check-skips: no test results in the input" >&2
	exit 1
fi
grep -vxF -f "$tmp/allowed" "$tmp/skipped" > "$tmp/unexpected" || true
if [ -s "$tmp/unexpected" ]; then
	echo "check-skips: unexpected skips (fix the test setup or add the test to $allowed with a reason):" >&2
	while read -r pkg test; do
		echo "  $pkg $test" >&2
		# The skip reason: the test's own output lines, without the --- SKIP line.
		jq -j --arg p "$pkg" --arg t "$test" \
			'select(.Package == $p and .Test == $t and .Action == "output") | .Output' "$tmp/events" |
			grep -v -e '^=== ' -e '^--- SKIP' | sed 's/^/    /' >&2 || true
	done < "$tmp/unexpected"
	exit 1
fi
echo "check-skips: $(grep -cx pass "$tmp/results") passed, $(wc -l < "$tmp/skipped" | tr -d ' ') skipped (all allowed)"
