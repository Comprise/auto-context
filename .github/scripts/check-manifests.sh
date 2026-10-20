#!/bin/sh
# Usage: check-manifests.sh <dir> [version]
# Checks the plugin manifests of a tree. Without a version the manifests must
# not carry one (main has none; the release stamps it from the tag).
set -u
dir=${1:?usage: check-manifests.sh <dir> [version]}
ver=${2-}

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
