# Releasing

Repository: `Comprise/auto-context`. Users install from the `release` branch; the version is set by
`release.yml` from the tag, never stored in `main`.

## Normal release

1. Add a section `## vX.Y.Z` to `CHANGELOG.md` and `CHANGELOG.ru.md` (subsections `### Breaking`,
   `### Fixed`, `### Added`). The release workflow cuts the section of its tag into the GitHub Release.
2. Tag a commit of `main`: `git tag vX.Y.Z && git push origin vX.Y.Z`. Tags with a `-` suffix
   (`vX.Y.Z-rc1`) do not touch the `release` branch.
3. The workflow builds six binaries, provenance and `checksums.txt`, and adds a new commit on top of
   `release` (no force-push, only to a higher version). It also creates the tag `dist/vX.Y.Z` on that commit.

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
`to: dist/vX.Y.Z`, the tag of the last good tree. It re-commits that tree on top of `release` with a
bumped patch version; history is not rewritten and nothing is force-pushed.

## Pinning a version

Users who must stay on a version install by the tag `dist/vX.Y.Z` (it points to the commit of the
`release` branch holding that version's tree), for example in Codex with `--ref dist/vX.Y.Z`. Pin
by tag, never by branch.

## Release branch size

(To be filled in.)
