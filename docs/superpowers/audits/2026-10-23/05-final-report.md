# Comprehensive Code Review Report

## Review Target

Весь проект auto-context (коммит af0cae8, ветка main): Go-плагин Claude Code и Codex CLI — `cmd/`, 14 пакетов `internal/`, тесты, шим `bin/`, `hooks/`, `skills/`, манифесты, `.github/`, Makefile, README и `context/`. Модель угроз: враждебный клон (в т.ч. архив с `.git/`), хуки срабатывают автоматически, часть команд авто-одобряется.

## Executive Summary

Код зрелый: инварианты из `context/_root.md` соблюдаются, покрытие 91%, `go vet`/staticcheck/govulncheck чисты, протокол хуков соответствует актуальным схемам, находки прошлого аудита закрыты добросовестно. Главные риски — периметр доверия вокруг git и текста из репозитория: локальный `.git/config` враждебного архива может исполнить код через ленивую докачку объектов (в каждом хуке) и через `gpg.program` в авто-одобряемом `changes`, а имена юнитов, пути и stderr git попадают в контекст Claude вопреки заявленному инварианту. Второй блок — канал релиза без branch protection и закоммиченный `source_hash`, зависящий от неотслеживаемых файлов (ломает рекомендованный CI-сценарий).

## Findings by Priority

### Critical Issues (P0 — Must Fix Immediately)

Нет.

### High Priority (P1 — Fix Before Next Release)

Безопасность (исполнение кода и инъекция в контекст):
1. **S-1** Исполнение команды из `.git/config` через partial clone + lazy fetch (`uploadpack`/`sshCommand`/`ext::`/`gitProxy`) в каждом хуке и в `status`/`units`/`changes` — `internal/gitx/gitx.go:116-118`, `internal/units/gitfiles.go:29`. Воспроизведено. *(Фаза 2)*
2. **gpg.program** `log.showSignature` + `gpg.program` исполняются в авто-одобряемом `auto-context changes` — `internal/project/changes.go:86`. Воспроизведено. *(Фаза 1, ревью CLI)*
3. **D-H1** README Security обещает защиту, которой нет (последствие 1–2 и S-2/S-3) — `README.md:196-200`. *(Фаза 3)*

Корректность и устойчивость:
4. **A-H1 / D-H2** `source_hash` зависит от неотслеживаемых файлов, локальных ignore, режима обнаружения и `Default()` бинарника; схема не версионирована → ложный stale в CI на чистом клоне; README рекомендует именно этот сценарий — `README.md:153,180`. Воспроизведено. *(Фазы 1, 3)*
5. **Q-H1** `rootindex.fenced` квадратичен: корневой CLAUDE.md 2 МБ с незакрытыми fence ≈ 17 мин, хуки упираются в таймаут — `internal/rootindex/rootindex.go:102-124,203`. Воспроизведено. *(Фаза 1)*
6. **Q-H2** `rootindex.sanitize` квадратичен по summary — `rootindex.go:69-75`. Воспроизведено. *(Фаза 1)*

Тесты:
7. **T-H1** Тесты `ActivationRefused` молча SKIP на всех ОС (симлинк в несуществующем `.auto-context/`). *(Фаза 3)*
8. **T-H2** FIFO вместо `.gitignore` → каждый хук висит 20 с, ветка таймаута `ls-files` не покрыта — `internal/units/gitfiles.go:41`. Воспроизведено. *(Фаза 3)*

Релиз:
9. **CD-H1** Нет branch protection/rulesets для `main`, `release`, тегов `v*` и environment с апрувом — push в `release` доставляет бинарники пользователям в обход CI. *(Фаза 4)*

### Medium Priority (P2 — Plan for Next Sprint)

Безопасность/устойчивость:
- Шим ищет `uname`/`tr`/`dirname`/`sed` через PATH: относительный элемент PATH → код репо на каждом хуке — `bin/auto-context:3-14`. Воспроизведено.
- Путь цели симлинка context-дока (многострочный, с инструкциями) в выводе `status`/`ack` — `internal/docstate/docstate.go:79,146-166`. Воспроизведено.
- **S-2** Имя каталога-юнита как prompt injection в `additionalContext`/`status`/`units` (`SafeName` пропускает пробелы, без лимита длины) — `internal/units/units.go:181-191`. Воспроизведено.
- **S-3** Тексты ошибок с путями и значениями `.git/config` в выводе `ack`/`status`/`changes` — `cmd/auto-context/main.go:299,303,324,397`, `internal/project/changes.go:72,79,129`. Воспроизведено.
- Юнит с ведущим `-` (`--exclude`) превращает совет Stop в `ack --exclude <юнит>` — `internal/units/units.go:256-266`, `internal/hooks/stop.go:174-181`. Воспроизведено.
- gitx: таймаут не срабатывает при живом потомке git (нет `WaitDelay`) — `internal/gitx/gitx.go:117-124`. Воспроизведено.
- CLI создаёт `.auto-context/` без `.gitignore` — `internal/project/project.go:326-337`, `internal/logx/logx.go:41`. Воспроизведено.

Архитектура/качество:
- **A-M1** Доменная логика хода в `hooks/stop.go`; `project` отдаёт `*units.Tree`.
- **A-M2** Проверки здоровья проекта продублированы в CLI и SessionStart.
- **A-M3** `status --exit-code` = 1 и для stale, и для ошибки, и в неактивном проекте; нет `--json`.
- **A-M4** Разные версии в Claude и Codex взаимно перезаписывают `cache.json`; `SetHash` стирает чужие ключи frontmatter.
- **Q-M1** `SessionStart` CC 34, 124 строки — `internal/hooks/session_start.go:34-157`.
- **Q-M2** Изменяемое состояние сопоставителя в `units.Tree` вопреки «read-only after Open».
- **Q-M3** YAML разбирается до 4 раз; дублированный ключ даёт ложное «wrong type» — `internal/config/config.go:113-157`.

Производительность (248k файлов / 8k юнитов: хуки 0,3–1,8 с, линейно; память — первое узкое место):
- **P-M1** Stop без правок кода грузит весь cache.json. **P-M2** После правки дока каждый Stop пересчитывает все статусы. **P-M3** `RefreshIndex` хэширует ради summary. **P-M4** cache.json перезаписывается целиком. **P-M5** GOGC=400 + 512 МиБ: 529 МБ на 248k файлов. **P-M6** Хэширование без лимита размера и дедлайна.

Тесты:
- **T-M1** нет `GIT_CEILING_DIRECTORIES` (35 падений при TMPDIR в репо); **T-M2** `changes` недопокрыт; **T-M3** `Project.Exclude` ошибки; **T-M4** нет конкурентных тестов; **T-M5** CI без `-race`/`-shuffle`/fuzz/покрытия; **T-M6** нет e2e бинарь+шим; **T-M7** нет fuzz и бенчмарков.

Документация:
- **D-M1** модель угроз без архива с `.git/`, `gitx.md` заявляет отсутствующую защиту; **D-M2** навыки не помечают вывод как недоверенный (S-6); **D-M3** неполная справка CLI; **D-M4** нет удаления/отключения/обновления в Codex; **D-M5** нет CHANGELOG и описания смены форматов; **D-M6** CLAUDE.md требует MCP, `.mcp.json` в `.gitignore`.

Best practices / CI/CD:
- **BP-M1** `gopkg.in/yaml.v3` архивирован → `go.yaml.in/yaml/v3`; **BP-M2** `context.Context` вместо глобального дедлайна; **BP-M3** нет `toolchain`; **BP-M4** нет staticcheck/govulncheck в CI.
- **CD-M1** нет dependabot/CodeQL/govulncheck по расписанию; **CD-M2** нет отката релиза; **CD-M3** бинарники не запускаются до публикации; **CD-M4** provenance не проверяема пользователем; **CD-M5** нет SECURITY.md и private reporting; **CD-M6** рассогласованная история релизов; **CD-M7** рост ветки `release`.

### Low Priority (P3 — Track in Backlog)

~87 пунктов, подробно в фазовых файлах и `raw/`. Наиболее заметные: удаление юнита не поднимает `_root`; ложное «code changed in _root»; `Input.Command()` без учёта регистра ключей JSON; Codex наследует `CLAUDE_PROJECT_DIR`; корень в хуках и CLI выбирается по-разному; `StripControls` пропускает bidi; FindRoot и неканонический HOME; `AddExclude` через YAML-якорь; `ack --exclude` без экранирования glob; расхождения разбора `.gitignore` на экзотических формах и вложенных репо; **S-4** `core.worktree`/`gitdir:` за корень; **S-5** `skills/sync` запускает `auto-context` через PATH; нет `.gitattributes eol=lf` для шима; npm в publish-задании с правами записи; `concurrency` отменяет промежуточные релизы; неверное имя теста в `context/cmd/auto-context.md:30`.

## Findings by Category

- **Code Quality**: 24 (0 C / 2 H / 4 M / 18 L)
- **Architecture**: 15 (0 C / 1 H / 4 M / 10 L)
- **Security**: 17 (0 C / 2 H / 6 M / 9 L) + 1 Info
- **Performance**: 13 (0 C / 0 H / 6 M / 7 L) + 1 Info
- **Testing**: 16 (0 C / 2 H / 7 M / 7 L)
- **Documentation**: 20 (0 C / 2 H / 6 M / 12 L)
- **Best Practices**: 16 (0 C / 0 H / 4 M / 12 L) + 6 Info
- **CI/CD & DevOps**: 20 (0 C / 1 H / 7 M / 12 L)

Итого: **141** (Critical 0 · High 10 · Medium 44 · Low 87). Счёт приблизительный: часть находок пересекается между фазами (например, A-H1 и D-H2) и учтена в обеих.

## Recommended Action Plan

1. **Закрыть исполнение кода через git** (малый объём): в `gitx.Run` env `GIT_NO_LAZY_FETCH=1`, `GIT_ALLOW_PROTOCOL=none`; `-c log.showSignature=false` (+ `--no-show-signature` в `changes`); `cmd.WaitDelay` и kill группы процессов; проверка `rev-parse --show-toplevel --absolute-git-dir` против корня (S-4). Регрессионные тесты рядом с `TestRunIgnoresFSMonitor`.
2. **Закрыть утечку текста репо в контекст** (средний): фиксированные категории ошибок в `docstate` Detail/`Ack`, `ack`/`status` warnings, `changes` (stderr git через `gitx.Brief`); `SafeName` — запрет ведущего `-`, пробелов/длины (или кавычки при выводе); bidi в `StripControls`; строгий разбор ключа `command`. Тесты инварианта «текст репо не цитируется». Обновить README Security, `context/_root.md`, `context/internal/gitx.md`, навыки (S-6).
3. **Шим** (малый): `PATH=/usr/bin:/bin` в начале `bin/auto-context`; `.gitattributes` `bin/auto-context text eol=lf` и включить его в дерево release.
4. **rootindex** (малый): линейный `fenced` (незакрытый блок — до конца файла), обрезка длины до очистки в `sanitize`; fuzz-тест.
5. **source_hash** (средний–большой, требует решения): хэшировать только отслеживаемые файлы (`ls-files` без `-o`) или явно задокументировать ограничение; версионировать схему хэша; разделить коды выхода `status --exit-code` (stale vs ошибка); исправить README CI-сценарий.
6. **Защита релиза** (малый, настройки GitHub): rulesets для `main`/`release`/`v*`, environment с апрувом для publish, обязательное SHA-закрепление, SECURITY.md + private vulnerability reporting, dependabot для gomod и actions, Immutable Releases.
7. **Тесты/CI** (средний): починить SKIP в `TestActive`/`TestSessionStartRefusedMarker`; `GIT_CEILING_DIRECTORIES` в `ScrubEnv`; FIFO/таймаут `ls-files`; `-race -shuffle=on` и govulncheck/staticcheck в CI; smoke-тест собранных бинарников через шим; fuzz для rootindex/frontmatter/ignore/allowedCommand.
8. **Мелочи надёжности** (малый): `EnsureStateDir` в `SaveCache`/`logx`; удаление юнита поднимает `_root`; `toolchain` в go.mod; `go.yaml.in/yaml/v3`; `concurrency: release-${{ github.ref }}`; CHANGELOG.
9. **Производительность и архитектура** (большой, по мере роста): `context.Context` как бюджет хука до хэширования; ленивая загрузка кэша в Stop; инкрементальный пересчёт статусов; лимит размера хэшируемого файла; вынос доменной логики хода из `hooks` в `project`; типизированная модель проблем для CLI и SessionStart.

## Review Metadata

- Review date: 2026-10-23
- Phases completed: 0 (scope), 1 (quality & architecture), 2 (security & performance), checkpoint 1, 3 (testing & documentation), 4 (best practices & CI/CD), 5 (report); плюс 4 предварительных ревью по областям (`raw/area-*.md`)
- Flags applied: нет
- Сырые отчёты: `.full-review/raw/`; воспроизведения: `/tmp/claude-1000/-home-elia-Projects-auto-context/b403d57d-6fe7-40d7-9c97-ee52030c3f1f/scratchpad/`
