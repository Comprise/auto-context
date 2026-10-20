# Integration and packaging audit (read-only; security excluded)

Checked and found consistent: hook timeouts vs budgets (60/55, 30/25, 10/8, Codex SessionEnd 3/2); hooks.json/codex.json commands vs hookHandlers (covered by tests in cmd/auto-context/main_test.go); SessionStart/Stop/PermissionRequest JSON shapes vs the spec; shim `$2` for `hook <event> --agent codex`; hook-exit; file lists in ci strict tree / tar / git add -f; version stamping of both manifests; exec bit (git mode 100755 + chmod); status/units/changes strings quoted in skills match Go sources (all docs up to date, docs tracking is paused..., stale (not acked yet / broken frontmatter / symlink outside repo / unreadable doc / cannot hash sources), note: ... belongs to no unit, note: N error(s) in .auto-context/log, cannot be read or parsed; fix it first).

## Findings

1. Medium - skills/init/SKILL.md:7 (same line in sync:9, status:7, auto-context-docs:7), internal/hooks/session_start.go:36-45
   Codex skills say "run by the path the hook gave", but in a not-yet-activated project SessionStart returns nil (no output), and PermissionRequest/Stop never fire before activation. On the first `$auto-context:init` the agent has no path (PATH lacks bin/, CLAUDE_PLUGIN_ROOT is empty in Codex's shell per spec) and `auto-context units` fails with "command not found"; the agent has to guess/glob `~/.codex/plugins/cache/...`.
   Fix: have the skill tell the agent to locate the binary (`ls ~/.codex/plugins/cache/*/auto-context/*/bin/auto-context`, pick newest) or have SessionStart print the path even for inactive projects (only the path hint, no writes).

2. Medium - internal/hooks/agent.go:50,57-63
   `pluginRootSafe = ^/[A-Za-z0-9_./-]*$` rejects Windows roots (C:\..., /c/Users/... passes only if POSIX), any home dir with a space, `@`, `+`, `~` or non-ASCII. CLI() then falls back to bare `auto-context`, which is not on PATH in Codex, so every hook-suggested command (`ack`, `status`) fails there, and approvable() has no absolute form to accept.
   Fix: for Codex, single-quote the root in suggested commands (POSIX-quote with `'\''`) and allow any root without control chars/newline, or have the skill fall back to locating the binary (see 1).

3. Medium - README.md (Требования/Windows), hooks/codex.json:3-7
   README advertises Windows for both agents and documents only Git Bash (a Claude Code setting, CLAUDE_CODE_GIT_BASH_PATH). Codex commands are `"${CLAUDE_PLUGIN_ROOT}"/bin/auto-context ...` (POSIX sh syntax, sh shim); nothing in the spec's step 0 verifies how Codex runs hook commands on Windows (PowerShell/cmd would not expand `${...}` nor run a shebang script). Hooks would silently never work.
   Fix: verify on Windows Codex; otherwise state "Codex: Linux/macOS (WSL) only" in README.

4. Low - internal/hooks/stop.go:21-25,164-175
   Claude Stop answers with `hookSpecificOutput:{hookEventName:"Stop",additionalContext}`. Claude Code documents Stop output as `decision/reason`; hookSpecificOutput.additionalContext is documented for SessionStart/UserPromptSubmit/PostToolUse etc. Could not be verified offline; if a Claude Code release validates hookEventName strictly the feedback is dropped or shown as a hook error, and there is no test against the real binary. (Spec 2026-10-19:238 records the choice but not a live check.)
   Fix: confirm against the pinned 2.1.304 (and CI-pinned claude) with a live run; keep decision:block as the fallback.

5. Low - bin/README.md:5 (line "Включённый проект шим определяет сам, без поиска корня ... в CLAUDE_PROJECT_DIR (или в .)") vs bin/auto-context:15-30
   Shim now walks up to .git or .auto-context/config.yaml (stopping below $HOME). The README is stale.
   Fix: update the bullet.

6. Low - hooks/README.md:1-12
   Describes only hooks.json; no mention of hooks/codex.json, PermissionRequest (matcher ^Bash$, 10 s), Codex SessionEnd 3 s (budget 2 s), or that Codex has no `if`.
   Fix: add a Codex table.

7. Low - README.md ("Если что-то не работает"), README.md Codex section
   Lists SessionEnd as an expected hook in Codex /hooks while the spec (Поправки по шагу 0) says it does not fire on TUI exit; old session files are only cleaned by SessionStart after 7 days. Not stated for users.
   Fix: note it.

8. Low - bin/auto-context:20 vs cmd/auto-context/main.go:151-157
   Shim `active()` starts from `${CLAUDE_PROJECT_DIR:-.}` (process cwd), the binary uses payload `cwd` when CLAUDE_PROJECT_DIR is unset (always under Codex). If Codex's process cwd differs from the payload cwd, the shim's "binary missing/crashed" notice is shown/suppressed for the wrong project.
   Fix: low impact; document, or accept that the notice is best effort.

9. Low - .github/workflows/release.yml:243-245,295
   checksums.txt lists `libexec/<binary>` hashes but only the tar.gz and checksums.txt are attached; the individual binaries (and attestations' subjects) cannot be checked against the file list by users, and `sha256sum -c` run from a download dir fails on paths.
   Fix: attach libexec/* to the release, or hash the extracted tree entries consistently.

10. Low - .github/workflows/ci.yml:139,146-151; release.yml:246-253
   Codex manifests are checked only by `jq -e .` (syntax); `claude plugin validate --strict` ignores .codex-plugin/.agents/hooks/codex.json. Structure is protected only by Go tests (main_test.go:1095-1180), which are skipped when ci's paths-ignore matches (not for these files, fine) but not run against the extracted release tree.
   Fix: optionally run the manifest test against the extracted tree or jq-assert required keys (name, hooks, plugins[0].source.path).

11. Low - Makefile:7-13
   `make dist` builds binaries only; the plugin tree for `claude --plugin-dir` / local Codex still has unstamped manifests (fine) but `dist` does not mention or create the `libexec` dir permissions; no `go vet` in `make test` (CI runs it). Minor dev/CI drift.
   Fix: add `vet` to `test`.
