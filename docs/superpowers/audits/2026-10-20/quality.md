# Audit: code quality and docs accuracy (auto-context)

Tooling: `go vet ./...` clean, `gofmt -l .` clean, `go test ./...` passes. staticcheck is not installed (skipped). No TODO/FIXME/HACK left in Go code. Security excluded.
Line numbers are per file (verified against the source).

## Docs accuracy

### 1. [Medium] context/internal/textx.md:20 and context/internal/units.md:38 - wrong dependency edges
- textx.md says it is used by `internal/hooks` and `internal/units`. Neither imports `internal/textx` (non-test code). Real users: `cmd/auto-context`, `logx`, `project`, `rootindex`.
- units.md "Зависит от" lists `internal/textx`. Real imports of `internal/units`: `config`, `fsutil`, `gitx`.
- Fix: drop `internal/hooks` and `internal/units` from textx.md; drop `internal/textx` from units.md.

### 2. [Medium] hooks/README.md:1-15 - describes only the Claude `hooks.json`
- Codex registration is missing: `hooks/codex.json` (`--agent codex`, `PermissionRequest` matcher `^Bash$`, `SessionEnd` timeout 3 s).
- The contract-test sentence mentions only `hooks.json`, but `TestHooksJSONContract` also covers `codex.json`.
- Fix: add a Codex table (events, timeouts 60/30/60/10/3) and mention the Codex file.

### 3. [Medium] docs/superpowers/specs/2026-10-19-auto-context-design.md:259, :245, :176, :273-281, :383 - stale details that contradict the code
The header (line 4) says "при расхождении верен код", so it is non-authoritative, but a reader can still be misled.
- :259 the allow regex is `(ack|status|units|version)` and says `ack --exclude <unit>` is approved. Code (`internal/hooks/pre_tool_use.go:14,63-69`) also allows `changes` and always refuses `--exclude`.
- :245 step 6 says a clean Stop clears the journal and deletes the snapshot. Code (`internal/hooks/stop.go:111-114`) keeps both on purpose.
- :176 names `hooks.Active`; the function is `project.Active`.
- :273-281 the CLI table lacks `changes`, `version`, `status --exit-code`, `hook session-end`, `hook-exit`.
- :383 the default `code_extensions` list is far shorter than `config.Default()`.
- Fix: mark these sections as historical, or sync them. Codex spec (2026-10-20-codex-support-design.md:4) still says "Статус: проект" although Codex support is shipped.

### 4. [Low] .auto-context/config.yaml `track:` - Codex packaging not tracked
- `track` has `.claude-plugin/*`, `hooks/*`, `bin/*`, `skills/**`, etc. It lacks `.codex-plugin/*` and `.agents/**`.
- context/_root.md:35-37 documents both as part of the `_root` unit.
- Result: edits to them never make `_root` stale.
- Fix: add `.codex-plugin/*` and `.agents/plugins/*` to `track`.

### 5. [Low] context/cmd/auto-context.md:11 and cmd/auto-context/main.go:110-118,72-85 - "permission-request - только для Codex" is not enforced
- `hook permission-request` without `--agent` runs with agent Claude and answers `allow`.
- Usage text says `--agent codex`, docs say Codex only, code accepts both.
- Fix: reject the combination (log and exit 0), or reword the docs.

### 6. [Low] README.md:571 vs cmd/auto-context/main.go:36 - README command table omits `status` flags
- Usage lists `status [--stale] [--exit-code]`; the README table has only `auto-context status`.
- Fix: add `--stale` and `--exit-code` to the table.

### 7. [Low] internal/docstate/docstate.go:43 - misleading note text
- `NoteOutside = "symlink outside repo"` is printed by `status`, but the doc says (`OutsideText`) it also covers links into `.git`, to a non-.md file, or dangling.
- The text is mirrored in skills/auto-context-docs/SKILL.md ("Status notes"), so changing it needs both places.

## Code quality

### 8. [Medium] internal/hooks/stop.go:29-130 - `Stop` is a 102-line function with nested switches
- Mixes activation, state-dir detection, the "no snapshot" recovery (lines 51-73), journal handling, parent/_root expansion (lines 99-107), cache save and feedback.
- `pendingUnits` (line 240) takes 7 parameters, two of them funcs.
- Fix: extract the no-snapshot branch (`stopWithoutSnapshot`) and the parent-expansion loop (`withParents`). Group the `pendingUnits` params in a struct.

### 9. [Medium] internal/units/walk.go (808 lines) - three concerns in one file
- It holds the .gitignore matcher (`patternSet`, `ignoreRule`, `parseIgnore`, `longestLiteral`, `ignoreFile.match`, `loadGitignore`, lines 46-373), discovery (`Discover`, `addFile`, `walk`, `parallelLstat`) and git listing (`gitFiles`).
- Fix: split into `ignore.go`, `walk.go`, `gitfiles.go`. The unit CLAUDE.md (internal/units/CLAUDE.md) lists only two files and should follow.

### 10. [Low] internal/units/walk.go:526-532 and :593-600 - duplicated "is this file interesting" classification
- `interesting` re-implements the `addFile` classification. Its `slices.Contains(ModuleMarkers, name)` clause is redundant, because `isSource` (line 31) already accepts markers.
- Two copies can drift; the comment says "see addFile".
- Fix: have `addFile` and `interesting` share one helper returning (ctxDoc, isDoc, isMarker, isSrc).
- Also, walk.go:60 `literal` closure re-declares the same character set as the `globMeta` constant (line 164); reuse it.

### 11. [Low] internal/hooks/stop.go:181-198 - `configPaused` has two identical save branches
- Both the `!first` and `first` paths save the journal and log the same error.
- Fix: one `j.Save` first, then `if !first || saveErr != nil { return nil, nil }`.

### 12. [Low] internal/hooks/stop.go:354-369 - duplicated refused/unreadable blocks in `StopReason`
- The two loops building `ctxs` are identical; extract `contextPaths(list)`.
- The `capList`+`more` pattern repeats five times (lines 341, 347, 354, 362, 370).

### 13. [Low] Modern Go idioms (toolchain go1.27)
- `strings.Split` ranged over -> `strings.SplitSeq`:
  - internal/hooks/pre_tool_use.go:64 (the hinted one)
  - internal/logx/logx.go:133 (up to 4 MiB log, avoids the slice)
  - internal/fsutil/fsutil.go:100 and :308
  - internal/frontmatter/frontmatter.go:114 (breaks early)
  - internal/project/changes.go:143
  - internal/units/walk.go:361 (`parseIgnore` takes `[]string`; change it to take a `iter.Seq` or keep the slice)
- `sort.Strings` -> `slices.Sort`: internal/hashing/hashing.go:137,147.
- internal/session/session.go:116-125 hand-rolled field split could use `bytes.Cut` twice.

### 14. [Low] Identifier shadowing
- Builtin `max`:
  - internal/fsutil/fsutil.go:192,201,210
  - internal/textx/textx.go:22,31 (params named `max`)
  - internal/hooks/session_start.go:251
- Package `sync` shadowed by a bool parameter: internal/fsutil/fsutil.go:268 (`writeAtomic(..., sync bool)`; the package is imported in the same file).
- Package `units` shadowed by variadic params: internal/session/session.go:219,244-246 (`NotifyAckFailed`, `AddCode`, `AddDocs`, `Notify`).
- Rename to `limit`, `doSync`, `us`.

### 15. [Low] Dead or test-only API and fields
- internal/hooks/hooks.go:16-19: `Input.HookEventName`, `Input.Source` and `Input.StopHookActive` are never read in non-test code.
- internal/hashing/hashing.go:88: `Cache.Len` is used only by tests.
- `Input.Command` (hooks.go:29-37) unmarshals into `any` then type-asserts. Unmarshalling into `string` gives the same "" on non-string input.

### 16. [Low] Redundant logic and inconsistent patterns
- internal/config/config.go:122-124 and :128-130 run the same `indexFileKeys(data) > 1` check twice; the second covers both paths.
- internal/config/config.go:338 `a || b && c` without parentheses.
- internal/session/session.go:99-103 the `ErrNotExist` branch only re-wraps the same error; line 272 uses `os.IsNotExist` where the rest of the code uses `errors.Is`.
- internal/project/changes.go:75-83 `switch` with `case !ok` / `default` -> `if/else`.
- internal/project/project.go:197 and :214 `Prewarm` and `Statuses` repeat the same prewarm+save closure; have `Statuses` call `p.Prewarm`.
- internal/project/project.go:26-59 `Active` and `ActivationRefused` repeat the same marker loop.
- internal/hooks/hooks.go:47-57 `load` ends with `if err != nil { return nil, err }; return p, nil`; return `project.Open(env.Root)` directly.
- cmd/auto-context/main.go:272 `runAck` has an unused `stdout` parameter and repeats the `loadRepo` block for `--exclude` and normal paths.
- internal/hooks/session_start.go:242-247 `SessionFailure` uses `json.Marshal` and drops the error, unlike the rest, which use `encodeJSON`.
- Three near-identical hook-output structs: `stopOutput`, `sessionOutput`, `preToolUseOutput`.

### 17. [Low] Doc comments missing or inaccurate
- internal/hooks/hooks.go:1 "implements the Claude Code hook handlers" and :13 "payload Claude Code passes": the package also serves Codex.
- Exported types without comments:
  - docstate.go:16,24 (`State`, `UnitStatus`)
  - units.go:26,32,83 (`Unit`, `Kind`, `IsUnit`)
  - config.go:34,52 (`Config`, `Default`)
  - rootindex.go:22,26 (`Entry`, `ErrBrokenMarkers`)
- internal/units/walk.go:524-525 comment typo "a index file".

## Checked and accurate (no finding)
- README config defaults match `config.Default()` (extensions, markers, exclude, min_files, max_session_notice).
- `hookBudget` and `codexSessionEndBudget` are below the `hooks.json` and `codex.json` timeouts.
- All other `Зависит от` / `Используется в` lists match the actual imports (fsutil, gitx, hashing, session, project, docstate, rootindex, logx, config, cmd).
- context/internal/hooks.md claims about `Agent.CLI` using `path.Join`, `approvable`, the Stop steps and the journal flags match the code.
- Root CLAUDE.md unit list matches `auto-context units` output (14 units plus `_root`).
