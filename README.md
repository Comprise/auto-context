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
- The unit table of contents in the root index file is kept between the lines `<!-- auto-context:units -->` and
  `<!-- /auto-context:units -->`. You may translate or replace the heading line right after the opening marker; the
  list itself is rewritten by the plugin. If the markers are broken (one missing, duplicated or out of order), the table
  of contents is not updated and `auto-context status` warns about it: fix the markers by hand.

When the docs are up to date, the plugin stays silent and spends no context (the only exception is a one-line
note about the docs language, shown when `language` is set).

## Requirements

- Claude Code 2.1.304 or later (CLI, IDE extension or desktop app; older versions were not tested), or Codex CLI.
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

To update, see [Updating](#updating) (auto-update for third-party marketplaces is off by default).

## Updating

- **Claude Code:** `claude plugin update auto-context@auto-context`, or enable auto-update in `/plugin` → Marketplaces.
  Restart Claude Code afterwards; the new version is used by the next session.
- **Codex:** refresh the `auto-context` marketplace and update the plugin from `/plugins` (the exact CLI command for
  refreshing a marketplace depends on your Codex version; check `codex plugin marketplace --help`), then trust the
  plugin's hooks again in `/hooks` and start a new session: Codex asks for trust again after an update, and
  untrusted hooks do not run.
- A project at one version works with one binary at a time, so do not mix versions in one repository: the doc
  format on disk changes between releases (see `CHANGELOG.md`). A project written by v1.0.0 needs a one-time
  `auto-context ack --upgrade` after the update (see [CI](#ci)).
- **Upgrade every machine together.** Plugin v1.0.0 does not understand `v2:` hashes and rewrites them back to
  `sha256:` on its own auto-ack, so one teammate still on v1.0.0 undoes the upgrade for everyone.
- **Pinning a version.** Every release is also tagged `dist/vX.Y.Z` on a commit of the release history. For a stable
  version the `release` branch moved to, that commit is on `release`; a pre-release's tag is on the `release-next`
  line, and a stable version released below the branch's current one (a fix for an older line) gets a commit off
  the branch. For Codex:
  `codex plugin marketplace add Comprise/auto-context --ref dist/vX.Y.Z`. Whether Claude Code's
  `/plugin marketplace add` accepts a ref for a GitHub source depends on your Claude Code version; check `/plugin`
  help, this has not been verified here. The `dist/` tags appear with the first release that publishes them. Pre-release tags move the `release-next`
  branch instead, which testers can install with `--ref release-next`.

## Uninstalling and disabling

- **Remove the plugin:** in Claude Code, `/plugin uninstall auto-context@auto-context`; in Codex, uninstall it in
  `/plugins` and remove the marketplace with `codex plugin marketplace remove auto-context`. These commands were not
  verified here; check `/plugin` help and `codex plugin marketplace --help` if they differ.
- **Disable it in one project only:** hooks work while `.auto-context/config.yaml` or `context/_root.md` exists in the
  project. Delete both (and the `.auto-context/` directory) and the plugin stays silent and writes nothing there.
  The generated docs (`CLAUDE.md`, `context/`) stay as ordinary files; remove the section between
  `<!-- auto-context:units -->` and `<!-- /auto-context:units -->` from the root index file if you do not want it.

## Verify

Binaries are built by the release workflow, and their build provenance is attested. To check an installed binary
(the path depends on where your agent keeps plugins; for Claude Code it is usually under
`~/.claude/plugins/cache/`):

```sh
gh attestation verify <plugin dir>/libexec/auto-context-linux-amd64 -R Comprise/auto-context
(cd <plugin dir> && sha256sum -c checksums.txt)
```

The `checksums.txt` in the release tree lists the binaries as `libexec/...` paths, so it is checked from the plugin
directory as above. The GitHub Release also carries the archive `auto-context-vX.Y.Z.tar.gz` and its own
`checksums.txt` with flat file names; check that one in the directory where you downloaded the files. The archive and
both checksum files are attested too. They appear starting with the first release built by the updated workflow; older
releases do not have them. On macOS use `shasum -a 256 -c checksums.txt`.

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
(`${CODEX_HOME:-~/.codex}/plugins/cache/<marketplace>/auto-context/<version>/bin/auto-context ack …`; a path with spaces
or other unusual characters is in single quotes), and the skills run them the same way; if a hook did not
supply a path, the skill looks only under the `auto-context` marketplace of the Codex plugin cache
(`${CODEX_HOME:-~/.codex}/plugins/cache/auto-context/auto-context/<version>/bin/auto-context`, highest version), shows you
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
Commit the generated `CLAUDE.md`, `context/`, `.auto-context/config.yaml` and `.auto-context/.gitignore`
(the plugin creates it to keep its logs and caches out of git) so your colleagues get the docs too.

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
| `auto-context status --json` | the same as JSON; combines with `--stale` and `--exit-code` |
| `auto-context units` | list of units |
| `auto-context units --json` | the same as JSON |
| `auto-context ack <unit…>` | the change is minor: treat the docs as up to date |
| `auto-context ack .` | the same for the project root (`_root`, `context/_root.md`) |
| `auto-context ack --upgrade` | rewrite docs that are current under the old hash scheme to scheme v2 |
| `auto-context ack --exclude <unit>` | do not treat the directory as a separate unit (`_root` cannot be excluded) |
| `auto-context changes <unit>` | what changed in the unit since the docs were last updated |
| `auto-context version` | the plugin version |
| `auto-context version -v` | also the revision, Go version, platform and on-disk format versions (for bug reports) |

**Exit codes.** 0: success, or everything is up to date; 1: only `status --exit-code` when something is
stale or missing; 2: wrong usage; 3: an error (the project cannot be opened, the config is broken, `ack` refused,
or the project is not enabled and `status --exit-code` was asked). In CI, 1 means stale docs and 3 means a
broken setup: tell them apart.

**Output.** `status` prints a table with the columns `UNIT STATE DOC`; a state can carry a note in parentheses, for
example `stale (…)`, and lines starting with `warning:` or `note:` follow it, all on stdout. With `--json`
(`format_version` 1) the document is `{"format_version":1,"paused":…,"units":[{"unit","state","doc","note","detail"}],"orphans":{"shown":[…],"hidden":N},"warnings":[…],"notes":[…]}`.
`units` prints one tab-separated line per unit, `path`, `reason` (`root`, `include`, `module` or `files`) and the
number of files; with `--json` it is `{"format_version":1,"units":[{"path","reason","files"}]}`. Other commands
report errors on stderr.

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
- `include`, `exclude` and `track` are glob patterns relative to the repository root (`doublestar` syntax, not plain
  directory names): up to 256 bytes and 8 `{}` groups each. `min_files` is at least 1, `max_session_notice` is
  0..100. A list with no valid items falls back to the default; an explicit `[]` stays empty.
- Files ignored by git (`.gitignore`) are not counted when units are found, but a file that is already committed
  counts even if it matches `.gitignore`. Untracked, not ignored files count towards `min_files` (see [CI](#ci) for
  the effect on hashes).
- An invalid setting is dropped with a warning, and the others stay in effect. These break the whole config
  instead (doc tracking is suspended until it is fixed): a YAML syntax error; an unreadable file, a file larger
  than 1 MiB, or one that is not a regular file; a repeated or non-string `index_file`; an `index_file` other than
  `CLAUDE.md` and `AGENTS.md`. `ack`, `units` and `changes` refuse to run while the config is broken.

## CI

`auto-context status --exit-code` can gate a pull request. Since hash scheme v2, `source_hash` is computed only from
the files git tracks (the index), so untracked files, `.git/info/exclude` and a global `core.excludesFile` do not
change it: the hash in your working copy equals the one in a clean clone. Files above 32 MiB are hashed by
size and their first and last MiB.

- Run `auto-context ack` (or update the docs) and commit the docs together with the code; CI then sees `ok`.
- Run the check in a clean checkout of the commit, with `git` installed and the full tree checked out.
- Exit code 1 means stale or missing docs, 3 means a setup error (see [Commands](#commands)).
- Docs written by v1.0.0 carry `sha256:` hashes. They show as `ok` with the note `old hash scheme` as long as nothing
  changed; run `auto-context ack --upgrade` once and commit the result. Units whose old hash cannot be reproduced
  (for example a unit with a file above 32 MiB) become `stale` and need a normal `ack`.

## Troubleshooting

- `auto-context status` shows warnings: about the config, `.gitignore`, git.
- `/hooks` should list the plugin's hooks: `SessionStart`, `UserPromptSubmit`, `Stop`, `PreToolUse`,
  `SessionEnd` (in Codex, `PermissionRequest` instead of `PreToolUse`, and the hooks must be trusted).
- In Codex the `SessionEnd` hook does not fire when you quit the TUI (a background app-server keeps the session alive), so session files in `.auto-context/state` remain; SessionStart deletes those older than 7 days.
- Two agent sessions in the same directory see each other's edits: a reminder or auto-ack may reach the wrong session. For parallel work, use separate `git worktree`s.
- Plugin errors are written to `.auto-context/log`. The plugin never interrupts the agent's work.
- A repository with hundreds of thousands of files can exceed the hook time budget. The plugin refuses to list
  more than 800000 files; add large generated directories to `.gitignore`, and for a big working tree run
  `git config core.untrackedCache true` to speed up the plugin's file listing (`git ls-files`). The plugin always
  turns `core.fsmonitor` off for its own git calls.
- If every hook reports a git timeout, check that `.gitignore` and `.git/info/exclude` are regular files: a FIFO or
  device in their place makes git block on reading it. The plugin does not fall back to walking the tree in that case.
- To report a bug, include the output of `auto-context version -v`, `auto-context status` and the tail of
  `.auto-context/log`.

## Security

The plugin assumes the repository may be untrusted. What it guarantees:

- The plugin's own file reads and writes stay inside the repository: symlinks that lead outside it, into `.git/` or
  into the state directory are refused, a symlinked doc must point at another `.md` file, and files that are not
  regular files are not read.
- Nothing in `.auto-context/config.yaml` is executed.
- Text of the config (except the validated `language` code), of the log and of git's stderr never reaches the agent or the terminal: messages carry only
  unit names that passed a safety check, numbers and fixed categories. Full error text goes to `.auto-context/log`
  only.
- git runs with protective settings: no pager, no fsmonitor, no prompts, no network transports, no lazy fetching,
  no system config. The file listing (`git ls-files`) and `auto-context changes` run only after checking that the
  working tree is the one the repository claims; the `check-ignore` and `rev-parse` calls run without that check.

What it does not guarantee, and what you should treat as untrusted data:

- The docs themselves, `auto-context changes` output (commit subjects, file names, `git status` lines) and the names
  of unit directories are text from the repository and reach the agent. Control characters are stripped, but the
  content is not vetted: it can contain instructions meant for the model.
- git itself reads the repository's `.git/config` and a few files outside it (`core.excludesFile`, `include.path`,
  alternates). Do not open a repository received as an archive together with its `.git/` directory with the plugin
  enabled unless you trust it.
- Report vulnerabilities through a private GitHub security advisory (see `SECURITY.md`).

## Development

```sh
make test                                   # go vet + go test -race (needs Go 1.27, git, a C compiler; on Linux also jq)
make dist                                   # binaries for 6 platforms into libexec/
claude --plugin-dir /path/to/auto-context   # run Claude Code with the local version (after make dist)
```

`claude --plugin-dir` works only after `make dist`: the shim looks for binaries in `libexec/`, which is not in `main`.
To try the plugin in Codex locally, add the checkout as a marketplace with `codex plugin marketplace add <path>`
after `make dist`.

A release is a `vX.Y.Z` tag on a commit from `main` and only moves the `release` branch to a higher version: the workflow builds
the binaries and updates the `release` branch, from which the plugin is installed. Pre-release tags (`vX.Y.Z-pre`) do
not touch `release`. The version is taken from the tag. The code structure is described in
[`context/_root.md`](context/_root.md) (in Russian).

## License

[MIT](LICENSE)
