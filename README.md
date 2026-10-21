# auto-context

**English** · [Русский](README.ru.md)

**A plugin for [Claude Code](https://docs.claude.com/en/docs/claude-code) and
[Codex CLI](https://developers.openai.com/codex) that keeps a project's documentation in sync with
its code.**

An agent works better when it knows how a project is put together, but reading all the code for every
task is expensive, and docs written once go stale quickly. auto-context spreads short descriptions
across the parts of a project and makes sure they keep up with the code.

## How it works

A project is split into **units**: directories that hold a module (`go.mod`, `package.json`, …) or
enough code files. Each unit has two documents:

```
repo/
├── CLAUDE.md                 # your rules + the unit index (maintained by the plugin)
├── services/billing/
│   └── CLAUDE.md             # the unit's purpose and key files, briefly
└── context/
    ├── _root.md              # the project's overall structure
    └── services/billing.md   # details: API, invariants, relations
```

- `<unit>/CLAUDE.md` is loaded by Claude Code itself (in Codex the index files are called `AGENTS.md`,
  see [Codex](#codex)) when it works with the unit's files.
- `context/<unit>.md` is read by Claude only when it needs the details.
- If Claude changed a unit's code but left the docs as they were, the plugin reminds it at the end of
  the turn to update them or to confirm that the change is minor.
- Changes made between sessions (`git pull`, manual edits) are reported by the plugin at session start.

When the docs are up to date, the plugin stays silent and spends no context.

## Requirements

- Claude Code (CLI, IDE extension or desktop app) or Codex CLI.
- Linux, macOS or Windows (amd64 or arm64). Go is not required: the plugin is installed with prebuilt binaries.
- `git` is recommended but not required.
- **Windows:** [Git for Windows](https://git-scm.com/downloads/win) is required; without Git Bash the
  plugin's hooks do not run. If Git Bash is not in the default location, set its path in `settings.json`:

  ```json
  { "env": { "CLAUDE_CODE_GIT_BASH_PATH": "C:\\Program Files\\Git\\bin\\bash.exe" } }
  ```

- **Codex:** Linux and macOS only (on Windows, inside WSL). Codex hooks are POSIX sh commands; how Codex runs them on Windows without WSL has not been verified.

## Installation

In Claude Code:

```
/plugin marketplace add Comprise/auto-context
/plugin install auto-context@auto-context
```

If you have no SSH key for GitHub, set `CLAUDE_CODE_PLUGIN_PREFER_HTTPS=1` first.

To update (auto-update for third-party marketplaces is off by default; turn it on in
`/plugin` → Marketplaces):

```
claude plugin update auto-context@auto-context
```

## Codex

Codex is supported on Linux and macOS (on Windows, through WSL).

The hooks and skills are the same, but unit index files are called `AGENTS.md` and skills are invoked with `$`.

```
codex plugin marketplace add Comprise/auto-context --ref release
```

Then, in Codex:

1. `/plugins` → install `auto-context`.
2. `/hooks` → trust the plugin's hooks. After the plugin is updated, Codex may ask for trust again. Hooks only work in a session started after they are trusted.
3. Start a new session and run `$auto-context:init` (not Codex's built-in `/init`). The skill itself writes
   `index_file: AGENTS.md` to `.auto-context/config.yaml` before creating any docs.

The other skills are `$auto-context:sync` and `$auto-context:status`. End-of-turn reminders arrive as a regular continuation
of the turn.

In Codex, `auto-context` is not on PATH: the hooks suggest commands with an absolute path
(`~/.codex/plugins/cache/<marketplace>/auto-context/<version>/bin/auto-context ack …`; a path with spaces
or other unusual characters is in single quotes), and the skills run them the same way; if a hook did not
supply a path, the skill looks only under the `auto-context` marketplace of the Codex plugin cache
(`~/.codex/plugins/cache/auto-context/auto-context/<version>/bin/auto-context`, highest version), shows you
the path before the first run and always runs it quoted; if it finds none, it asks you. The plugin binary at an absolute path usually runs without a prompt; if
Codex is about to ask anyway, the `PermissionRequest` hook approves the plugin's CLI calls by that absolute path only (`ack`,
`status`, `units`, `changes`, `version`; `ack --exclude` always asks).

The Codex marketplace is described in `.agents/plugins/marketplace.json` on the `release` branch
(source `local`, `./`); the plugin identifier is `auto-context@auto-context`.

## Quick start

The plugin is installed globally but only works in projects where it has been enabled. Open a project in
Claude Code and run (in Codex, `$auto-context:init`, see [Codex](#codex)):

```
/auto-context:init
```

Claude finds the units, shows the list for confirmation, asks for the docs language and writes the docs.
Commit the generated `CLAUDE.md`, `context/` and `.auto-context/config.yaml` so your colleagues
get the docs too.

## Usage

**Work as usual.** Ask Claude to change code; you do not need to think about the docs.

**If Claude changed code but not the docs,** the plugin reminds it at the end of the reply, and Claude
decides for itself: update `context/<unit>.md` (if the purpose, API, structure or important behavior
changed) or confirm that the change is minor (`auto-context ack`). If the docs are updated in the same
turn, the plugin acknowledges them itself. The reminder comes at most once per unit per turn.

**After `git pull`, a branch switch or edits made outside Claude,** the plugin reports at session start which
docs are stale. To update them right away:

```
/auto-context:sync
```

**A new module or a directory with code appears:** the plugin notices it, and Claude asks whether to
document it or not to treat it as a separate unit. **A directory is deleted:** the plugin reports its docs as orphaned,
and Claude deletes them only after asking you.

**To check whether everything is up to date,** run `/auto-context:status`.

**To keep the docs in a single language,** set `language` in the settings (`/auto-context:init`
asks for it itself).

## Commands

| Command (Claude Code / Codex) | What it does |
|---|---|
| `/auto-context:init` / `$auto-context:init` | enable the plugin in the project and write the docs |
| `/auto-context:sync` / `$auto-context:sync` | find stale docs and update them |
| `/auto-context:status` / `$auto-context:status` | show which docs are stale, changing nothing |

Claude also uses the command-line tool, which you can run by hand:

| Command | What it does |
|---|---|
| `auto-context status` | state of unit docs: `ok`, `stale`, `missing` |
| `auto-context status --stale` | only stale and missing |
| `auto-context status --exit-code` | exit code 1 if anything is stale/missing, for CI |
| `auto-context units` | list of units |
| `auto-context ack <unit…>` | the change is minor: treat the docs as up to date |
| `auto-context ack --exclude <unit>` | do not treat the directory as a separate unit |
| `auto-context changes <unit>` | what changed in the unit since the docs were last updated |

## Configuration

Settings are optional and live in `.auto-context/config.yaml`. Defaults:

```yaml
min_files: 5            # how many code files make a directory a unit
include: []             # directories that are always units
exclude: [vendor/**, node_modules/**, "**/testdata/**", dist/**, build/**]
track: []               # files without a code extension that also count as unit code
index_file: CLAUDE.md   # unit index file name: CLAUDE.md or AGENTS.md (Codex)
language: ""            # docs language: ru, en, pt-BR…; empty means as in the existing docs
max_session_notice: 10  # how many units to list in the session-start notice (0: counts only)
code_extensions: [.go, .ts, .tsx, .mts, .cts, .js, .jsx, .mjs, .cjs, .vue, .svelte, .py, .rs, .java, .kt,
                  .scala, .swift, .dart, .rb, .php, .cs, .fs, .c, .h, .cc, .cpp, .cxx, .hh, .hpp, .m, .mm,
                  .ex, .exs, .erl, .hs, .clj, .lua, .zig, .sh, .sql, .proto, .graphql, .tf]
module_markers: [go.mod, package.json, Cargo.toml, pyproject.toml, pom.xml, build.gradle]
```

- A list you set replaces the default value; it does not extend it.
- To stop a directory from being a separate unit, add it to `exclude` (or run `auto-context ack --exclude`);
  to make the plugin ignore it entirely, use `<dir>/**`.
- Files ignored by git (`.gitignore`) are not taken into account by the plugin.
- An invalid setting is dropped with a warning, and the others stay in effect. The exception is
  `index_file`: any value other than `CLAUDE.md` and `AGENTS.md` breaks the whole config (doc
  tracking is suspended until the value is fixed).

## Troubleshooting

- `auto-context status` shows warnings: about the config, `.gitignore`, git.
- `/hooks` should list the plugin's hooks: `SessionStart`, `UserPromptSubmit`, `Stop`, `PreToolUse`,
  `SessionEnd` (in Codex, `PermissionRequest` instead of `PreToolUse`, and the hooks must be trusted).
- In Codex the `SessionEnd` hook does not fire when you quit the TUI (a background app-server keeps the session alive), so session files in `.auto-context/state` remain; SessionStart deletes those older than 7 days.
- Two agent sessions in the same directory see each other's edits: a reminder or auto-ack may reach the wrong session. For parallel work, use separate `git worktree`s.
- Plugin errors are written to `.auto-context/log`. The plugin never interrupts the agent's work.
- The plugin may not be able to process a very large repository (millions of files); add large
  generated directories to `.gitignore`.

## Security

The plugin assumes the repository may be untrusted: it neither reads nor writes files outside the
repository, does not run commands from its configuration, and passes no text from the repository to
Claude other than the docs themselves.

## Development

```sh
make test                                   # tests
make dist                                   # binaries for 6 platforms
claude --plugin-dir /path/to/auto-context   # run Claude Code with the local version
```

A release is a `vX.Y.Z` tag on a commit from `main`: the workflow builds the binaries and updates the `release` branch,
from which the plugin is installed. The code structure is described in [`context/_root.md`](context/_root.md) (in Russian).

## License

[MIT](LICENSE)
