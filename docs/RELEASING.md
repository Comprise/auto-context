# Releasing

Repository: `Comprise/auto-context`. Users install from the `release` branch; the version is set by
`release.yml` from the tag, never stored in `main`.

## Normal release

1. Add a section `## vX.Y.Z` to `CHANGELOG.md` and `CHANGELOG.ru.md` (subsections `### Breaking`,
   `### Fixed`, `### Added`). The release workflow cuts the section of its tag into the GitHub Release.
2. Tag a commit of `main`: `git tag vX.Y.Z && git push origin vX.Y.Z`. Tags with a `-` suffix
   (`vX.Y.Z-rc1`) do not touch the `release` branch.
3. The workflow builds six binaries, provenance and `checksums.txt`, and adds a new commit on top of
   `release` (no force-push, only to a higher version). The commit's subject is `release vX.Y.Z`.

Pre-releases:

- A tag with a `-` suffix moves the branch `release-next` instead of `release` (same rules: higher
  version only, no force-push). It can be installed to try a pre-release (`--ref release-next`).
- A version the branch does not move to (not newer than the branch head) gets only its GitHub
  Release; no commit of the branch holds its tree.
- If the publish job refuses (for example a version a rollback took), the draft GitHub Release it
  already created stays. Delete the draft (`gh release delete vX.Y.Z --yes`), fix the cause, tag again.

## Hotfix

1. Fix on `main` (or revert the bad change), update the CHANGELOG.
2. Tag the next patch version, `vX.Y.(Z+1)`. The branch only moves to a higher version, so a fix is
   always a roll-forward.
3. For a security fix, work from the private advisory (see `SECURITY.md`) and publish the GHSA after
   the release is out.

## Yank (withdraw a bad release)

Installs come from the `release` branch, not from GitHub Releases, so deleting a Release does not
stop anyone. Instead:

1. Roll back (below) so new installs get the last good tree.
2. Mark the GitHub Release of the bad version as pre-release and add a note at the top of its text
   ("yanked: use vX.Y.Z"); keep the tag.
3. Mention the yank in the `### Fixed` section of the next CHANGELOG release.

## Rollback

Run the workflow `rollback.yml` (Actions tab, "Run workflow", `workflow_dispatch`) with the input
`to: vX.Y.Z`, the last good version. It takes the tree of the commit `release vX.Y.Z` of the
`release` branch and re-commits it on top of `release` with a bumped patch version; history is not
rewritten and nothing is force-pushed.

After a rollback:

- The next real release tag must be above the rollback version (the rollback takes the version
  `current+1`, commit subject `rollback to vA.B.C as v<that version>`). `release.yml` refuses a
  version a rollback took, in `guard` and again in `publish`; tag a higher version.
- The rolled-back tree keeps the binaries it was built with, so `auto-context version` still prints
  the old version while the plugin manifests say the rollback version.

## Release branch size

Measured on 2026-10-24 with a throwaway `HOME` (claude 2.1.309, codex-cli 0.163.0), repository
`Comprise/auto-context`, `release` at v1.0.0 (4 commits):

| What | Result |
| --- | --- |
| `git clone --single-branch --branch release` | `.git` 17 MB, 4 commits |
| same with `--depth 1` | `.git` 8.1 MB |
| Codex `plugin marketplace add Comprise/auto-context [--ref release]` | NOT shallow (`is-shallow-repository` false), all branches, `.git` 18 MB |
| Claude Code `plugin marketplace add` | clones `main` shallow (468 KB); the plugin is installed from `ref: release` (`gitCommitSha` 59f8fea), but the cache copy has no `.git` and the temporary clone is gone, so the depth of that clone is not measured |

Each release adds about 24 MB of binaries (uncompressed) that hardly delta-compress; the full
clone is about twice the shallow one at 4 commits, and Codex downloads all of it. Decision: Codex is not shallow and Claude Code is
unproven, so `release.yml` (step "Release branch clone size", after the branch is pushed) measures a
bare single-branch clone and emits `::warning::` above 200 MiB. It never fails the release.

Next step for the owner, when the warning appears: make each `release` commit parentless (an orphan
commit; the old binaries then stay only in the GitHub Releases, and a rollback can no longer find
the trees of old versions on `release`). That needs a
force-push to `release`, which conflicts with the ruleset that forbids it; allow it only for the
release bot through a bypass actor of the ruleset. The no-force-push guarantees of this document
then apply to `release-next` and rollback only. The alternative (download `libexec` from the
Release on first run and check it against the `checksums.txt` in the tree) needs network access and
is not planned.
