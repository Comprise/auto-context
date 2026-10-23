#!/bin/sh
# Usage: check-manifests.sh <dir> [version [release]]
# Checks the plugin manifests of a tree. Without a version the manifests must
# not carry one (main has none; the release stamps it from the tag). With
# "release" the tree is the one users install: it must also hold the six
# binaries and a checksums.txt that lists exactly them, with paths relative to
# the tree (so `sha256sum -c checksums.txt` works in the plugin directory).
set -u
dir=${1:?usage: check-manifests.sh <dir> [version [release]]}
ver=${2-}
release=${3-}

fail() { echo "check-manifests: FAIL: $1" >&2; exit 1; }
chk() { # name file jq-filter [jq args...]
	name=$1; file=$2; filter=$3; shift 3
	jq -e "$@" "$filter" "$dir/$file" >/dev/null 2>&1 || fail "$name ($file)"
}

for m in .claude-plugin/plugin.json .codex-plugin/plugin.json; do
	chk "name of $m" "$m" '.name == "auto-context"'
	if [ -n "$ver" ]; then
		chk "version of $m" "$m" '.version == $v' --arg v "$ver"
	else
		chk "no version in $m" "$m" 'has("version") | not'
	fi
done
chk "codex hooks path" .codex-plugin/plugin.json '.hooks == "./hooks/codex.json"'
chk "marketplace plugin name" .agents/plugins/marketplace.json '.plugins[0].name == "auto-context"'
chk "marketplace plugin source" .agents/plugins/marketplace.json '.plugins[0].source == {"source":"local","path":"./"}'
chk "codex hook events" hooks/codex.json '.hooks | keys == ["PermissionRequest","SessionEnd","SessionStart","Stop","UserPromptSubmit"]'
[ -x "$dir/bin/auto-context" ] || fail "bin/auto-context is not executable"
grep -qx 'bin/auto-context text eol=lf' "$dir/.gitattributes" 2>/dev/null || fail ".gitattributes keeps bin/auto-context LF"
if [ "$release" = release ]; then
	n=0
	for b in "$dir"/libexec/auto-context-*; do
		[ -f "$b" ] || continue
		n=$((n + 1))
		grep -q "  libexec/${b##*/}\$" "$dir/checksums.txt" 2>/dev/null || fail "checksums.txt lists libexec/${b##*/}"
	done
	[ "$n" -eq 6 ] || fail "libexec has $n binaries, want 6"
	[ "$(wc -l < "$dir/checksums.txt")" -eq 6 ] || fail "checksums.txt lists only the six binaries"
	(cd "$dir" && sha256sum -c --quiet checksums.txt) || fail "checksums.txt matches the binaries"
fi
