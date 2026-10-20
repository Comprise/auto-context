# bin/

**English** · [Русский](README.ru.md)

`auto-context` is a POSIX sh shim. Claude Code adds the plugin's `bin/` to PATH for Bash, so skills call `auto-context` by name.

- Picks the binary `../libexec/auto-context-<os>-<arch>[.exe]` by `uname` (linux, darwin, windows via Git Bash/MSYS; amd64, arm64). The binaries are built by `make dist` or the release.
- For `hook …` it always exits 0: a binary crash (a Go fatal error exits with 2, which Claude Code treats as a block) is recorded through `hook-exit` in `.auto-context/log`. If SessionStart crashes in an enabled project, the shim prints a fixed JSON with a `systemMessage` instead of the binary's partial output.
- The shim determines an "enabled project" like `config.FindRoot` (without the owner check): from `CLAUDE_PROJECT_DIR` (or `.`) upward to a directory with `.git` or `.auto-context/config.yaml`, no higher than `$HOME`; the project is enabled if `.auto-context/config.yaml` or `context/_root.md` is there.
- The shim does not read stdin (the binary does), so it checks whether the project is enabled from `CLAUDE_PROJECT_DIR`, or without it from the process's working directory (in Codex, the session's cwd), not from the `cwd` in the JSON. Its notice about a missing or crashed binary is best effort.
- No binary for the platform: for `hook session-start` in an enabled project, a JSON with a `systemMessage`; otherwise silence for hooks; for the CLI, an error and exit code 1.
- The behavior is covered by `cmd/auto-context/shim_test.go`.
