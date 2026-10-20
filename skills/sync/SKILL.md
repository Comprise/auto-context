---
name: sync
description: This skill should be used when the user runs /auto-context:sync (or $sync in Codex) to check auto-context docs now and bring stale or missing unit docs up to date (after git pull, branch switches or manual edits between sessions).
disable-model-invocation: true
allowed-tools: Bash(auto-context status --stale)
---
# /auto-context:sync (Codex: $sync)

In Codex, `auto-context` is not on PATH: run the commands below by the path the hook gave (`~/.codex/plugins/cache/<marketplace>/auto-context/<version>/bin/auto-context`).

Current state (`auto-context status --stale`):
!`auto-context status --stale`

1. Read the state above (if there is no `auto-context status --stale` output above, as in Codex,
   run `auto-context status --stale` yourself), including any `warning:` and `note:` lines (report them).
   - It starts with `error:`, or shows neither a `UNIT` table nor `all docs up to date` (and is not the
     paused message below): run `auto-context status --stale` via Bash; if that fails too, report the
     error and stop.
   - `docs tracking is paused until … (see the warning above); no table is printed`: the config cannot
     be read or parsed, and
     `ack` refuses. Tell the user to fix `.auto-context/config.yaml` and stop.
   - `all docs up to date` and no `belongs to no unit` note: say so and stop.
2. Invoke the `auto-context:auto-context-docs` skill (Codex: `$auto-context-docs`); follow it for every doc.
3. `missing` units: ask the user once, listing them all, which to document (step "1. Decide"). `_root` cannot be
   excluded: if it is missing, create `context/_root.md` (and the root index file purpose line).
4. `note: … belongs to no unit`: apply the "Unit removed" rule of the docs skill (it asks the user first).
   Do this before the stale units: a removed unit's code may now belong to a stale parent.
5. Units with a note (`stale (…)`): handle them as "Status notes" of the docs skill says; the
   `not acked yet` and `broken frontmatter` ones have no usable ack date: read the code against
   `context/<unit>.md`, then apply step "1. Decide". Each other `stale` unit: run
   `auto-context changes <unit>` (commits since the ack day, uncommitted and untracked files; do not
   run git yourself). If it shows nothing relevant or has no ack date, read the code against
   `context/<unit>.md`. Then apply step "1. Decide" of the docs skill.
6. Run `auto-context status --stale` again and report the result briefly.
