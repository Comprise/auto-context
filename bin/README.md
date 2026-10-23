# bin/

**English** · [Русский](README.ru.md)

`auto-context` is a POSIX sh shim. Claude Code adds the plugin's `bin/` to PATH for Bash, so skills call `auto-context` by name.

- Picks the binary `../libexec/auto-context-<os>-<arch>[.exe]` by `uname` (linux, darwin, windows via Git Bash/MSYS; amd64, arm64). The binaries are built by `make dist` or the release.
- For `hook …` it always exits 0: a binary crash (a Go fatal error exits with 2, which Claude Code treats as a block) is recorded through `hook-exit` in `.auto-context/log`. If SessionStart crashes in an enabled project, the shim prints a fixed JSON with a `systemMessage` instead of the binary's partial output.
- The shim determines an "enabled project" like `project.FindRoot` (without the owner check): from `CLAUDE_PROJECT_DIR` (or `.`) upward to a directory with `.git` or `.auto-context/config.yaml`, no higher than `$HOME`; the project is enabled if `.auto-context/config.yaml` or `context/_root.md` is there.
- The shim does not read stdin (the binary does), so it checks whether the project is enabled from `CLAUDE_PROJECT_DIR`, or without it from the process's working directory (in Codex, the session's cwd), not from the `cwd` in the JSON. Its notice about a missing or crashed binary is best effort.
- No binary for the platform: for `hook session-start` in an enabled project, a JSON with a `systemMessage`; otherwise silence for hooks; for the CLI, an error and exit code 1.
- The shim's own utilities (`uname`, `tr`, `dirname`, `sed`) run with the original `PATH` minus empty and relative entries (so a project directory in `PATH` cannot supply them), or `/usr/bin:/bin` if nothing is left. The binary itself gets the original `PATH` back, so `git` outside `/usr/bin` (Homebrew, `/usr/local/bin`) is found.
- The "enabled project" check compares directories after `pwd -P` (canonical paths), including `$HOME`, so a symlinked `$HOME` or project path does not change where the search stops.
- The behavior is covered by `cmd/auto-context/shim_test.go`.
