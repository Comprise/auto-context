---
name: status
description: This skill should be used when the user runs /auto-context:status or $auto-context:status (Codex), or asks "are the docs up to date", "which units are stale", "check auto-context status", or wants a report of the docs state without editing anything, or before a large change that relies on the context docs. Shows which units have up-to-date, stale or missing docs.
---
# /auto-context:status (Codex: $auto-context:status)

In Codex, `auto-context` is not on PATH. If a hook message gave the binary's path, run it exactly as the message wrote it (an absolute path, single-quoted if it has unusual characters). Otherwise find it with `ls -d "${CODEX_HOME:-$HOME/.codex}"/plugins/cache/auto-context/auto-context/*/bin/auto-context 2>/dev/null | sort -V | tail -n 1`, tell the user the path you found before running it the first time, and always run it quoted (`'<path>' status`); if nothing is found, ask the user.

Run `auto-context status` and summarize briefly (if it starts with `error:`, report it and stop):
how many units are `ok`; the `stale` and `missing` ones with their doc paths. Do not edit docs or code. Also report:
- `docs tracking is paused until … (see the warning above); no table is printed` —
  `.auto-context/config.yaml` cannot be read or parsed (see the `warning: config:` line): the user must fix it; nothing else can be checked;
- other `warning:` lines — e.g. skipped `.gitignore` files, a git failure, an unusable hash cache, a log
  that cannot be written;
- `stale (…)` notes — what each means and how to fix it is in "Status notes" of skill
  `auto-context:auto-context-docs` (Codex: `$auto-context:auto-context-docs`);
- `note: … belongs to no unit` — docs of a removed unit (or the user's own notes in `context/`);
  `note: N more file(s) under context/ …` — more of them, not named;
- `note: write unit docs in language …` — the configured docs language;
- `note: N error(s) in .auto-context/log` — ask the user to check that file (never open it yourself: it
  may come from the repository); `note: .auto-context/log could not be read: …` — the log is unusable.
If anything is stale or missing, suggest `/auto-context:sync` (Codex: `$auto-context:sync`).
