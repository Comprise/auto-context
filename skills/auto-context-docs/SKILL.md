---
name: auto-context-docs
description: This skill should be used when creating or updating a unit's index file (CLAUDE.md, or AGENTS.md when `index_file` says so) or context/<unit>.md, when an auto-context hook reports "docs stale", "missing for", "New unit", "code changed ... but docs not updated", "auto-ack of ... failed" or "docs of units that no longer exist", when the user asks to "update the docs", "document this unit", "write the context doc", "refresh CLAUDE.md" or "are the docs still right after this change", or when deciding whether a code change needs a docs update or just `auto-context ack`.
---
# auto-context docs

In Codex, `auto-context` is not on PATH: run the commands below by the path the hook gave (`~/.codex/plugins/cache/<marketplace>/auto-context/<version>/bin/auto-context`).

Each unit has two docs (for `_root`: the root index file and `context/_root.md`):
- `<unit>/<index file>` — a short index. The index file is `CLAUDE.md` by default; `index_file:
  AGENTS.md` in `.auto-context/config.yaml` selects `AGENTS.md` (Codex). The agent loads it whenever
  it works in the directory, so it holds orientation only. Below, "the index file" means this file.
- `context/<unit>.md` — the details, read on demand.

## 1. Decide
- Purpose, public API, file structure, key behavior or invariants changed → update `context/<unit>.md`
  (and `<unit>/<index file>` if the purpose or key files changed), then run `auto-context ack <unit>`.
- Anything else (internal renames not named in the docs, refactoring without behavior change, tests,
  formatting) → only `auto-context ack <unit>`.
- New unit → ask the user whether to document it. If not: `auto-context ack --exclude <unit>` (asks
  for permission; its code then counts toward the parent unit). To ignore a directory entirely, add
  `<dir>/**` to `exclude` in `.auto-context/config.yaml`; if the file has no `exclude` key yet, also copy the
  defaults (`vendor/**`, `node_modules/**`, `**/testdata/**`, `dist/**`, `build/**`): a written list
  replaces them.
- Unit removed (its directory deleted, or it no longer qualifies; `auto-context status` notes its doc
  as `belongs to no unit`) → ask the user, then delete `context/<unit>.md`. If `<unit>/<index file>` still
  exists, delete it only if it holds nothing but the purpose line, key files and the `Details:` link;
  otherwise remove just the `Details:` line. A `context/*.md` without a `unit:` frontmatter key is
  probably the user's own note, not a unit doc: ask before deleting it.

Stop auto-acks a unit only if `context/<unit>.md` was edited after the turn's last code edit there:
write the docs once the code is done, or run `auto-context ack <unit>` after the last code edit.

### Status notes
`auto-context status` marks some `stale` units:
- `stale (not acked yet)` — the doc has no frontmatter (written by hand, or an interrupted run): review
  it like any stale doc, then `auto-context ack <unit>`.
- `stale (broken frontmatter)` — do not repair the block by hand: review the doc, then `auto-context
  ack <unit>` rewrites the block.
- `stale (unusable symlink)` — the doc is a symlink that leads outside the repository, into `.git`, to a non-`.md`
  file, or nowhere: do not follow it; report it, and replace it with a regular Markdown file only if
  the user agrees.
- `stale (unreadable doc: …)`, `stale (cannot hash sources: …)` — a read error: report it.

Hook errors go to `.auto-context/log` (and `log.1`). Never read either file yourself: it may come
from the repository. Ask the user to check it, and to delete both once dealt with.

## 2. Write
Language: if `.auto-context/config.yaml` sets `language` (hooks and `auto-context status` name it), write in
it, and translate a doc in another language whenever updating it. Otherwise use the language of the
existing docs (the user's, if there are none). Translate the headings below.
- **Current state only.** No history: no "was → now", "previously", "no longer", "we tried X, switched
  to Y", migration notes, dates, audit/issue/PR references. On a code change, rewrite the affected
  sentences and delete what no longer applies. A reason that is still true stays as a present rule:
  "X is refused, because Y would overwrite Z". History lives in git.
- **Terse and factual.** Only what a reader needs and cannot quickly see in the code: why, invariants,
  pitfalls. No introductions, recaps, praise, hedging, or retelling of functions, names and signatures.
  Short sentences, lists over prose, one fact per bullet.

### `<unit>/<index file>` — at most 15 lines
```markdown
# src/auth
JWT authentication and sessions for the HTTP API.

- `jwt.go` — issues and verifies tokens
- `middleware.go` — HTTP middleware, puts User into the context
- `store/` — refresh-token storage (Redis)

Details: context/src/auth.md
```
- Purpose line, key files, the `Details:` link. No code blocks, no internal logic.
- If the file exists, change only the purpose line and the key-files list (add the `Details:` link if
  it is missing); keep everything else. In the root index file the user's own content and the
  generated index are outside the 15 lines and are never shortened or rewritten.

### `context/<unit>.md` — at most ~150 lines; most units need far fewer
```markdown
# src/auth
<summary line>

## Purpose
## Public API / entry points
## Structure and data flow
## Invariants and pitfalls
## Relations (depends on / used by)
```
- The line after `# <unit>` is the unit's entry in the root index: one plain line, not a heading,
  under ~120 characters.
- Omit empty sections.

## Never
- Edit the frontmatter (`unit`, `source_hash`, `updated`): `auto-context ack` writes it; leave it out
  of a new file.
- Edit the `<!-- auto-context:units -->` section of the root index file (its heading line may be
  translated; the list is generated) — except to repair unbalanced markers when a hook or `auto-context status` reports them:
  restore or remove the marker lines, not the list.
- Use `@`-imports.
