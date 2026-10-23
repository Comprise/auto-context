#!/bin/sh
# Usage: install-claude.sh (in GitHub Actions, from the repository root)
# Installs the claude CLI pinned by .github/tools/package-lock.json and puts it
# on PATH for the next steps. npm runs no install scripts of the dependency
# tree; only the CLI's own installer, which links its native binary from the
# locked platform package, runs explicitly.
set -eu
tools=$(cd "$(dirname "$0")/../tools" && pwd)
npm ci --ignore-scripts --no-audit --no-fund --prefix "$tools"
node "$tools/node_modules/@anthropic-ai/claude-code/install.cjs"
"$tools/node_modules/.bin/claude" --version
echo "$tools/node_modules/.bin" >> "${GITHUB_PATH:?not in GitHub Actions}"
