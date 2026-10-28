# Changelog

Russian version: [CHANGELOG.ru.md](CHANGELOG.ru.md). Release notes of a tag are the section `## vX.Y.Z` of this file.

## v1.1.1

### Breaking

- **No more `dist/vX.Y.Z` tags.** Releases no longer tag their commit on the `release` branch, and
  the tag `dist/v1.1.0` is deleted. Pinning a version by `--ref dist/vX.Y.Z` no longer works; install
  from the `release` branch. For maintainers, `rollback.yml` now takes `to: vX.Y.Z` and finds the
  commit `release vX.Y.Z` on the `release` branch.

## v1.1.0

### Breaking

- **Upgrade every machine in the team together.** Plugin v1.0.0 does not understand `v2:` hashes
  and rewrites them back to `sha256:` on its own auto-ack. With mixed plugin versions the
  frontmatter flips back and forth in commits.
- **Hash scheme v2.** `source_hash` is now `v2:sha256:<hex>` and is computed over the files of the
  unit as git sees them (only entries in the git index; untracked files no longer count; in
  walk mode, all files found). `_root` hashes the units in the same index view. Docs with an old
  `sha256:` hash are shown as `ok (old hash scheme)` while they still match the old rules;
  `auto-context ack --upgrade` converts them to v2.
- **Known one-time upgrade costs.** A unit that has a file larger than 32 MiB, and `_root` when a
  directory with a space in its name used to be a unit, show `stale` once after the update; run
  `auto-context ack <unit>` (or `ack --upgrade`) after checking the docs.
- **`units.SafeName` is stricter:** spaces and a leading `-` in any path segment are forbidden. Such
  directories are no longer units and their docs become orphans (`status` says so).
- **Exit codes:** 0 success, 1 only `status --exit-code` with stale or missing docs, 2 wrong usage,
  3 failure. `ack`, `units` and `changes` failures (cannot open the project, broken config, refused
  ack) now exit with 3 instead of 1.
- **`--json`** for `status` and `units` (`format_version: 1`); combines with `--stale` and `--exit-code`.
- **Limits:** at most 800 000 files per repository; files larger than 32 MiB are hashed by sampling
  (size plus the first and last 1 MiB).
- **Hash cache is now `.auto-context/cache.v5.bin`** (binary). Older cache files are ignored and
  can be deleted; `auto-context version -v` reports `cache 5`.
- **Frontmatter:** unknown keys are now kept on write. The first `ack` may reformat nested or folded
  values of unknown keys.
- Projects created with v1 may pin the four default keys (`code_extensions`, `module_markers`,
  `exclude`, `min_files`) in `.auto-context/config.yaml` so that future default changes do not alter
  hashes (the `init` skill now writes them).
- The YAML module is `go.yaml.in/yaml/v3` (replaces `gopkg.in/yaml.v3`).

### Fixed

- Windows: removing a stale lock no longer deletes a fresh lock that replaced it, and a doc or
  state file that another process holds open is replaced after a short retry instead of failing.
- git can no longer run programs from a repository's `.git/config` (lazy-fetch transports,
  `gpg.program`, other transports); timeouts kill git's whole process group; a foreign work tree
  (`core.worktree`, a `.git` file pointing elsewhere, bare) is refused, linked worktrees and
  submodules still use git.
- No repository text reaches Claude's context or the terminal except sanitized unit names, numbers,
  fixed categories and the output of `changes`.
- The shim ignores empty and relative `PATH` entries for its own utilities and still passes the
  original `PATH` to the binary.
- Quadratic parsing of the root index section is gone; the hook log's rotation lock is more robust (a stale lock is removed only if it is still the same file; flock uses `SyscallConn`).
- Unit set changes inside a turn, SessionStart steps, `--exclude`, the cheap index view and `changes`
  in the service layer, hook input handling and several smaller defects from the full review of
  2026-10-23.
- A hook that runs out of time stops git, file discovery and hashing and answers with what it has (units not hashed, or a time-up notice) instead of being killed.
- Parallel sessions in one tree keep each other's hashes in the cache.
- Linked worktrees created with relative paths (`worktree.useRelativePaths`, git 2.48+) use git
  instead of falling back to walking the tree.
- On Linux git dies with a hook the agent kills on its timeout instead of being left running.
- `ack` keeps the comments of unknown frontmatter keys in files with CRLF line ends.
- `/auto-context:sync` also reports docs acked in the old hash scheme and suggests `auto-context ack --upgrade`.

### Added

- `auto-context ack --upgrade`, `auto-context version -v` (commit, Go version, OS/arch).
- `SECURITY.md`, `docs/RELEASING.md` (hotfix, yank, rollback, pinning), issue templates.

## v1.0.0

First stable release.

### Breaking

- Frontmatter has a new `commit` key; a block without `unit` or `source_hash` is no longer treated
  as ours; a BOM is preserved. An older plugin (v0.0.x) erases `commit` on `ack`, and `changes` then
  falls back to searching by date: keep one plugin version across a team.
- `.auto-context/cache.json` version 2 became 3 (older caches are discarded silently).
- Config `index_file` is stricter: a duplicate key or a non-string value breaks the whole config.
