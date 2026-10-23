# hooks/

**English** · [Русский](README.ru.md)

Two hook registration files: `hooks.json` (Claude Code) and `codex.json` (Codex; `.codex-plugin/plugin.json` points to it, commands carry `--agent codex`). Each hook calls `"${CLAUDE_PLUGIN_ROOT}"/bin/auto-context hook <event>`; the logic lives in `internal/hooks`.

Claude Code (`hooks.json`):

| Event | Subcommand | Timeout | What it does |
|---|---|---|---|
| `SessionStart` | `session-start` | 60 s | doc statuses, the index in the root `CLAUDE.md`, a notice about stale/missing docs and problems |
| `UserPromptSubmit` | `prompt-submit` | 30 s | snapshot of files at the start of the turn |
| `Stop` | `stop` | 60 s | comparison with the snapshot, auto-ack, a reminder to update the docs |
| `PreToolUse` (`Bash`, `if: Bash(auto-context *)`) | `pre-tool-use` | 10 s | auto-approval of safe `auto-context` calls |
| `SessionEnd` | `session-end` | 10 s | deletes the session's snapshot and log |

Codex (`codex.json`):

| Event | Subcommand | Timeout | What it does |
|---|---|---|---|
| `SessionStart` | `session-start` | 60 s | same as Claude Code |
| `UserPromptSubmit` | `prompt-submit` | 30 s | snapshot of files at the start of the turn |
| `Stop` | `stop` | 60 s | same as Claude Code; the reminder is `{"decision":"block","reason":…}` |
| `PermissionRequest` (`matcher: ^Bash$`) | `permission-request` | 10 s | auto-approval of the same CLI calls as `pre-tool-use`, only by the plugin's absolute path (never the bare `auto-context`) |
| `SessionEnd` | `session-end` | 3 s | deletes the snapshot and log (git budget 2 s); Codex does not call it when you quit the TUI |

- A hook never blocks the session: the shim always exits 0, errors go to `.auto-context/log`.
- `TestHooksJSONContract` (`hooks.json`) and `TestCodexHooksManifest` (`codex.json`) check events, subcommands, timeouts and the git budget against `hookTable`/`budgetFor` in `cmd/auto-context`: add a new event to both files and to the code.
- `if` on `PreToolUse` is a Claude Code filter so the binary does not start on every Bash call; the binary itself makes the decision.
- Codex has no `if`: the filter is `matcher: ^Bash$`, and the binary makes the decision.
