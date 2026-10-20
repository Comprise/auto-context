# auto-context core correctness audit

Scope: internal/units, hashing, docstate, session, project, rootindex, config, hooks/stop.go + prompt_submit.go, cmd/auto-context. Security excluded.
Method: read the source; checked each finding with the built binary or with throwaway tests in a copy of the repo
(scratchpad/audit-core/repo, test file internal/units/zz_audit_test.go; scenario dirs t1..t6). The repo itself was not modified.
`go test ./...` passes on the original code.

Areas checked and found correct (no finding): CRLF normalization in lfWriter (also across chunk boundaries), cache dirty/prune/version
handling, snapshot encode/decode, journal set normalization, pause/resume flow, AlreadyNotified/AckFailNotified dedup (no Stop loop
with stop_hook_active ignored), rootindex marker balancing and heading preservation, config index_file validation (dup/non-string/
null), empty repo / no git / root ignored by an outer repo, and about 20 extra .gitignore forms compared with `git` (anchoring, `**`,
dir-only, negation inside ignored dirs, escapes, trailing spaces/tabs, classes). Only the brace case below differs.

---

## 1. Medium: a new unit created together with its docs never flags its parent or _root
`internal/hooks/stop.go:97-107`

The parent/_root check only runs for *pending* units whose doc is `DocAbsent`. When Claude creates a unit and writes its
`context/<u>.md` in the same turn, the unit is auto-acked, so it is not pending. The parents are never checked. But _root's hash
covers the unit list, and the parent loses the files, so both go stale without any Stop feedback.

Repro (verified, scratch t2): acked repo with `main.go` and `context/_root.md` (status: `_root ok`). Then prompt-submit, create
`svc/go.mod`, `svc/a.go`, `context/svc.md`, then stop. Result: **no output**. `status` then shows `_root stale`, `svc ok`. The same
happens when an existing subdirectory of unit `a` crosses `min_files` and gets docs: `a` silently loses files.

Fix: collect the "new unit" candidates from every unit that is new this turn (its `context/<u>.md` or the unit itself was not in the
turn-start snapshot), from `acked` and from `pending`, and not only from pending DocAbsent units. Then run the parent/_root
`pendingUnits` check for them. A simpler option: whenever the set of units differs from the one at turn start, add `_root` and the
parents of the added or removed units. That needs the unit list stored next to the snapshot.

## 2. Medium: `auto-context ack` writes the root CLAUDE.md even when every ack fails and in inactive projects
`cmd/auto-context/main.go:303-313` (`RefreshIndex` at :310)

`runAck` always calls `p.RefreshIndex()`, whatever the result of the acks and whether or not the project is active. In a repo with no
`.auto-context/config.yaml` and no `context/_root.md`, `auto-context ack typo` prints `unknown unit typo` and exits 1. It still
creates (or appends to) the root `CLAUDE.md` with an `auto-context:units` section listing every discovered unit. The hooks' PreToolUse
auto-approves `auto-context ack ...` in any project, so Claude can trigger this without any prompt.

Repro (verified, scratch t1): `git init`, `pkg/f1..f5.go`, `auto-context ack typo` gives exit 1, and an untracked `CLAUDE.md` with the
units section now exists.

Fix: refresh the index only if at least one ack succeeded and `project.Active(root)` is true. Alternatively, put the `Active`
guard into `Project.UpdateIndex`, as `SaveCache` already does.

## 3. Low: auto-ack orders the doc edit against the mtime of moved or renamed files, so a file moved in after the docs edit is acked
`internal/hooks/stop.go:209-231` (`mt = max(mt, fs.MtimeNs)` at :214, check at :228)

`newestCode` uses the file's mtime. `mv`, `git mv`, `cp -p`, `tar x` and `rsync -t` keep the old mtime, so a file added to a unit
after `context/<u>.md` was edited looks older than the doc. The unit then counts as "fresh" and is auto-acked, and the reminder is
suppressed.

Repro (verified, scratch t6): `other/moved.go` with mtime 2020. prompt-submit, edit `context/pkg.md`, then
`mv other/moved.go pkg/`, then stop. Result: only `_root` is reported, and `pkg` was **auto-acked** (`status`: `pkg ok`), although
its code changed after its docs.

Fix: for paths that are added or removed in the diff (not just modified), use the time of the Stop (or `max(mtime, ctime)` where
available) as their change time. Alternatively, never auto-ack a unit whose added files have an mtime older than the turn-start
snapshot.

## 4. Low: parallel sessions in one tree get feedback (and auto-acks) for each other's edits
`internal/hooks/stop.go:48-84` (with `session.Diff`)

Each session diffs the whole tree against its own turn-start snapshot. So an edit by session A (or by the user in an editor) during
B's turn is attributed to B.

Repro (verified, scratch t4): prompt-submit A, prompt-submit B, append to `pkg/f1.go`, stop A, stop B. **Both** sessions print
"code changed in pkg but docs not updated". If B edited `context/pkg.md` after A's code edit, B auto-acks pkg for A's change.

Fix (if this is not an accepted limitation): it cannot be fully fixed with stat diffs alone. Options: record the paths Claude's own
tools wrote (PostToolUse on Edit/Write) and intersect them with the diff, or at least document it next to the cache.json note in
`context/_root.md`.

## 5. Low: walk mode (no git) makes units of directories with no tracked files; git mode does not
`internal/units/walk.go:701` (`dirs[rel] = &dirInfo{}` for every walked directory)

The walk records every directory it enters, including empty ones and ones holding only gitignored or non-source files. In git mode,
`dirs` only has ancestors of listed files. With an `include` pattern, walk mode therefore yields extra 0-file units, which stay
`missing` forever and appear in the root index. This breaks the documented invariant that git and walk modes give the same result,
and the unit set flips when git fails and the walk fallback runs.

Repro (verified, throwaway test `TestAuditEmptyDirUnit`): `include: [packages/*]`, `packages/a/x.go`, `packages/old/out.log` (ignored
by `*.log`), empty `packages/empty`.
walk: `packages/a, packages/empty, packages/old` (0 files each). git: `packages/a` only.

Fix: in the walk, create `dirInfo` only from `addFile` (for files that pass the checks), as git mode does, and add their ancestors
the same way git mode does.

## 6. Low: in walk mode, a `.gitignore` rule `*<suffix>` with `{` or `}` never matches
`internal/units/walk.go:202` together with `:225-229` and `:294`

For glob rules, braces are escaped into `r.pattern` (`*{a}.go` becomes `*\{a\}.go`). The suffix bucket is chosen from the unescaped
line, but the match compares `base` against `r.pattern[1:]`, which is the escaped text. So the rule never matches.

Repro (verified, `TestAuditBraceSuffix`): `.gitignore` `*{a}.go\n*}.go\n`, files `x{a}.go`, `y.go`, `z}.go`.
walk tracks `[x{a}.go y.go z}.go]`. git tracks `[y.go]`.

Fix: store the unescaped suffix for suffix rules (for example `r.suffix = l[1:]`) and compare against that. The alternative is to
route lines containing braces to `other`.

## 7. Low: `ack --exclude` leaves the excluded unit in the root index
`cmd/auto-context/main.go:277-291`

The `--exclude` path writes the config and returns without refreshing the index. The section keeps listing the unit until the next
SessionStart or ack.

Repro (verified, scratch t4): after SessionStart lists `newu`, `auto-context ack --exclude newu` exits 0, and the root CLAUDE.md
still contains ``- `newu` ``.

Fix: after a successful `Exclude`, reopen the project (the tree must be rediscovered with the new config) and call `RefreshIndex()`,
behind the same `Active` guard as in finding 2.

## 8. Low: LF lines are written into CRLF files (root index section, frontmatter)
`internal/rootindex/rootindex.go:40-51,133-138`; `internal/frontmatter/frontmatter.go:106`

`render` and the separator logic always use `\n`, and `SetHash` writes `---\n...---\n`. In a CRLF root CLAUDE.md or context doc
(Windows without autocrlf, or `eol=crlf`), the result has mixed line endings. Hashing and parsing still work, but the file shows
noisy diffs and some editors or linters flag it.

Repro (verified, scratch t5): a CRLF `CLAUDE.md` and `context/_root.md`, then `ack _root`. `od -c` shows `\r\n` in the user's lines
and bare `\n` in the units section and in the frontmatter.

Fix: detect the dominant line ending of the existing content (for example, whether the first line ends with `\r\n`) and use it for
the inserted lines.
