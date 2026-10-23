#!/bin/sh
# Usage: fuzz.sh [fuzztime]
# Runs every Fuzz* target of the module for fuzztime each (default 20s). go test
# fuzzes one target of one package per run, so the targets are listed from the
# *_test.go files. A failure leaves its input in testdata/fuzz of the package.
set -eu
t=${1:-20s}
found=0 failed=0
for f in $(grep -rl --include='*_test.go' '^func Fuzz' cmd internal); do
	for fn in $(sed -n 's/^func \(Fuzz[A-Za-z0-9_]*\)(.*/\1/p' "$f"); do
		found=$((found + 1))
		echo "== $fn ($(dirname "$f"))"
		go test -run='^$' -fuzz="^$fn\$" -fuzztime="$t" "./$(dirname "$f")" || failed=$((failed + 1))
	done
done
[ "$found" -gt 0 ] || { echo "fuzz: no fuzz targets found" >&2; exit 1; }
echo "fuzz: $found targets, $failed failed"
[ "$failed" -eq 0 ]
