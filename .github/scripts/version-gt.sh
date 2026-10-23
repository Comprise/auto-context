#!/bin/sh
# Usage: version-gt.sh <a> <b>
# Exits 0 when semver a (X.Y.Z[-pre], no leading v) is higher than b, else 1.
# A release is higher than its own pre-releases (2.0.0 > 2.0.0-rc2); pre-release
# suffixes compare with sort -V (rc2 < rc10). Build metadata is not supported.
set -eu
a=${1:?usage: version-gt.sh <a> <b>}
b=${2:?usage: version-gt.sh <a> <b>}
[ "$a" != "$b" ] || exit 1
core_a=${a%%-*} core_b=${b%%-*}
if [ "$core_a" != "$core_b" ]; then
	[ "$(printf '%s\n%s\n' "$core_a" "$core_b" | sort -V | tail -n 1)" = "$core_a" ]
	exit
fi
case $a in *-*) pre_a=${a#*-} ;; *) exit 0 ;; esac # a is the release of b's pre-release
case $b in *-*) pre_b=${b#*-} ;; *) exit 1 ;; esac
[ "$(printf '%s\n%s\n' "$pre_a" "$pre_b" | sort -V | tail -n 1)" = "$pre_a" ]
