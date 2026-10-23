# Phase 4: Best Practices & Standards

Полные отчёты: `raw/4A-best-practices.md`, `raw/4B-cicd.md`.

## Framework & Language Findings

Счётчики (4A): Critical 0 · High 0 · Medium 4 · Low 12 · Info 6. `go vet`, nilness, govulncheck, `go mod tidy -diff` — чисто; staticcheck `-checks all` — 1 (ST1020 `internal/session/session.go:249`); modernize — 13 мелких предложений (в основном тесты). Код уже современный (slices/maps, SplitSeq, min/max, range n, atomic.Int64, %w). Протокол хуков сверен с актуальной документацией Claude Code и Codex — соответствует.

### Medium
- **BP-M1** `gopkg.in/yaml.v3` архивирован → `go.yaml.in/yaml/v3 v3.0.5` (тот же API и `yaml.Node`; в копии все тесты зелёные). `goccy/go-yaml` не рекомендуется — другой AST.
- **BP-M2** Бюджет хука — глобальный `gitx.SetDeadline`; передавать `context.Context` от `runHook` через `project`/`gitx` до хэширования и обхода (закрывает и «дедлайн только для git», P-M6).
- **BP-M3** Нет директивы `toolchain` (`go 1.27`) — релизы невоспроизводимы; сборка продублирована в Makefile и release.yml.
- **BP-M4** В CI нет staticcheck, govulncheck, modernize; закрепить через `tool` в `tools/go.mod`, govulncheck по расписанию.

### Low
`errors.AsType`, `wg.Go`, `b.Loop`; `os.Root` — только дополнение к `fsutil.Resolve` (TOCTOU), не замена; `filepath.IsLocal`; версия через `debug.ReadBuildInfo`; `errors.New`, sentinel `ErrUnknownUnit`; Codex `PLUGIN_ROOT`; `SyscallConn` вместо `f.Fd()` во flock; сверка `hook_event_name` с argv; Makefile без build/lint/-race, `dist` не чистит `libexec`; не объявлена минимальная версия Claude Code.

## CI/CD & DevOps Findings

Счётчики (4B): Critical 0 · High 1 · Medium 7 · Low 9 (+ из `raw/area-packaging.md`: 4 Low). Настройки репо смотрелись через `gh api` (только чтение).

### High
- **CD-H1** Канал доставки не защищён: нет branch protection/rulesets у `main` и `release`, нет правил для тегов `v*`, нет environment с апрувом у publish, обязательное SHA-закрепление actions выключено. Push в `release` (например, со скомпрометированным ключом) доставит бинарники всем пользователям в обход CI и provenance; ветку `release` уже переписывали вручную.

### Medium
- **CD-M1** Нет govulncheck, CodeQL, `dependabot.yml`; Dependabot security updates выключены; нет CI по расписанию.
- **CD-M2** Откат релиза невозможен: `release` только вперёд, на её коммитах нет тегов, нет runbook; пользователь не может закрепить версию.
- **CD-M3** Кросс-собранные бинарники не запускаются до публикации, нет smoke-теста через шим; нет канала пре-релизов.
- **CD-M4** Provenance не проверяема пользователем: аттестованы только `libexec/*`, не tar.gz и `checksums.txt`; в README нет `gh attestation verify`.
- **CD-M5** Нет `SECURITY.md`, private vulnerability reporting выключен при открытых High; нет шаблонов issue.
- **CD-M6** История релизов рассогласована: Releases v0.1.0-rc1…v0.3.0 без тегов на remote; коммиты `release v0.0.1…v0.0.3` без тегов/Releases; Immutable Releases выключены.
- **CD-M7** (предположительно) ветка `release` копит ~24 МБ бинарников на релиз — не-shallow клон маркетплейса растёт линейно.

### Low
Нет `toolchain`; `paths-ignore` несовместим с обязательными проверками и пропускает `README.ru.md`; нет линт-гейтов (gofmt, staticcheck, actionlint, shellcheck); `npx claude-code` без lock-файла; кэш setup-go от main/PR в релизе; мало диагностики (`version` без коммита, нет doctor); дубль сборки в Makefile/release; публикация не атомарна; Actions разрешают любые actions. Из ревью упаковки: нет `.gitattributes eol=lf` для шима (CRLF → выход 2), npm-код в задании с `contents: write`/`id-token: write`, `concurrency` отменяет промежуточные релизы, старый тег становится Latest.
