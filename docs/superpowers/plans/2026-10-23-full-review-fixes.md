# Исправления по полному ревью 2026-10-23 — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. План содержит блок ```` ```paraplane ````: волны выполняются по правилам `paraplane:rules` (задачи волны параллельно, коммитит только координатор, один коммит на волну после зелёного гейта).

**Goal:** закрыть все находки полного ревью 2026-10-23: исполнение кода через `.git/config`, утечки текста репозитория в контекст Claude, квадратичный `rootindex`, `source_hash`, зависящий от неотслеживаемых файлов, контракт CLI, пробелы в тестах, документацию, CI/релиз. Затем отдельной фазой провести крупные рефакторинги.

**Architecture:** фаза A (волны W1–W6) — безопасность и корректность. Сначала листовые пакеты (`gitx`, `textx`, `fsutil`, `testutil`, `config`, `units`, `session`, `logx`, `hashing`, ввод хуков, шим), потом вверх по графу зависимостей: `rootindex`/`frontmatter`/`units` → `docstate` → `project` → `hooks` → `cmd`. Фаза B (W7–W8) — зависимости отдельной волной, затем навыки, README, документы проекта, CI/релиз. Фаза C (W9–W14) — рефакторинги без смены поведения: `context.Context`, `os.Root`, неизменяемый `units.Tree`, формат кэша, пакет `internal/turn`, модель проблем, затем модернизация кода. Каждая задача оставляет дерево компилируемым и тесты зелёными: экспортированный API меняется только добавлением, старое удаляет задача, которая владеет всеми вызывающими.

**Tech Stack:** Go 1.27.3 (модуль `auto-context`), `gopkg.in/yaml.v3` → `go.yaml.in/yaml/v3` v3.0.5 (T23), `doublestar/v4`, POSIX sh-шим, GitHub Actions, Claude Code 2.1.304, Codex CLI.

**Spec:** `docs/superpowers/audits/2026-10-23/` — итог `05-final-report.md`, фазовые сводки `01`–`04`, полные отчёты с file:line, PoC и предложенными исправлениями в `raw/` (`1A-code-quality.md`, `1B-architecture.md`, `2A-security.md`, `2B-performance.md`, `3A-testing.md`, `3B-documentation.md`, `4A-best-practices.md`, `4B-cicd.md`, `area-*.md`). Исполнитель задачи читает разделы `raw/` по ID из своей задачи: там точные места и предложенный код. Фон: `context/_root.md`, `context/<unit>.md`.

## Global Constraints

- Go `1.27.3`, модуль `auto-context`. Новых зависимостей нет, кроме `go.yaml.in/yaml/v3 v3.0.5` (T23) и отдельного модуля инструментов `tools/go.mod` (T22).
- Хук всегда завершается с кодом 0; неактивный проект (нет `.auto-context/config.yaml` и `context/_root.md`) не получает записей.
- **Инвариант вывода:** в stdout/stderr CLI, `additionalContext`/`systemMessage` и тексты хуков из репозитория попадают только (а) имена, прошедшие `units.SafeName`, (б) числа, (в) фиксированные категории (`fsutil.Category`, `gitx.Brief`), (г) вывод `changes` (темы коммитов, имена файлов) после `textx.StripControls`. Полный текст ошибки — только в `.auto-context/log` (`logx.Errorf`).
- **Схема хэша v2:** `source_hash: v2:sha256:<hex>`. Вход: файлы юнита в «индексном виде» (git-режим: только записи индекса, `ls-files` с тегом `H`/`S`/`M`/`R`/`C`/`K`; режим обхода: все найденные файлы), для `_root` — юниты индексного вида. Файл больше `32 MiB` даёт сумму `sha256("large\x00" + <size десятичным> + "\x00" + LF(первые 1 MiB) + LF(последние 1 MiB))`. Прежний формат `sha256:<hex>` — «старая схема»: если он совпадает с хэшем по прежним правилам (все файлы, включая неотслеживаемые; все юниты), юнит `ok` с нотой `old hash scheme` (State OK, Note `NoteOldScheme`); иначе `stale`.
- **Коды выхода CLI:** 0 — успех/всё актуально; 1 — только `status --exit-code` при stale/missing; 2 — неверное использование; 3 — ошибка (не открыть проект, битый конфиг, отказ ack, неактивный проект при `status --exit-code`).
- **JSON (format_version 1):** `status --json` → `{"format_version":1,"paused":bool,"units":[{"unit","state","doc","note","detail"}],"orphans":{"shown":[…],"hidden":N},"warnings":[…],"notes":[…]}`; `units --json` → `{"format_version":1,"units":[{"path","reason","files"}]}`. `--json` сочетается с `--stale` и `--exit-code`.
- `units.SafeName`: только буквы, цифры, метки и `._-@+/` (пробел запрещён); ни один сегмент не начинается с `-`; длина ≤ 128 байт, сегмент ≤ 64 байт.
- Каждое изменение `README.md` повторяется в `README.ru.md` рядом (английский — основной).
- Комментарии в Go — по-английски, в стиле окружающего кода; `context/*.md` и `CLAUDE.md` юнитов — по-русски (`language: ru`).
- Подзадачи не коммитят и не запускают `auto-context ack`; frontmatter в `context/*.md` не трогают (только текст). Координатор после гейта волны подтверждает изменённые юниты собранным из дерева бинарём: `go run ./cmd/auto-context ack <units>` (после W6 — ещё и `go run ./cmd/auto-context ack --upgrade`). Установленный плагин v1.0.0 будет считать хэши v2 устаревшими — это ожидаемо до релиза.
- `verify` задачи компилирует и пакеты, которые в той же волне правят другие задачи (например, `internal/units` в W2). Падение `verify` в чужом пакете до завершения его задачи — не ошибка; повторить `verify` после неё. Полную проверку делает гейт волны.
- CHANGELOG: разделы `## vX.Y.Z` (подзаголовки `### Breaking`, `### Fixed`, `### Added`), release-workflow вырезает раздел своего тега.

## Review Focus

1. **Обновление существующего проекта v1.0.0** (доки с `sha256:`-хэшами, в дереве есть неотслеживаемые файлы): без правок кода ни один юнит не должен стать `stale` из-за обновления; `ack --upgrade` переводит их на v2. Тест `TestUpgradeFromV1Project` — в T18.
2. **Связанный git worktree и сабмодуль** (README советует worktree для параллельных сессий): новая проверка топологии git не должна переводить их в режим обхода. Тесты `TestWorkTreeOKLinkedWorktree`, `TestWorkTreeOKSubmodule` — в T1; `TestDiscoverInLinkedWorktreeUsesGit` — в T14.
3. **git вне `/usr/bin`** (Homebrew `/opt/homebrew/bin`, `/usr/local/bin`): шим сужает PATH только для своих утилит, бинарь получает исходный PATH. Тест `TestShimPassesOriginalPATH` — в T5.
4. **Каталоги с пробелом, бывшие юнитами:** после обновления они не юниты, их доки — сироты; `status` показывает это нотой, хуки не падают. Тест `TestSpaceDirNoLongerUnit` — в T7; запись в CHANGELOG — T21.
5. **Codex с путём плагина с пробелом или кавычкой** (`PLUGIN_ROOT`): текст команды в сообщениях хуков остаётся корректно закавыченным и одобряемым. Тест `TestCodexCLIFromPluginRoot` — в T8.

---

## Покрытие находок

ID — из отчётов `docs/superpowers/audits/2026-10-23/raw/`. Префиксы ревью по областям: `HS-n` — `area-hooks-session.md` п.n, `CL-n` — `area-cli-infra.md`, `PK-n` — `area-packaging.md`, `CO-n` — `area-core-data.md`. `Q-` — `1A`, `A-` — `1B`, `S-`/`I-` — `2A`, `P-` — `2B`, `T-` — `3A`, `D-` — `3B`, `BP-` — `4A`, `CD-` — `4B`. «GH» — чек-лист настроек GitHub в конце плана (выполняет владелец).

| Находка | Задача | Находка | Задача | Находка | Задача |
|---|---|---|---|---|---|
| S-1 | T1 | Q-H1 | T12 | P-M1 | T11, T16 |
| CL-1 (gpg.program) | T1, T16 (тест) | Q-H2 | T12, T13 | P-M2 | T17 |
| CL-2 (WaitDelay) | T1 | Q-M1 | T17 | P-M3 | T15, T16 |
| S-4 | T1, T14, T16 | Q-M2 | T26 | P-M4 | T11 (часть), T27 |
| S-2 | T7, T17 | Q-M3 | T6 | P-M5 | T14 (лимит), T26 (копии) |
| S-3 | T16, T18 | Q-L1 | T17 | P-M6 | T11 (размер), T24 (дедлайн) |
| S-5 | T19 | Q-L2 | T2, T12, T13 | P-L1 | T3 |
| S-6 | T19 | Q-L3 | T1, T3, T14, T16 | P-L2 | T16 |
| I-1 | T21 (док) | Q-L4 | T9, T17 | P-L3 | T7 |
| HS-1 | T5 | Q-L5 | T16 | P-L4 | T18 |
| HS-2 / CO-3 | T7, T17 | Q-L6 | T18 | P-L5 | T11 |
| HS-3 | T17 | Q-L7 | T6 | P-L6 | T27 |
| HS-4 | T17 | Q-L8 | T15 | P-L7 | T14, T20 |
| HS-5 | T8 | Q-L9 | T6, T18 | P-I1 | T9 |
| HS-6 / A-L2 | T18 | Q-L10 | T9, T17 | T-H1 | T4, T17 |
| HS-7 / A-L3 | T24 | Q-old#16a (Active) | T16 | T-H2 | T14 |
| CL-3 | T10, T16 | Q-old#16b (выходы хуков) | T17 | T-M1 | T4, T9, T11, T15 |
| CL-4 | T6, T16 | Q-old#15 (Cache.Len) | отклонено (см. ниже) | T-M2 | T16 |
| CL-5 | T5, T6 | A-H1 | T14, T15, T16, T18, T19, T20 | T-M3 | T16 |
| CL-6 | T6 | A-M1 | T28 | T-M4 | T16, T17 |
| CL-7 | T18 | A-M2 | T30 | T-M5 | T22 |
| CL-8 | T2 | A-M3 | T18, T19, T20 | T-M6 | T18 |
| CL-9 | T10 | A-M4 | T9, T13, T27, T18 | T-M7 | T7, T8, T11, T12, T13, T14, T17 |
| CO-1 | T15 | A-L1 | T8, T17 | T-L1 | T12 |
| CO-2 (= Q-H1) | T12 | A-L4 | T6, T26, T21 (док шима) | T-L2 | T3 |
| CO-4 | T7, T21 (док) | A-L5 | T26 | T-L3 | T17 |
| CO-5 | T13 | A-L6 | T16 | T-L4 | T17 |
| CO-6 | T15 | A-L7 | T19 | T-L5 | T3 |
| PK-1 | T5, T22 | A-L8 | T6, T16, T17 | T-L6 | T17 |
| PK-2 | T22 | A-L9 | T18 | T-L7 | T22 |
| PK-3 | T22 | D-H1 | T20 | BP-M1 | T23 |
| PK-4 | T22 | D-H2 | T20 | BP-M2 | T24 |
| PK-5 | T18 | D-M1 | T21 | BP-M3 | T23, T22 |
| CD-H1 | GH | D-M2 | T19 | BP-M4 | T22 |
| CD-M1 | T22, GH | D-M3 | T20 | BP-L1..L3 | T31 |
| CD-M2 | T21, T22 | D-M4 | T20 | BP-L4 | T25 |
| CD-M3 | T22 | D-M5 | T21, T22 | BP-L5 | T3 |
| CD-M4 | T22, T20 | D-M6 | T21 | BP-L6 | T18 |
| CD-M5 | T21, GH | D-L1..L7, D-L11 | T20 | BP-L7 | T7, T9, T15, T18 |
| CD-M6 | T22, GH | D-L8 | T21 | BP-L8 | T8 |
| CD-M7 | T29 | D-L9 | T20 | BP-L9 | T10 |
| CD-L1 | T23, T22 | D-L10 | T3 | BP-L10 | T8, T18 |
| CD-L2..L5, L7, L8 | T22 | BP-I3 | T8 | BP-L11 | T22 |
| CD-L6 | T18, T21 | BP-I1, I2, I4..I6 | без действий (информация) | BP-L12 | T20 |
| CD-L9 | GH | | | | |

### Документированные ограничения (не исправляются в коде)

| Находка | Решение | Где описать |
|---|---|---|
| CO-4, часть «байты против рун» (`?` и классы на не-ASCII в режиме обхода) | Режим обхода — запасной; байтовая семантика wildmatch не стоит переписывания сопоставителя. | `context/internal/units.md` (T14) |
| T-H2, часть «каждый хук ждёт таймаут» | Обход дерева, которое git не перечислил вовремя, тоже не уложится в таймаут; исправляются сообщение и тесты. | `context/internal/units.md` (T14), README Troubleshooting (T20) |
| I-1 (git читает `core.excludesFile`, `include.path` вне репо) | Содержимое в вывод не попадает; после T1 значимых последствий нет. | `context/internal/gitx.md` (T1) |
| Q-old#15 (`Cache.Len` только в тестах) | Отклонено: `Len` нужен тестам двух пакетов (`internal/hashing`, `internal/project/ops_test.go:121`); `export_test.go` между пакетами не работает. | — |
| Хэш больших файлов (> 32 MiB) по выборке | Изменение в середине файла при том же размере и mtime не видно — тот же класс риска, что racy-кэш; для CRLF-чекаутов размер другой. | `context/internal/hashing.md` (T11), CHANGELOG (T21) |

---

## Фаза A — безопасность и корректность

### Task T1: gitx — запрет транспортов, подписи, дочерних процессов и чужой топологии

**Files:**
- Modify: `internal/gitx/gitx.go:104-160` (`Run`, `environ`)
- Create: `internal/gitx/proc_unix.go`, `internal/gitx/proc_other.go` (группа процессов)
- Create: `internal/gitx/topology.go`
- Test: `internal/gitx/gitx_test.go`, `internal/gitx/topology_test.go`
- Docs: `context/internal/gitx.md`

**Interfaces:**
- Produces: `func NotRepo(err error) bool` — `*Error` с `Code == 128` и `"not a git repository"` в `Stderr`; `func WorkTreeOK(root string) (bool, error)` — `rev-parse --show-toplevel --absolute-git-dir`: true, если реальный root равен toplevel или лежит под ним, и git-каталог (а) под toplevel, или (б) содержит файл `gitdir`, который указывает на `<root>/.git` (связанный worktree), или (в) лежит под `<A>/.git/modules/` или `<A>/.git/worktrees/<n>/modules/` для строгого предка `A` корня (сабмодуль); если toplevel ≠ реальный root, git-каталог не может лежать под root (`core.worktree`, указывающий вверх). false и nil-ошибка — топология чужая; ошибка — git не ответил.

- [x] **Step 1: Write the failing tests** (`gitx_test.go`):
  - `TestRunNoLazyFetch` — таблица `uploadpack`, `core.sshCommand`, `ext::` (`protocol.ext.allow=always`), `core.gitProxy`: репо по PoC из `raw/2A-security.md` S-1 (skip-worktree `.gitignore`, удалённый блоб, `extensions.partialClone`), `Run(root, 0, "ls-files", "-co", "--exclude-standard", "-z")` → файл-маркер скрипта не появился. Skip, если `git config extensions.partialClone` не поддерживается.
  - `TestRunIgnoresShowSignature` — локальный `log.showSignature=true`, `gpg.program=<скрипт, создающий маркер>`, коммит с заголовком `gpgsig` (объект собрать `git hash-object -t commit -w` и `update-ref`), `Run(root, 0, "log", "-n1")` → маркера нет.
  - `TestRunTimeoutWithGrandchild` — `testutil.FakeGit` со скриптом `sleep 30 & wait`, `Timeout = 200ms` → `Run` вернулся быстрее 2 с, `TimedOut == true`.
  - `TestNotRepo` — `Run` в каталоге без git → `NotRepo(err)`; ошибка с кодом 128 и другим stderr → false.
  - `topology_test.go`: `TestWorkTreeOK` (обычный репо → true), `TestWorkTreeOKCoreWorktreeOutside` (`core.worktree=../other` → false), `TestWorkTreeOKForeignGitdir` (`.git`-файл `gitdir:` на другой репо → false), `TestWorkTreeOKLinkedWorktree` (`git worktree add` → true), `TestWorkTreeOKSubmodule` (`git submodule add` локального репо, root = каталог сабмодуля → true).
- [x] **Step 2: Run** `go test ./internal/gitx/` — Expected: FAIL (маркеры появляются; таймаут ждёт 30 с; `NotRepo`/`WorkTreeOK` не определены).
- [x] **Step 3: Implement.** В `Run`: env добавить `GIT_NO_LAZY_FETCH=1`, `GIT_ALLOW_PROTOCOL=none`, `GIT_PROTOCOL_FROM_USER=0`; фиксированные аргументы — `-c log.showSignature=false` рядом с `core.fsmonitor`; `cmd.WaitDelay = time.Second`; на Unix `SysProcAttr{Setpgid: true}` и `cmd.Cancel`, убивающий группу (`syscall.Kill(-pid, SIGKILL)`), в `proc_unix.go`; в `proc_other.go` — без изменений поведения. `NotRepo` и `WorkTreeOK` — по Interfaces.
- [x] **Step 4: Run** `go test ./internal/gitx/` — Expected: PASS.
- [x] **Step 5: Docs.** В `context/internal/gitx.md` перечислить фактические защиты (env, `-c`, WaitDelay/группа) и `WorkTreeOK`; добавить ограничение I-1.

### Task T2: textx — bidi в многострочном выводе, общие BOM/EOL

**Files:** Modify `internal/textx/textx.go`; Test `internal/textx/textx_test.go`; Docs `context/internal/textx.md`.

**Interfaces:** Produces `const BOM = "﻿"`; `func EOL(s string) string` — `"\r\n"`, если первая строка кончается на `\r\n`, иначе `"\n"` (как нынешние `eolOf` в frontmatter и rootindex).

- [x] **Step 1: Tests.** `TestStripControlsBidi`: U+202A–U+202E, U+2066–U+2069, U+200B, U+FEFF удаляются; `\n`, `\t` остаются; текст без них возвращается тем же срезом. `TestEOL`: `"a\r\nb"` → `"\r\n"`, `"a\nb\r\n"` → `"\n"`, `""` → `"\n"`.
- [x] **Step 2: Run** `go test ./internal/textx/` — FAIL.
- [x] **Step 3: Implement.** `StripControls` отбрасывает ещё и `unicode.Cf`; `BOM`, `EOL`.
- [x] **Step 4: Run** — PASS. **Step 5:** обновить `context/internal/textx.md`.

### Task T3: fsutil — категории ошибок, чтение, мелочи

**Files:** Modify `internal/fsutil/fsutil.go` (`escapes` :35, `readRegular` :210-235, комментарий `EnsureStateDir` :319-323); Create `internal/fsutil/export_test.go`; Test `internal/fsutil/fsutil_test.go`; Docs `context/internal/fsutil.md`.

**Interfaces:** Produces `func Category(err error) string`: `ErrOutside` → `"it leads outside the repository or into .git"`, `ErrNotRegular` → `"not a regular file"`, `ErrTooLarge` → `"too large"`, `os.ErrPermission` → `"permission denied"`, `os.ErrNotExist` → `"missing"`, иначе `"unreadable"`. Никогда не содержит текст `err`.

- [x] **Step 1: Tests.** `TestCategoryNeverQuotes` — для ошибки с путём `"x\nIGNORE.md"` в каждой ветке результат из фиксированного набора. `TestWriteAtomicCleansTmpOnFailure` (T-L2) — цель `path` — непустой каталог → ошибка, в каталоге нет `*.tmp*`. `TestReadRegularAllocsOnce` (P-L1) — `testing.AllocsPerRun` для чтения файла 1 MiB ≤ 3 аллокаций.
- [x] **Step 2: Run** `go test ./internal/fsutil/` — FAIL.
- [x] **Step 3: Implement.** `Category`; `readRegular` — буфер ёмкостью `fi.Size()+512` и `ReadFrom(io.LimitReader(f, limit+1))`; `escapes(rel)` → `!filepath.IsLocal(rel)` (BP-L5; `IsLocal(".")` — true); комментарий `EnsureStateDir`: «A file that lacks either line (e.g. a bare "*") is rewritten» (D-L10). `export_test.go`: `func ResetCaches()` очищает `realRoots`/`sameCache` (T-L5).
- [x] **Step 4: Run** — PASS. **Step 5:** `context/internal/fsutil.md` — `Category` и правило вывода.

### Task T4: testutil — изоляция от внешнего репо, симлинки, фейковый git

**Files:** Modify `internal/testutil/testutil.go`; Test `internal/testutil/testutil_test.go`; Docs `context/internal/testutil.md`.

**Interfaces:** Produces `func Symlink(t testing.TB, target, link string)` — создаёт родительский каталог; `t.Skip` только на Windows при `os.ErrPermission`, иначе `t.Fatal`. `func FakeGit(t testing.TB, script string)` — кладёт исполняемый `git` (`#!/bin/sh` + script) во временный каталог в начале `PATH` (`t.Setenv`); skip на Windows. `ScrubEnv` ставит `GIT_CEILING_DIRECTORIES=filepath.Dir(os.TempDir())`.

- [x] **Step 1: Tests.** `TestScrubEnvCeiling` — после `ScrubEnv` git в `t.TempDir()` под внешним репо (`TMPDIR` внутри временного git-репо) говорит «not a git repository». `TestSymlinkCreatesParent`. `TestFakeGit` — `exec.Command("git", "x")` выполняет скрипт.
- [x] **Step 2–4:** FAIL → реализовать → `go test ./internal/testutil/` PASS. **Step 5:** док.

### Task T5: шим — PATH, канонический HOME, концы строк

**Files:** Modify `bin/auto-context`; Modify `.gitattributes`; Test `cmd/auto-context/shim_test.go`.

**Interfaces:** нет новых. Шим вызывает свои утилиты при `PATH=/usr/bin:/bin`, а бинарь запускает с исходным `PATH`.

- [x] **Step 1: Tests.** `TestShimRelativePATHEntry` — `PATH=.:$PATH`, в cwd исполняемый `uname`, пишущий маркер → маркера нет, фейковый бинарь из `libexec` выполнен. `TestShimPassesOriginalPATH` (Review Focus 3) — фейковый бинарь печатает `$PATH` → совпадает с исходным. `TestShimHomeTrailingSlash` (CL-5) — `HOME=<home>/`, в `<home>` есть `.git` и `.auto-context/config.yaml`, проект — `<home>/proj` без маркеров → `active` ложно (сообщения SessionStart нет). `TestShimGitattributesLF` (PK-1) — `git check-attr eol -- bin/auto-context` → `lf`.
- [x] **Step 2: Run** `go test ./cmd/auto-context/ -run Shim` — FAIL.
- [x] **Step 3: Implement.** Первые строки после shebang: `orig_path=$PATH`, затем PATH для утилит шима — исходный PATH без пустых и относительных элементов (только элементы, начинающиеся с `/`; разбор встроенными средствами sh, без внешних команд); перед каждым запуском `"$bin"` — `PATH=$orig_path` (бинарь должен видеть исходный PATH: по нему `shadowed()` в PreToolUse судит о PATH Bash-инструмента). В `active()` — `start=$(cd … && pwd -P)`, `home=$(cd "$HOME" 2>/dev/null && pwd -P)`, сравнение с `$home`. `.gitattributes`: строка `bin/auto-context text eol=lf`.
- [x] **Step 4: Run** — PASS; `sh -n bin/auto-context`.

### Task T6: config — один разбор YAML, безопасные причины, якоря, HOME

**Files:** Modify `internal/config/config.go` (`Load` :113-157, `decodeConfig` :177, `typeProblem` :216, `fix` :251-320, `AddExclude` :336-427, `FindRoot` :446-465); Test `internal/config/config_test.go`; Docs `context/internal/config.md`.

**Interfaces:** Produces поле `Config.DiscoveryReset bool` с тегом `yaml:"-"` — true, если `fix` сбросил или отбросил что-то в `include`, `exclude`, `track`, `min_files`, `code_extensions`, `module_markers`. `func EscapePattern(dir string) string` — экранирует `\ * ? [ ] { }` обратной косой (doublestar). Тип `Problems` и сигнатура `Load` не меняются.

- [x] **Step 1: Tests.** `TestLoadDuplicateKey` (Q-M3): `min_files: 3\nmin_files: 7` → проблема `line 2: duplicate key min_files`, `MinFiles` = 3 (первое значение), нет «wrong type»; дубликат `index_file` — по-прежнему `ErrBadIndexFile`. `TestProblemsNeverQuotePattern` (Q-L7): шаблон с текстом `IGNORE` длиной > лимита → в `Problems` только `invalid pattern`. `TestDiscoveryReset`: неверный шаблон в `exclude` → `DiscoveryReset`; неверный `language` → нет. `TestAddExcludeAnchor` (CL-6): `exclude: &ex [a/**]\ntrack: *ex` → после `AddExclude("b")` `track` прежний, `exclude` содержит `b`. `TestAddExcludeKeepsBlankLines` — пустые строки и `%YAML` сохраняются. `TestAddExcludeBrokenNamesFile` (Q-L9): ошибка начинается с `.auto-context/config.yaml cannot be used:`. `TestFindRootHomeTrailingSlash`, `TestFindRootHomeSymlink` (CL-5). `TestEscapePattern`: `lib[v1]` → `lib\[v1\]`, и `doublestar.Match(EscapePattern(d), d)` для `a*b`, `x?`, `{y}`.
- [x] **Step 2: Run** `go test ./internal/config/` — FAIL.
- [x] **Step 3: Implement.** Разбор один раз в `yaml.Node`, дальше по ключам из `raw/1A-code-quality.md` Q-M3 (merge-ключи `<<` декодировать целым узлом, поведение закрепить тестом). Проверки в `fix` возвращают безопасную причину (`type check func(string) (reason string, ok bool)`), без разбора текста ошибки. `AddExclude`: узел с `Anchor` или ключ через alias/merge → заменить `exclude` новым узлом-списком; ошибки разбора — `fmt.Errorf("%s cannot be used: %s", FileName, Describe(err))`. `FindRoot`: `home` и `cwd` через `filepath.Clean` + `EvalSymlinks` перед сравнением.
- [x] **Step 4: Run** — PASS. **Step 5:** док (`DiscoveryReset`, `EscapePattern`, правило дубликатов).

### Task T7: units (часть 1) — имена юнитов, формы .gitignore, вложенные репо, обход

**Files:** Modify `internal/units/units.go:178-191` (`SafeName`), `internal/units/ignore.go` (`globPattern` :218, `bracketClass` :258), `internal/units/walk.go` (`walk` :369-428); Create `internal/units/fuzz_test.go`; Test `internal/units/units_test.go`, `internal/units/git_test.go`.

**Interfaces:** Produces `var ErrUnknownUnit = errors.New("unknown unit")` (BP-L7). `SafeName` — по Global Constraints.

- [x] **Step 1: Tests.** `TestSafeNameRules`: `"a b"`, `"-rf"`, `"x/--exclude"`, 129 байт, сегмент 65 байт → false; `"svc/auth"`, `"модуль"`, `"a.b@c+d"` → true. `TestSpaceDirNoLongerUnit` (Review Focus 4; заменить ожидание `"ok dir"` в `units_test.go:410`). `TestIgnorePOSIXClasses`: `a[[:digit:]].go`, `a[![:digit:]].go`, `[[:alpha:]]*.go` в обходе совпадают с `git check-ignore`. `TestIgnoreTripleStar`: `***/c.go` как `**/c.go`. `TestWalkSkipsNestedRepo`: `inner/.git` (каталог и файл) → файлы `inner/` не перечислены, `inner` не юнит. `FuzzIgnoreMatchesGit` — дифференциально против `git check-ignore --no-index` (алфавит `ab*?[]!-\/.^`, skip без git) — по `raw/3A-testing.md` M-7.
- [x] **Step 2: Run** `go test ./internal/units/` — FAIL.
- [x] **Step 3: Implement.** `SafeName`; в `bracketClass` перевод ASCII-классов `[:alpha:] [:digit:] [:alnum:] [:upper:] [:lower:] [:space:] [:xdigit:] [:punct:]` в диапазоны; `***` и длиннее → `**`; в обходе пропускать каталог, где есть `.git` (файл или каталог); lstat кандидатов обхода — через `parallelLstat` после `WalkDir`, как в git-ветке (P-L3).
- [x] **Step 4: Run** — PASS.

### Task T8: hooks (ввод) — строгий `command`, имя события, корень плагина

**Files:** Modify `internal/hooks/hooks.go` (`Input`, `Command` :23-31, `Env`), `internal/hooks/agent.go` (`CLI` :58-66), `internal/hooks/pre_tool_use.go`, `internal/hooks/permission_request.go`; Create `internal/hooks/fuzz_test.go`; Test `internal/hooks/pre_tool_use_test.go`, `internal/hooks/agent_test.go`, `internal/hooks/permission_request_test.go`.

**Interfaces:** Produces поле `Input.HookEventName string` (`json:"hook_event_name"`); `func CheckEvent(event string, in Input) error` — ошибка, если `HookEventName` непуст и не равен имени события агента для `event` (`session-start` → `SessionStart`, `prompt-submit` → `UserPromptSubmit`, `stop` → `Stop`, `pre-tool-use` → `PreToolUse`, `session-end` → `SessionEnd`, `permission-request` → `PermissionRequest`); поле `Env.PluginRoot string`; `func (e Env) CLI() string` — как `Agent.CLI()`, но корень плагина из `e.PluginRoot`, а при пустом — из `PLUGIN_ROOT` (только Codex), затем `CLAUDE_PLUGIN_ROOT`. `Agent.CLI()` остаётся обёрткой до T17.

- [x] **Step 1: Tests.** `TestCommandCaseVariantKeys` (HS-5): `{"command":"rm -rf ~","Command":"auto-context status"}`, `{"COMMAND":"auto-context status"}` → `Command()` пусто, PreToolUse не одобряет. `TestCommandArrayStillEmpty`. `TestCheckEvent`. `TestCodexCLIFromPluginRoot` (Review Focus 5): `PluginRoot="/p a'b"` → текст команды закавычен и проходит одобрение Codex. `FuzzAllowedCommand` (`raw/3A-testing.md` M-7: одобренная команда не содержит `..`, `--exclude`, байтов вне `[A-Za-z0-9_./ -]`).
- [x] **Step 2: Run** `go test ./internal/hooks/ -run 'Command|CheckEvent|CodexCLI|Fuzz'` — FAIL.
- [x] **Step 3: Implement.** `Command()` разбирает `tool_input` в `map[string]json.RawMessage`; берёт ровно ключ `command`; если есть другой ключ, равный `command` без учёта регистра, — `""`. Комментарий про «array in Codex» уточнить (BP-I3).
- [x] **Step 4: Run** `go test ./internal/hooks/` — PASS.

### Task T9: session — журнал с версией, ID сессии, срок снимков

**Files:** Modify `internal/session/session.go` (`RemoveStale` :146, `Journal` :195, `LoadJournal` :233-249, комментарий `AddCode` :249); Create `internal/session/testmain_test.go`; Test `internal/session/journal_test.go`, `internal/session/snapshot_test.go`; Docs `context/internal/session.md`.

**Interfaces:** Produces `func ValidID(id string) bool` (непустой после `strings.TrimSpace`); поле `Journal.Version int` (`json:"version"`), текущее значение `1`; `LoadJournal` для другой версии возвращает пустой журнал без ошибки.

- [x] **Step 1: Tests.** `TestLoadJournalNormalizesAckFail` (Q-L4); `TestJournalForeignVersion`; `TestValidID`; `TestRemoveStaleSnapshots24h` (P-I1): снимок старше 24 ч удалён, журнал того же возраста остаётся до `maxAge`.
- [x] **Step 2–4:** FAIL → реализовать (`AckFailNotified = AddUnique(...)`; снимки удаляются при возрасте больше `min(maxAge, 24h)`; doc-комментарий `AddCode` начинается с имени — ST1020; `testmain_test.go` вызывает `testutil.Main`) → `go test ./internal/session/` PASS. **Step 5:** док.

### Task T10: logx — гонка снятия lock, flock без Fd, state-dir

**Files:** Modify `internal/logx/lock_excl.go:230-250`, `internal/logx/lock_flock.go:15-30`, `internal/logx/logx.go:33-80`; Test `internal/logx/logx_test.go`; Docs `context/internal/logx.md`.

- [x] **Step 1: Tests.** `TestStaleLockNotStolen` (CL-9): протухший lock подменён свежим между `Lstat` и удалением → свежий не удалён (через `os.SameFile` с ранее полученным `FileInfo`; подмену вызвать через тестовый хук `beforeRemove func()`). `TestWriteCreatesStateDirGitignore` (CL-3): в активном проекте без `.auto-context/` `Errorf` создаёт `.auto-context/.gitignore`.
- [x] **Step 2–4:** FAIL → реализовать (`SameFile` перед удалением; `f.SyscallConn().Control` для `syscall.Flock` — BP-L9; `write` вызывает `fsutil.EnsureStateDir`, если каталога нет) → `go test ./internal/logx/` PASS. **Step 5:** док.

### Task T11: hashing — большие файлы, лишние перезаписи, сохранение без остановки

**Files:** Modify `internal/hashing/hashing.go` (`LoadCache` :68-97, `Len` :104, `Prune` :107, `hashFile` :197-230, `Prewarm` :239-297); Create `internal/hashing/testmain_test.go`, `internal/hashing/fuzz_test.go`; Test `internal/hashing/hashing_test.go`; Docs `context/internal/hashing.md`.

**Interfaces:** Produces `const MaxHashSize = 32 << 20`; `func (c *Cache) Dirty() bool`. Сигнатуры `SourceHash`, `Prewarm`, `Save`, `LoadCache` не меняются.

- [x] **Step 1: Tests.** `TestHashLargeFileSampled` (P-M6): разреженный файл `MaxHashSize+1` → сумма по формуле Global Constraints; читается не больше 2 MiB (обёртка-счётчик). `TestLoadDropsRacyWithoutDirty` (P-M4): кэш с racy-записью → после `LoadCache` `Dirty()==false`. `TestPruneEmptyNoop`. `TestPrewarmSaveDoesNotStallWorkers` (P-L5): `save`, спящий 200 мс, не увеличивает общее время прогрева больше чем на 300 мс. `FuzzLFWriter` (`raw/3A-testing.md` M-7).
- [x] **Step 2–4:** FAIL → реализовать (буферизованный канал результатов, `make(chan result, 256)`, и сохранение в отдельной горутине; `wg.Go`) → `go test ./internal/hashing/` PASS. **Step 5:** док + ограничение про выборку.

### Task T12: rootindex — линейный разбор fence, санитизация, `\r`

**Files:** Modify `internal/rootindex/rootindex.go` (`eolOf` :41-47, `sanitize` :69-75, `fenced` :102-124, `Update` :174-236); Create `internal/rootindex/fuzz_test.go`; Test `internal/rootindex/rootindex_test.go`; Docs `context/internal/rootindex.md`.

**Interfaces:** Consumes `textx.BOM`, `textx.EOL` (T2). Семантика fence прежняя: открывающая строка без последующей подходящей закрывающей ничего не скрывает.

- [x] **Step 1: Tests.** `TestUpdateManyUnclosedFencesFast` (Q-H1): 50 000 строк `~~~a` и ```` ```a ```` → `Update` и `Check` < 200 мс. `TestFencedSemanticsUnchanged` — таблица существующих случаев плюс «незакрытый fence перед маркерами — маркеры видны». `TestRenderLongSummary` (Q-H2): summary 1 MiB из `"<"*k + "!--"*k` → строка индекса ≤ 200 рун, < 50 мс. `FuzzUpdate` с seed `"\r"` (T-L1): после успешного `Update` повторный ничего не меняет и `Check()==nil`. `BenchmarkUpdateUnclosedFences`.
- [x] **Step 2: Run** `go test ./internal/rootindex/` — FAIL.
- [x] **Step 3: Implement.** Один проход снизу вверх: для каждого `(символ, длина ≥ n)` — индекс ближайшей следующей закрывающей; затем проход сверху вниз со стеком (алгоритм — `raw/1A-code-quality.md` H1, вариант «сохранить поведение»). `sanitize`: сначала `textx.Cap(textx.Clean(s), 4*maxSummary)`, потом цикл удаления. Файл без `\n`, но с `\r` — EOL по `textx.EOL` (`"\n"`); секция пишется так, чтобы второй `Update` её не переписывал.
- [x] **Step 4: Run** — PASS. **Step 5:** док.

### Task T13: frontmatter — сохранение чужих ключей, общий BOM/EOL, короткий summary

**Files:** Modify `internal/frontmatter/frontmatter.go` (`oursRe` :37, `eolOf` :95, `SetHash` :107-131, `Summary` :134); Create `internal/frontmatter/fuzz_test.go`; Test `internal/frontmatter/frontmatter_test.go`; Docs `context/internal/frontmatter.md`.

**Interfaces:** Consumes `textx.BOM`, `textx.EOL`. Сигнатуры не меняются. `Summary` возвращает не больше 800 рун.

- [x] **Step 1: Tests.** `TestSetHashKeepsUnknownKeys` (CO-5/A-M4): блок `unit/source_hash/tags/title` → после `SetHash` `tags` и `title` на месте, порядок `unit, source_hash, commit, updated`, затем чужие ключи в исходном порядке. `TestSetHashBrokenBlockReplaced` (битый YAML нашего блока заменяется целиком, как сейчас). `TestSummaryCapped`. `FuzzSetHashRoundTrip` (`raw/3A-testing.md` M-7).
- [x] **Step 2–4:** FAIL → реализовать (разбор в `yaml.Node`, правка значений наших четырёх ключей, остальные узлы без изменений) → `go test ./internal/frontmatter/` PASS. **Step 5:** док: правило совместимости «неизвестные ключи сохраняются».

### Task T14: units (часть 2) — хэш по индексу, топология, лимит, таймаут

**Files:** Modify `internal/units/gitfiles.go`, `internal/units/units.go` (`Tree`, новые методы), `internal/units/walk.go` (`Discover` :72-160); Test `internal/units/git_test.go`, `internal/units/units_test.go`; Docs `context/internal/units.md`, `internal/units/CLAUDE.md`.

**Interfaces:**
- Consumes: `gitx.WorkTreeOK`, `gitx.NotRepo` (T1); `testutil.FakeGit` (T4).
- Produces: `func (t *Tree) HashFiles(unit string) []string` — файлы юнита в индексном виде (nil, если `unit` не юнит индексного вида); `func (t *Tree) HashUnits() []string` — пути юнитов индексного вида без `_root`, отсортированы. `Files`, `Units`, `Tracked`, `Stats` — прежний «полный» вид (с неотслеживаемыми). В режиме обхода оба вида совпадают. `const maxFiles = 800_000` — больше → `ErrListTooLarge` (в обоих режимах).

- [x] **Step 1: Tests.** `TestHashViewExcludesUntracked` (A-H1): юнит с отслеживаемым `a.go` и неотслеживаемым `b.go` → `HashFiles` = `[a.go]`, `Files` = `[a.go b.go]`. `TestHashUnitsIgnoreUntrackedOnlyUnits`: каталог с `go.mod` только неотслеживаемым → в `Units`, нет в `HashUnits`. `TestHashViewIgnoresLocalExcludes`: `.git/info/exclude` не меняет `HashFiles` для отслеживаемых. `TestDiscoverForeignWorkTreeWalks` (S-4): `core.worktree` наружу → режим обхода, предупреждение «git's work tree or git dir lies outside the project». `TestDiscoverInLinkedWorktreeUsesGit` (Review Focus 2). `TestDiscoverLsFilesTimeout` (T-H2): `FakeGit` спит на `ls-files`, `gitx.Timeout=200ms` → `ErrGitTimeout` за < 2 с. `TestDiscoverGitFIFOGitignore` (unix): FIFO `.gitignore` → `ErrGitTimeout` за `Timeout`+1 с. `TestMaxFiles` (`maxFiles` понижен в тесте). `BenchmarkDiscover` (20k файлов, 500 юнитов, `b.ReportAllocs`).
- [x] **Step 2: Run** `go test ./internal/units/` — FAIL.
- [x] **Step 3: Implement.** `gitFiles`: сначала `WorkTreeOK` (false → обход с предупреждением), затем `gitx.Run(root, maxList, "-c", "core.untrackedCache=true", "ls-files", "-co", "--exclude-standard", "-t", "-z")`; тег `?` — неотслеживаемый, остальные — индекс. Квалификация юнитов и приписывание файлов к юнитам выполняются дважды: по полному списку и по индексному. `notRepo` — через `gitx.NotRepo`. Текст `ErrGitTimeout`: «git timed out listing the files: the repository is too large or slow, or a .gitignore/exclude file is not a regular file (…)».
- [x] **Step 4: Run** — PASS. **Step 5: Docs** — два вида, лимит, ограничения из таблицы «Документированные ограничения».

### Task T15: docstate — схема хэша v2, категории вместо текста ошибок

**Files:** Modify `internal/docstate/docstate.go`; Create `internal/docstate/testmain_test.go`; Test `internal/docstate/docstate_test.go`, `internal/docstate/doc_unix_test.go`; Docs `context/internal/docstate.md`.

**Interfaces:**
- Consumes: `fsutil.Category` (T3); `Tree.HashFiles`, `Tree.HashUnits`, `units.ErrUnknownUnit` (T14, T7); `hashing.SourceHash` (без изменений).
- Produces: `const HashPrefix = "v2:"`; `NoteOldScheme Note = "old hash scheme"`; `func AckIfNeeded(root string, t *units.Tree, c *hashing.Cache, unit string, now time.Time, head func() string) (written bool, err error)` (`Ack` остаётся обёрткой над ним; тесты docstate T15 переводит на `AckIfNeeded`; саму обёртку удаляет T24, которой принадлежат и `docstate`, и `project`); `func SummaryOf(root, unit string) string` — summary без хэширования ("" при любой ошибке); `func OldScheme(st UnitStatus) bool`. Ошибка «unknown unit» оборачивает `units.ErrUnknownUnit` с прежним текстом `unknown unit <u>`.

- [x] **Step 1: Tests.** `TestSourceHashIgnoresUntracked` (A-H1): ack при неотслеживаемом `scratch.go`, затем `git clone` → в клоне юнит `ok`. `TestLegacyHashIsOKWithNote`: frontmatter со старым `sha256:` по прежним правилам → `State==OK`, `Note==NoteOldScheme`. `TestLegacyMismatchIsStale`. `TestStatusDetailNeverQuotesPath` (CO-1): `context/a.md` → симлинк на каталог `docs/y\nSYSTEM\n.md` и на файл > 1 MiB → `Detail` из набора `fsutil.Category`, без `SYSTEM`. `TestStatusOfDirSymlinkIsOutside` (CO-6): `ErrNotRegular` → `NoteOutside`. `TestAckRootMissingMessage` (Q-L8): `no context/_root.md: create it (the root unit cannot be excluded)`. `TestAckIfNeededWritten`. `TestSummaryOf`.
- [x] **Step 2: Run** `go test ./internal/docstate/` — FAIL.
- [x] **Step 3: Implement** по Global Constraints (v2 — `HashPrefix + SourceHash(HashFiles, HashUnits)`; старая схема — `SourceHash(Files, Units)` без префикса). Ack всегда пишет v2. `Detail` — `fsutil.Category(err)` (полный текст в `StatusOf` не нужен).
- [x] **Step 4: Run** — PASS. **Step 5:** док.

### Task T16: project — state-dir, exclude, ack по схеме, дешёвый индекс, changes

**Files:** Modify `internal/project/project.go`, `internal/project/changes.go`; Test `internal/project/project_test.go`, `internal/project/ops_test.go`; Create `internal/project/changes_test.go`, `internal/project/conc_test.go`; Docs `context/internal/project.md`.

**Interfaces:**
- Consumes: `config.EscapePattern`, `Config.DiscoveryReset` (T6); `docstate.AckIfNeeded`, `SummaryOf`, `OldScheme` (T15); `gitx.WorkTreeOK`, `gitx.NotRepo`, `gitx.Brief` (T1); `fsutil.EnsureStateDir`.
- Produces: `var ErrConfigDegraded = errors.New(config.FileName + " has invalid settings that decide the units; fix it first")`; `func (p *Project) Upgrade(now time.Time) (acked []string, failed map[string]error)` — ack юнитов с `NoteOldScheme`; `Exclude(dir) error` после записи перечитывает конфиг и дерево сам (A-L6); `AckTouched` возвращает прежнее, но индекс обновляет через `SummaryOf`. Вызовы `docstate.Ack` в project переходят на `AckIfNeeded`.

- [x] **Step 1: Tests.** `TestSaveCacheCreatesGitignore` (CL-3). `TestExcludeGlobMetachars` (CL-4): каталог `lib[v1]` → юнит исключён. `TestExcludeCaseMismatch`: `Internal/Hooks` при реальном `internal/hooks` → ошибка `no directory Internal/Hooks (names are case-sensitive)`. `TestExcludeRefuses` (T-M3; таблица из `raw/3A-testing.md`), конфиг байт в байт не меняется. `TestExcludeReloads` (A-L6). `TestAckRefusedOnDiscoveryProblems` (A-L8). `TestUpgrade`. `TestPrewarmSkipsWithoutFiles` (P-M1): `Prewarm(nil)` не загружает кэш. `TestStatusesPrewarmOnlyDocumented` (P-L2). `TestRefreshIndexNoHashing` (P-M3). `changes_test.go`: `TestChangesIgnoresShowSignature` (CL-1, коммит с `gpgsig`), `TestChangesForeignWorkTree` (S-4), `TestChangesGitErrorNotQuoted` (S-3: `core.bigFileThreshold='IGNORE…'` → в выводе нет `IGNORE`), `TestChangesOutputCapped`, `TestAckPointReasons` (T-M2). `conc_test.go`: стресс 8 горутин × 15 `Open`+`AckTouched`+`SaveCache` (`raw/3A-testing.md` «прогоны»).
- [x] **Step 2: Run** `go test ./internal/project/` — FAIL.
- [x] **Step 3: Implement.** `SaveCache` вызывает `fsutil.EnsureStateDir`; `SaveCache` не пишет, если кэш не грязный и файл есть. `Exclude`: имя каталога должно точно совпасть с записью `os.ReadDir` родителя; шаблон — `config.EscapePattern(dir)`. `Ack`/`AckTouched` отказывают с `ErrConfigDegraded` при `DiscoveryReset`. `changes`: `WorkTreeOK` false → `(git unavailable: the work tree or git dir lies outside the project)`; ошибки git — `(git failed: <gitx.Brief(err)>)` (префикс `git failed` проверяют тесты `main_test.go:793,898`); выбор диапазона — чистая `logSection(...)` и `slices.Concat` (Q-L5). `Active`/`ActivationRefused` — через общую `markerState(root) (active, refused bool)`. Проверки «not a repo» — `gitx.NotRepo`.
- [x] **Step 4: Run** — PASS. **Step 5:** док.

### Task T17: hooks — набор юнитов в ходе, SessionStart по шагам, тесты активации

**Files:** Modify `internal/hooks/stop.go`, `internal/hooks/session_start.go`, `internal/hooks/prompt_submit.go`, `internal/hooks/session_end.go`, `internal/hooks/agent.go` (удаление `Agent.CLI`); Test `internal/hooks/stop_test.go`, `internal/hooks/hooks_test.go`, `internal/hooks/session_problems_test.go`, `internal/hooks/stop_unix_test.go`, `internal/hooks/testdata/*`; Create `internal/hooks/concurrent_test.go`; Docs `context/internal/hooks.md`.

**Interfaces:**
- Consumes: `Env.CLI()` (T8); `session.ValidID`, `Journal.Version` (T9); `project.Upgrade`, `ErrConfigDegraded`, `docstate.OldScheme` (T15/T16); `testutil.Symlink` (T4).
- Produces: один тип ответа `hookOutput` (поля с `omitempty`) вместо `stopOutput`/`sessionOutput`/`preToolUseOutput`/`permissionRequestOutput` (Q-old#16b) — JSON-вывод байт в байт прежний (golden-файлы не меняются, кроме новых текстов ниже). Новые строки: Stop — `Unit list changed under <parent>: check context/<parent>.md.` (родитель нового или удалённого юнита); SessionStart — `<N> unit(s) were acked by an older auto-context; run `<cli> ack --upgrade`.`

- [x] **Step 1: Tests.** `TestStopUnitDirDeleted` переписать (HS-3): удалён документированный `svc/auth` → Stop называет родителя/`_root` строкой «Unit list changed», orphan виден в этом же ходе. `TestStopOldMissingUnitNoFalseCodeChanged` (HS-4): юнит без доков существовал до хода → нет «code changed in _root». `TestStopRecordsOnlyThisStopDocs` (P-M2): второй Stop без изменений после правки дока не вызывает `RefreshIndex` (счётчик через тестовый хук). `TestSessionStartClassifyStatuses` — табличный тест чистой `classifyStatuses` (Q-M1). `TestSessionStartCLIWithPercent` (Q-L1): корень плагина `…/100%d…` → текст без `%!d`. `TestHooksSkipEmptySessionID` (Q-L10). `TestSessionStartOldSchemeLine`. `TestSessionStartDiscoveryReset` (A-L8). Починить `TestActive` и `TestSessionStartRefusedMarker` через `testutil.Symlink` (T-H1); `go test -v` не показывает их SKIP. `TestStopCapsLists`, `TestSessionNoticeCaps` (T-L3). `TestStopCorruptJournalOnce` (T-L4). `TestStopCacheSaveErrorLogged` (T-L6). `concurrent_test.go`: `TestConcurrentStopHooks` — helper-process (T-M4, `raw/3A-testing.md`). `BenchmarkStopNoChange`.
- [x] **Step 2: Run** `go test ./internal/hooks/` — FAIL.
- [x] **Step 3: Implement.** Новизна юнита — только по снимку начала хода (нет `context/<u>.md` и файлов юнита в снимке); исчезнувший юнит — `context/<u>.md` в снимке есть, `IsUnit(u)` ложно → родитель и `_root` в pending. `recordTurn` возвращает кандидатов на auto-ack только из диффа этого Stop. `SessionStart` — оркестрация шагов по `raw/1A-code-quality.md` M1. `cli` передаётся аргументом `Sprintf`. Общий `loadJournal(root, id, hook)`. Пустой `session_id` — `logx` и пустой ответ. `cmdArg` для имени, начинающегося с `-`, даёт `./name` (на случай ослабления `SafeName`).
- [x] **Step 4: Run** `go test ./internal/hooks/` — PASS, без неожиданных SKIP. **Step 5:** док.

### Task T18: CLI — коды выхода, JSON, корень, вывод ошибок, e2e

**Files:** Modify `cmd/auto-context/main.go`, `cmd/auto-context/changes.go`; Test `cmd/auto-context/main_test.go`; Create `cmd/auto-context/e2e_test.go`; Docs `context/cmd/auto-context.md`, `cmd/auto-context/CLAUDE.md`.

**Interfaces:** Consumes всё из T5–T17. Produces CLI по Global Constraints: коды 0/1/2/3, `status [--stale] [--exit-code] [--json]`, `units [--json]`, `ack --upgrade`, `version [-v]` (`-v`: версия, `vcs.revision`, версия Go, GOOS/GOARCH, `cache 3`, `snapshot 1`, `journal 1`, `hash v2`).

- [x] **Step 1: Tests.** `TestStatusExitCodes` (stale → 1; битый конфиг, ошибка открытия, неактивный проект → 3; usage → 2). `TestAckFailureExit3`. `TestStatusJSON`, `TestUnitsJSON` — схема и `format_version`. `TestUpgradeFromV1Project` (Review Focus 1): фикстура с доками в старой схеме и неотслеживаемыми файлами → `status --exit-code` = 0 (ноты `old hash scheme`), после `ack --upgrade` хэши начинаются с `v2:`. `TestOutputsNeverQuoteHostileText` (S-2/S-3; таблица `raw/3A-testing.md` п.4): `status`, `ack`, `ack .`, `units`, `changes`, SessionStart, Stop — нет `IGNORE`, `\x1b`, U+202E. `TestStatusLeavesNoUnignoredFiles` (CL-3). `TestCLIRootPrefersProjectDir` (CL-7): `CLAUDE_PROJECT_DIR=/p`, cwd `/p/nested-repo` → корень `/p`. `TestHookRootCodexIgnoresClaudeProjectDir` (HS-6). `TestHookEventMismatchLogged` (BP-L10). `TestExcludeBrokenConfig` (Q-L9) → `ErrConfigBroken`, код 3. `TestUnitsDoesNotLoadCache` (P-L4). `TestVersionVerbose` (BP-L6). `e2e_test.go`: `TestE2EHooksThroughShim` (T-M6) — сборка бинаря в каталог плагина с пробелом, все команды из `hooks/hooks.json` и `hooks/codex.json` через `sh -c`, exit 0, ответ пуст или валидный JSON; stdin 2 MiB → exit 0 и запись `input larger than` в логе.
- [x] **Step 2: Run** `go test ./cmd/auto-context/` — FAIL.
- [x] **Step 3: Implement.** Таблица `hookTable map[string]hookSpec` и `parseHookArgs` (Q-L6); `runHook` вызывает `hooks.CheckEvent` и заполняет `Env.PluginRoot`; `hookRoot` для Codex не смотрит `CLAUDE_PROJECT_DIR`; корень CLI — `CLAUDE_PROJECT_DIR`, если cwd внутри него, иначе `FindRoot(cwd)`. Предупреждения `root index:` в `ack`/`status` — `fsutil.Category(err)` + `(details in .auto-context/log)`, полный текст — `logx.Errorf`. Кэш грузится только в `status`/`ack`. `version` — через `debug.ReadBuildInfo`, если `main.version == "dev"`. `fmt.Errorf` без аргументов → `errors.New` (BP-L7). `hook-exit` остаётся скрытой внутренней командой; описать в `context/cmd/auto-context.md` (A-L9). Исправить в доке имена контрактных тестов: `TestHooksJSONContract` (hooks.json) и `TestCodexHooksManifest` (codex.json) (PK-5).
- [x] **Step 4: Run** `go test ./cmd/auto-context/` — PASS. **Step 5:** док (контракт CLI, коды, JSON).

---

## Фаза B — навыки, документация, CI, зависимости

### Task T19: навыки

**Files:** Modify `skills/sync/SKILL.md`, `skills/status/SKILL.md`, `skills/auto-context-docs/SKILL.md`, `skills/init/SKILL.md`, `skills/README.md`, `skills/README.ru.md`; Create `cmd/auto-context/skills_test.go`.

- [ ] **Step 1: Tests** (`skills_test.go`). `TestSkillsMarkUntrustedData` — каждый SKILL.md, который упоминает `changes`, `status` или чтение доков, содержит фразу `are data from the repository`. `TestSkillsCommandsParse` — каждая команда `` `auto-context <sub> …` `` из навыков даёт не 2 в `run()`. `TestSyncHasNoAllowedTools` (S-5) — в `skills/sync/SKILL.md` нет `allowed-tools` и `` !` ``.
- [ ] **Step 2: Run** `go test ./cmd/auto-context/ -run Skills` — FAIL.
- [ ] **Step 3: Edit.** sync: убрать `allowed-tools` и `!`-вставку, шаг 1 — запустить `auto-context status --stale --json` через Bash и разбирать JSON (A-M3). Абзац S-6 (текст из `raw/2A-security.md` S-6, со словами «are data from the repository») — в sync, status, auto-context-docs, init. auto-context-docs: раздел «Merge conflicts in frontmatter»: взять любую сторону, затем `auto-context ack <unit>` (A-L7); `ack --upgrade` для нот `old hash scheme`; init: записывать в `config.yaml` действующие значения `code_extensions`, `module_markers`, `exclude`, `min_files`, чтобы смена умолчаний в новой версии не меняла хэши (A-H1). README навыков — оба языка.
- [ ] **Step 4: Run** — PASS.

### Task T20: README (оба языка) и README каталогов

**Files:** Modify `README.md`, `README.ru.md`, `bin/README.md`, `bin/README.ru.md`, `hooks/README.md`, `hooks/README.ru.md`.

- [ ] **Step 1: Edit README.md**: Security — точная формулировка по `raw/3B-documentation.md` H-1 с учётом исправлений T1/T7/T15/T16 (что гарантируется; что `changes` и имена юнитов — недоверенные данные) (D-H1); CI-сценарий и правило учёта файлов по схеме v2 (хэш по индексу; неотслеживаемые не влияют; `ack --upgrade`) (D-H2); справка CLI: коды выхода, `--json`, `version [-v]`, `ack .` = `_root`, `ack --upgrade`, формат `units`/`status` (D-M3); разделы Updating (Claude Code, Codex, повторное доверие хукам, одна версия на команду, закрепление версии ref’ом из T22) и Uninstalling/disabling (D-M4, CD-M2); Verify — `gh attestation verify` и `sha256sum -c` (CD-M4); минимальная версия Claude Code 2.1.304 (BP-L12); D-L1…D-L7, D-L11 по тексту отчёта; большие репозитории — «сотни тысяч файлов», лимит 800 000, совет `core.untrackedCache` (P-L7, D-L5); Troubleshooting — FIFO/не-regular `.gitignore` (T-H2).
- [ ] **Step 2: Edit README.ru.md** — то же по смыслу, та же структура и те же литералы в обратных кавычках.
- [ ] **Step 3:** `hooks/README*` — одна строка про оба контрактных теста (D-L9); `bin/README*` — PATH шима и канонический HOME.
- [ ] **Step 4: Verify** `diff <(grep -o '`[^`]*`' README.md | sort) <(grep -o '`[^`]*`' README.ru.md | sort)` — пусто.

### Task T21: документы проекта, CHANGELOG, SECURITY, runbook

**Files:** Modify `context/_root.md`, `CLAUDE.md`, `.gitignore`, `docs/superpowers/specs/2026-10-19-auto-context-design.md`; Create `CLAUDE.local.md`, `CHANGELOG.md`, `CHANGELOG.ru.md`, `SECURITY.md`, `docs/RELEASING.md`, `.github/ISSUE_TEMPLATE/bug.yml`, `.github/ISSUE_TEMPLATE/config.yml`.

- [ ] **Step 1:** `context/_root.md` — строка зависимостей (`:64`) — `go.yaml.in/yaml/v3`; модель угроз: архив с `.git/` и враждебный `.git/config` (что закрыто в gitx), инвариант вывода, схема хэша v2, коды выхода, шим и `FindRoot` — два алгоритма поиска корня с намеренным расхождением (A-L4) (D-M1).
- [ ] **Step 2:** раздел «MCP Tools: code-review-graph» из `CLAUDE.md` перенести в `CLAUDE.local.md`; `CLAUDE.local.md` и `.github/tools/node_modules/` добавить в `.gitignore` (D-M6, для T22).
- [ ] **Step 3:** спецификация — раздел «Поправки 2026-10-23»: §4, §7, §10.2, §13 по `raw/3B-documentation.md` L-8 и схеме v2 (D-L8).
- [ ] **Step 4:** `CHANGELOG.md`/`.ru.md` — `## v1.0.0` (по `raw/3B-documentation.md` M-5) и `## v2.0.0` с `### Breaking`: схема хэша v2 и `ack --upgrade`, коды выхода, `SafeName` без пробелов, `--json`, лимит 800 000 файлов, выборка для файлов > 32 MiB (D-M5).
- [ ] **Step 5:** `SECURITY.md` — поддерживаемая версия, канал (GitHub private advisory), сроки, процедура (CD-M5). `docs/RELEASING.md` — hotfix, «yanked», откат через `workflow_dispatch` из T22, закрепление по тегу `dist/vX.Y.Z` (CD-M2). Шаблон issue — поля `auto-context version -v`, ОС, агент и версия, вывод `auto-context status`, число ошибок в логе (CD-M5, CD-L6).

### Task T22: CI, релиз, Makefile, инструменты

**Files:** Modify `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `.github/scripts/check-manifests.sh`, `Makefile`; Create `.github/workflows/codeql.yml`, `.github/workflows/rollback.yml`, `.github/dependabot.yml`, `.github/CODEOWNERS`, `.github/scripts/check-skips.sh`, `.github/scripts/allowed-skips.txt`, `.github/tools/package.json`, `.github/tools/package-lock.json`, `tools/go.mod`, `tools/go.sum`.

- [ ] **Step 1: ci.yml** — убрать `paths-ignore` (CD-L2); `test`: `go test -race -shuffle=on ./...` на ubuntu, `go test ./...` на macOS/Windows, `git --version` в лог (T-L7); шаг `go test -json ./... | sh .github/scripts/check-skips.sh` против `allowed-skips.txt` (T-M5); задание `lint`: `gofmt -l` пусто, `go tool -modfile=tools/go.mod staticcheck ./...`, `go tool -modfile=tools/go.mod govulncheck ./...`, actionlint и shellcheck по SHA, `sh -n` шима под `dash` и `busybox sh` (BP-M4, CD-L3); `on.schedule` еженедельно для govulncheck и fuzz `-fuzztime=20s` по фаззерам (CD-M1, T-M5); `workflow_call.inputs.cache` (bool, по умолчанию true) → `setup-go cache` (CD-L5); claude CLI — `npm ci --ignore-scripts` в `.github/tools` (CD-L4).
- [ ] **Step 2: release.yml** — `concurrency: release-${{ github.ref }}` (PK-3); сборка — `make dist VERSION=$GITHUB_REF_NAME PLATFORMS=…` (CD-L7, BP-M3); второй прогон сборки linux/amd64 и сравнение sha256 (CD-L1); `smoke`-матрица (ubuntu, ubuntu-24.04-arm, macos-latest, macos-13, windows-latest): `bin/auto-context version` = тег, `hook session-start` на фикстуре — exit 0 и валидный JSON (CD-M3); валидация `plugin validate --strict` — в отдельном задании с `contents: read` (PK-2); `publish` с `environment: release`, `GH_TOKEN` только на шагах push/release (PK-2); attest — ещё и tar.gz и `checksums.txt`, `checksums.txt` в дерево release (CD-M4); `.gitattributes` в дерево (PK-1); тег `dist/vX.Y.Z` на коммит ветки release (CD-M2); Release создаётся черновиком до push ветки и публикуется после (CD-L8); `--latest=false`, если ветка не сдвинулась (PK-4); тело — раздел CHANGELOG тега через `--notes-file` (D-M5, CD-M6); пре-релизные теги двигают ветку `release-next` (CD-M3); тесты вызывают ci.yml с `cache: false`.
- [ ] **Step 3: rollback.yml** — `workflow_dispatch` с входом `to: dist/vX.Y.Z`: коммит дерева этого тега поверх ветки release с версией `<текущая>+1` патчем, без force-push (CD-M2).
- [ ] **Step 4:** `dependabot.yml` (gomod, `tools/`, github-actions, npm в `.github/tools`; actions — одной группой); `codeql.yml` (Go, по SHA); `CODEOWNERS` для `.github/`, `bin/`, `hooks/`, манифестов (CD-H1 п.7); `tools/go.mod` с `tool` staticcheck и govulncheck.
- [ ] **Step 5a:** `.auto-context/config.yaml` — в `exclude` добавить `tools/**` и `.github/**`: `tools/go.mod` и `.github/tools/package.json` — маркеры модуля и иначе стали бы юнитами без доков.
- [ ] **Step 5: Makefile** — `VERSION ?= $(shell git describe --tags --always --dirty)`; цели `build` (хост в `libexec/`), `lint`, `verify` (`go mod verify`, `check-manifests.sh`), `test` с `-race`; `dist` сначала чистит `libexec/` (BP-L11).
- [ ] **Step 6: Verify** `actionlint` (если есть), `make verify`, `sh -n bin/auto-context`; полный `make test` — гейт волны.

### Task T23: зависимости — yaml и toolchain

**Files:** Modify `go.mod`, `go.sum`, `internal/config/config.go`, `internal/frontmatter/frontmatter.go`, `cmd/auto-context/main_test.go` (только импорт yaml). Задача идёт отдельной волной W7: `go get`/`go mod tidy` не должны идти параллельно со сборкой других задач.

- [ ] **Step 1:** `go get go.yaml.in/yaml/v3@v3.0.5`, импорты `yaml "go.yaml.in/yaml/v3"` в `config.go`, `frontmatter.go` и `cmd/auto-context/main_test.go:22` (BP-M1), `go mod tidy` — `gopkg.in/yaml.v3` и `gopkg.in/check.v1` уходят из go.sum.
- [ ] **Step 2:** `go.mod`: `go 1.27.0` и `toolchain go1.27.3` (BP-M3, CD-L1).
- [ ] **Step 3: Verify** `go mod verify && go vet ./... && go test ./...` — PASS.

---

## Фаза C — рефакторинги (поведение не меняется)

Каждая задача фазы C не меняет ни одного golden-файла и ни одного ожидания существующих тестов, кроме сигнатур. Если тест требует поменять ожидание, задача остановлена и возвращена координатору.

### Task T24: `context.Context` как бюджет хука (BP-M2, A-L3, HS-7, P-M6 дедлайн)

**Files:** Modify `internal/gitx/**`, `internal/hashing/**`, `internal/units/**`, `internal/docstate/**`, `internal/project/**`, `internal/hooks/**`, `cmd/auto-context/**` (и их `context/…md`).

**Interfaces:** `gitx.Run(ctx context.Context, dir string, limit int, args ...string)`; `gitx.SetDeadline` удаляется; `project.Open(ctx, root)`; `hooks.Env.Ctx context.Context`; `hashing.Prewarm(ctx, …)` и `SourceHash` проверяют `ctx.Err()` в цикле; при исчерпании — частичный результат, `Note` `NoteHashError` с `Detail` `"the hook's time is up"`: в `docstate` проверить `errors.Is(err, context.DeadlineExceeded)` до `fsutil.Category` (fsutil в этой волне принадлежит T25). Задача удаляет обёртку `docstate.Ack`. CLI — `signal.NotifyContext(context.Background(), os.Interrupt)`.

- [ ] **Step 1: Tests.** `TestHookBudgetStopsHashing` — 1000 файлов, бюджет 50 мс → хук отвечает < 500 мс и пишет в лог `time is up`. `TestRunHonorsParentDeadline` в gitx.
- [ ] **Step 2–4:** FAIL → перевести вызовы (пакет за пакетом, снизу вверх) → `go test ./...` PASS.

### Task T25: `os.Root` для ввода-вывода в fsutil (BP-L4)

**Files:** Modify `internal/fsutil/**`, `context/internal/fsutil.md`.

- [ ] **Step 1: Tests.** `TestReadRegularFIFONonBlocking` (unix): подмена файла на FIFO между `Resolve` и чтением не блокирует (`O_NONBLOCK` + `f.Stat().Mode().IsRegular()`). Существующие тесты симлинк-атак — без изменений.
- [ ] **Step 2–4:** `Resolve` остаётся политикой; чтение и `CreateTemp`/`Rename` идут через кэшированный `os.OpenRoot(realRoot)` по root-relative пути; запись через симлинк по-прежнему пишет в цель (поведение `CLAUDE.md → AGENTS.md` сохранено, тест есть). PASS.

### Task T26: неизменяемое дерево, `Stat` в листе, меньше копий, `FindRoot` в project (Q-M2, A-L5, P-M5, A-L4)

**Files:** Modify `internal/units/**`, `internal/session/**`, `internal/config/**`, `internal/project/**`, `internal/hooks/**`, `cmd/auto-context/**`; Create `internal/filestat/filestat.go` (+ тест, `context/internal/filestat.md`, `internal/filestat/CLAUDE.md`).

**Interfaces:** `filestat.Stat{MtimeNs, Size int64}` (вместо `units.Stat`); `units.Tree` неизменяем после `Discover`, изменяемое состояние — в `matcher` внутри `Discover`; `Classify` получает свой бюджет glob на вызов и не пишет предупреждений; `func (t *Tree) TakeStats() map[string]filestat.Stat` (без клона, дерево после вызова не используется для `Stats`); `project.FindRoot(cwd string) string` (из `config`, вызовы перенесены); `session.Snapshot` сохраняется отсортированным, `Diff` — слиянием двух отсортированных потоков.

- [ ] **Step 1: Tests.** `TestClassifyDoesNotMutate` (предупреждения и бюджет до и после 10 000 `Classify` равны); `TestTreeConcurrentReads` под `-race`; `TestStopMemory` — Stop на 50k файлах, `runtime.ReadMemStats` HeapInuse < 150 MiB.
- [ ] **Step 2–4:** FAIL → реализовать → `go test -race ./...` PASS.

### Task T27: кэш хэшей — бинарный формат, версии рядом, слияние под lock (P-M4, P-L6, A-M4)

**Files:** Modify `internal/hashing/**`, `internal/logx/**`; Create `internal/filelock/**` (+ `context/internal/filelock.md`).

**Interfaces:** `filelock.Lock(path string) (unlock func(), ok bool, err error)` — перенос `logx/lock_*.go`; logx использует его. Кэш — `.auto-context/cache.v4.bin`: заголовок `acache 4\n`, записи `uvarint(len) path | varint mtime | uvarint size | [32]byte sum | flags` по `raw/2B-performance.md` P-M4; старые `cache.json` разных версий не трогаются и не вытесняют друг друга (A-M4). `Save` под `filelock` перечитывает файл и сливает записи, совпадающие по mtime/size с текущим stat (P-L6). API `LoadCache/Save/Prewarm/SourceHash` прежний. Исключение из правила фазы C: T27 меняет в тестах `internal/hooks/hooks_test.go`, `session_problems_test.go`, `internal/project/ops_test.go`, `project_test.go` только имя файла кэша (`cache.json` → `cache.v4.bin`) и способ подложить битый кэш.

- [ ] **Step 1: Tests.** `TestCacheRoundTrip`, `TestCacheMergeConcurrentSave` (два процесса через helper-process — записи обоих сохранены), `TestCacheIgnoresOtherVersions`, `BenchmarkLoadSave200k` (< 50 мс на загрузку).
- [ ] **Step 2–4:** FAIL → реализовать → PASS.

### Task T28: пакет `internal/turn` — правила хода (A-M1)

**Files:** Create `internal/turn/**` (+ `context/internal/turn.md`); Modify `internal/hooks/**`, `internal/project/**`, `cmd/auto-context/main.go`.

**Interfaces:** `turn.Evaluate(in turn.Input) turn.Outcome`, где `Input{Tree view, Before, Cur session.Snapshot, Journal session.Journal, TurnStart int64, DirStat func(string) (int64, bool, error), Doc func(string) docstate.DocKind}`, `Outcome{AutoAck, Pending, NewUnits, ChangedLists []string; Journal session.Journal}`; `recordTurn`, `deletionTimes`, `turnNewUnits`, `pendingUnits`, `withParents` переезжают из `hooks/stop.go` без изменений логики. `project` перестаёт отдавать `*units.Tree`: узкие методы `Units()`, `IsUnit(u)`, `Classify(rel)`, `Stats()`; вызов `p.Tree().Units()` в `cmd/auto-context/main.go` (`runUnits`) переходит на `p.Units()`.

- [ ] **Step 1:** перенести табличные тесты Stop-логики в `internal/turn` (на чистых входах, без JSON). **Step 2–4:** перенос → `go test ./...` PASS, golden-файлы хуков не изменились.

### Task T29: размер ветки release — проверка и решение (CD-M7)

**Files:** Modify `docs/RELEASING.md`, `.github/workflows/release.yml`.

- [ ] **Step 1:** выяснить, клонируют ли Claude Code (`/plugin marketplace add`, обновление) и Codex ветку shallow: установить плагин во временный `HOME` и посмотреть `git rev-parse --is-shallow-repository` и размер `.git` в кэше плагина. Записать результат и дату в `docs/RELEASING.md`.
- [ ] **Step 2:** если клон не shallow — добавить в release.yml замер размера клона ветки и предупреждение (`::warning::`) при > 200 MiB; решение «коммиты release без родителя (force-push только ботом через bypass ruleset)» описать в RELEASING.md как следующий шаг владельца. Если shallow — записать, что действий не нужно.

### Task T30: модель проблем `project.Diagnose` (A-M2)

**Files:** Modify `internal/project/**`, `internal/hooks/**`, `cmd/auto-context/**`.

**Interfaces:** `type Problem struct{Code ProblemCode; Severity Severity; Unit string; Err error}`; `func (p *Project) Diagnose() []Problem` — все проверки здоровья (конфиг, `DiscoveryReset`, state-dir, кэш, check-ignore, предупреждения discover, индекс, лог); рендереры: `status` (текст и JSON), SessionStart, Stop — таблица `Code → текст`; `errCategory` (hooks), `errKind` (units) и `fsutil.Category` сводятся к одному месту (Q-L3).

- [ ] **Step 1:** тест `TestDiagnoseMatchesStatusAndSessionStart` — для набора поломанных фикстур множество кодов проблем, которое видят `status --json` и SessionStart, совпадает. **Step 2–4:** перенос → PASS, golden-файлы не изменились.

### Task T31: модернизация и staticcheck (BP-L1..L3, BP-L7 ST1020)

**Files:** Modify `internal/**/*.go`, `cmd/**/*.go`.

- [ ] **Step 1:** `go fix ./...` (или закреплённый `modernize -fix`): `errors.AsType`, `wg.Go`, `for range n`, `b.Loop`; `slices.Equal` вместо `reflect.DeepEqual` только там, где nil и пустой срез не различаются.
- [ ] **Step 2: Verify** `go vet ./... && go tool -modfile=tools/go.mod staticcheck ./... && go test -race ./...` — чисто.

### Task T32: документы после рефакторингов

**Files:** Modify `context/_root.md`.

- [ ] **Step 1:** список пакетов-кирпичей — `filestat` (T26), `filelock` (T27), `turn` (T28), `Diagnose` (T30); файл кэша `cache.v4.bin` и слияние под lock вместо «побеждает последняя запись» (T27); `FindRoot` в `project` (T26).

---

## Порядок и параллельность

```paraplane
version: 1
gate: "go vet ./... && go test ./... && sh -n bin/auto-context"
waves:
  - id: W1
    tasks:
      - id: T1
        title: "gitx: transports, signatures, process group, topology"
        owns: ["internal/gitx/**", context/internal/gitx.md]
        verify: "go test ./internal/gitx/"
      - id: T2
        title: "textx: bidi, BOM, EOL"
        owns: ["internal/textx/**", context/internal/textx.md]
        verify: "go test ./internal/textx/"
      - id: T3
        title: "fsutil: error categories, reads"
        owns: ["internal/fsutil/**", context/internal/fsutil.md]
        verify: "go test ./internal/fsutil/"
      - id: T4
        title: "testutil: ceiling, symlinks, fake git"
        owns: ["internal/testutil/**", context/internal/testutil.md]
        verify: "go test ./internal/testutil/"
      - id: T6
        title: "config: single parse, safe reasons, anchors, HOME"
        owns: ["internal/config/**", context/internal/config.md]
        verify: "go test ./internal/config/"
      - id: T7
        title: "units part 1: names, ignore forms, nested repos"
        owns: ["internal/units/**"]
        verify: "go test ./internal/units/"
  - id: W2
    tasks:
      - id: T8
        title: "hooks input: strict command, event name, plugin root"
        owns: [internal/hooks/hooks.go, internal/hooks/agent.go, internal/hooks/pre_tool_use.go, internal/hooks/permission_request.go, internal/hooks/agent_test.go, internal/hooks/pre_tool_use_test.go, internal/hooks/permission_request_test.go, internal/hooks/fuzz_test.go]
        verify: "go test ./internal/hooks/"
      - id: T9
        title: "session: journal version, ids, snapshot age"
        owns: ["internal/session/**", context/internal/session.md]
        verify: "go test ./internal/session/"
      - id: T11
        title: "hashing: large files, dirty, saving"
        owns: ["internal/hashing/**", context/internal/hashing.md]
        verify: "go test ./internal/hashing/"
      - id: T12
        title: "rootindex: linear fences, sanitize"
        owns: ["internal/rootindex/**", context/internal/rootindex.md]
        depends: [T2]
        verify: "go test ./internal/rootindex/"
      - id: T13
        title: "frontmatter: keep keys, shared BOM/EOL"
        owns: ["internal/frontmatter/**", context/internal/frontmatter.md]
        depends: [T2]
        verify: "go test ./internal/frontmatter/"
      - id: T14
        title: "units part 2: index view, topology, limits"
        owns: ["internal/units/**", context/internal/units.md]
        reads: [internal/gitx/topology.go, internal/testutil/testutil.go]
        depends: [T1, T4, T7]
        verify: "go test ./internal/units/"
  - id: W3
    tasks:
      - id: T15
        title: "docstate: hash scheme v2, categories"
        owns: ["internal/docstate/**", context/internal/docstate.md]
        reads: [internal/units/units.go, internal/fsutil/fsutil.go]
        depends: [T3, T11, T13, T14]
        verify: "go test ./internal/docstate/"
      - id: T5
        title: "shim: PATH, HOME, line endings"
        owns: [bin/auto-context, .gitattributes, cmd/auto-context/shim_test.go]
        verify: "sh -n bin/auto-context && go test ./cmd/auto-context/ -run Shim"
      - id: T10
        title: "logx: lock race, flock, state dir"
        owns: ["internal/logx/**", context/internal/logx.md]
        verify: "go test ./internal/logx/"
  - id: W4
    tasks:
      - id: T16
        title: "project: state dir, exclude, upgrade, cheap index, changes"
        owns: ["internal/project/**", context/internal/project.md]
        reads: [internal/docstate/docstate.go, internal/config/config.go, internal/gitx/gitx.go]
        depends: [T1, T3, T6, T10, T15]
        verify: "go test ./internal/project/"
  - id: W5
    tasks:
      - id: T17
        title: "hooks: unit set in a turn, SessionStart steps, activation tests"
        owns: ["internal/hooks/**", context/internal/hooks.md]
        reads: [internal/project/project.go, internal/session/session.go]
        depends: [T4, T8, T9, T12, T16]
        verify: "go test ./internal/hooks/"
  - id: W6
    tasks:
      - id: T18
        title: "CLI: exit codes, JSON, root, errors, e2e"
        owns: [cmd/auto-context/main.go, cmd/auto-context/changes.go, cmd/auto-context/main_test.go, cmd/auto-context/e2e_test.go, context/cmd/auto-context.md, cmd/auto-context/CLAUDE.md]
        reads: [hooks/hooks.json, hooks/codex.json, bin/auto-context]
        depends: [T5, T6, T17]
        verify: "go test ./cmd/auto-context/"
  - id: W7
    tasks:
      - id: T23
        title: "Dependencies: yaml module and toolchain"
        owns: [go.mod, go.sum, internal/config/config.go, internal/frontmatter/frontmatter.go, cmd/auto-context/main_test.go]
        depends: [T6, T13, T18]
        verify: "go mod verify && go test ./..."
  - id: W8
    tasks:
      - id: T19
        title: "Skills"
        owns: ["skills/**", cmd/auto-context/skills_test.go]
        reads: [cmd/auto-context/main.go]
        depends: [T18, T23]
        verify: "go test ./cmd/auto-context/ -run Skills"
      - id: T20
        title: "README files"
        owns: [README.md, README.ru.md, bin/README.md, bin/README.ru.md, hooks/README.md, hooks/README.ru.md]
        depends: [T18, T23]
        verify: "test -s README.ru.md"
      - id: T21
        title: "Project docs, CHANGELOG, SECURITY, runbook"
        owns: [context/_root.md, CLAUDE.md, CLAUDE.local.md, .gitignore, "docs/superpowers/specs/**", CHANGELOG.md, CHANGELOG.ru.md, SECURITY.md, docs/RELEASING.md, ".github/ISSUE_TEMPLATE/**"]
        depends: [T18, T23]
        verify: "test -s CHANGELOG.md && test -s SECURITY.md"
      - id: T22
        title: "CI, release, Makefile, tools"
        owns: [".github/workflows/**", ".github/scripts/**", .github/dependabot.yml, .github/CODEOWNERS, ".github/tools/**", Makefile, "tools/**", .auto-context/config.yaml]
        depends: [T5, T18, T23]
        verify: "make verify && sh -n bin/auto-context"
  - id: W9
    tasks:
      - id: T24
        title: "context.Context as the hook budget"
        owns: ["internal/gitx/**", "internal/hashing/**", "internal/units/**", "internal/docstate/**", "internal/project/**", "internal/hooks/**", "cmd/auto-context/**", context/internal/gitx.md, context/internal/hashing.md, context/internal/units.md, context/internal/docstate.md, context/internal/project.md, context/internal/hooks.md, context/cmd/auto-context.md]
        depends: [T1, T5, T11, T14, T15, T16, T17, T18, T19, T23]
        verify: "go test ./internal/gitx/ ./internal/hashing/ ./internal/units/ ./internal/docstate/ ./internal/project/ ./internal/hooks/ ./cmd/auto-context/"
      - id: T25
        title: "fsutil: os.Root for I/O"
        owns: ["internal/fsutil/**", context/internal/fsutil.md]
        depends: [T3]
        verify: "go test ./internal/fsutil/"
  - id: W10
    tasks:
      - id: T26
        title: "Immutable tree, filestat, fewer copies, FindRoot"
        owns: ["internal/units/**", "internal/session/**", "internal/config/**", "internal/project/**", "internal/hooks/**", "cmd/auto-context/**", "internal/filestat/**", context/internal/units.md, context/internal/session.md, context/internal/config.md, context/internal/project.md, context/internal/filestat.md]
        depends: [T9, T23, T24, T25]
        verify: "go test -race ./..."
  - id: W11
    tasks:
      - id: T28
        title: "internal/turn: the rules of a turn"
        owns: ["internal/turn/**", "internal/hooks/**", "internal/project/**", cmd/auto-context/main.go, context/internal/turn.md, context/internal/hooks.md, context/internal/project.md]
        depends: [T26]
        verify: "go test ./internal/turn/ ./internal/hooks/ ./internal/project/ ./cmd/auto-context/"
      - id: T29
        title: "Release branch size: measure and decide"
        owns: [docs/RELEASING.md, .github/workflows/release.yml]
        depends: [T21, T22]
        verify: "test -s docs/RELEASING.md"
  - id: W12
    tasks:
      - id: T27
        title: "Hash cache format, versions, merge under lock"
        owns: ["internal/hashing/**", "internal/logx/**", "internal/filelock/**", internal/hooks/hooks_test.go, internal/hooks/session_problems_test.go, internal/project/ops_test.go, internal/project/project_test.go, context/internal/hashing.md, context/internal/logx.md, context/internal/filelock.md]
        depends: [T10, T24, T28]
        verify: "go test ./internal/hashing/ ./internal/logx/ ./internal/filelock/ ./internal/hooks/ ./internal/project/"
  - id: W13
    tasks:
      - id: T30
        title: "project.Diagnose problem model"
        owns: ["internal/project/**", "internal/hooks/**", "cmd/auto-context/**", context/internal/project.md, context/internal/hooks.md, context/cmd/auto-context.md]
        depends: [T27, T28]
        verify: "go test ./internal/project/ ./internal/hooks/ ./cmd/auto-context/"
  - id: W14
    tasks:
      - id: T31
        title: "Modernize and staticcheck"
        owns: ["internal/**/*.go", "cmd/**/*.go"]
        depends: [T2, T7, T8, T12, T13, T22, T23, T25, T27, T30]
        verify: "go vet ./... && go test -race ./..."
      - id: T32
        title: "Docs after the refactors"
        owns: [context/_root.md]
        depends: [T21, T26, T27, T28, T30]
        verify: "test -s context/_root.md"
```

---

## Чек-лист настроек GitHub (выполняет владелец, не задачи плана)

Репозиторий `Comprise/auto-context`. Пункт 3 нужен до слияния T22: `publish` ссылается на окружение `release`.

1. **Rulesets** (CD-H1): `gh api -X POST repos/Comprise/auto-context/rulesets --input <file>` — три набора:
   - `main`: `pull_request` обязателен; `required_status_checks` — `test (ubuntu-latest)`, `test (macos-latest)`, `test (windows-latest)`, `plugin`, `lint`; `non_fast_forward`, `deletion`.
   - `release`: `non_fast_forward`, `deletion`, `update` с `bypass_actors` — только GitHub Actions (приложение `github-actions`).
   - теги `refs/tags/v*` и `refs/tags/dist/*`: `deletion`, `update`, `non_fast_forward`; создание — maintainers.
2. **Actions** (CD-H1 п.5, CD-L9): `gh api -X PUT repos/Comprise/auto-context/actions/permissions -F enabled=true -f allowed_actions=selected -F sha_pinning_required=true`; `gh api -X PUT repos/Comprise/auto-context/actions/permissions/selected-actions -F github_owned_allowed=true -F verified_allowed=true`.
3. **Окружение `release`** с обязательным ревьюером-владельцем (CD-H1 п.4): `gh api -X PUT repos/Comprise/auto-context/environments/release --input <file>` (`reviewers: [{type: User, id: <ваш id из gh api user --jq .id>}]`).
4. **Безопасность** (CD-M1, CD-M5): `gh api -X PUT repos/Comprise/auto-context/private-vulnerability-reporting`; `gh api -X PUT repos/Comprise/auto-context/vulnerability-alerts`; `gh api -X PUT repos/Comprise/auto-context/automated-security-fixes`.
5. **Immutable Releases** (CD-H1 п.6) — Settings → General → Releases (или API, если доступен для аккаунта).
6. **Висячие Releases** (CD-M6): `gh release delete v0.1.0-rc1 --yes`, то же для `v0.1.0`, `v0.2.0`, `v0.3.0` (тегов у них нет; `--cleanup-tag` не нужен). Перед удалением — `gh release view <tag>`, чтобы убедиться, что это они.
