# Phase 1: Code Quality & Architecture Review

Полные отчёты: `raw/1A-code-quality.md`, `raw/1B-architecture.md`. Дополнительно учтены ревью по областям: `raw/area-hooks-session.md`, `raw/area-cli-infra.md`, `raw/area-packaging.md`, `raw/area-core-data.md`.

## Code Quality Findings

Счётчики (1A): Critical 0 · High 2 · Medium 3 · Low 10.

### High
- **Q-H1** `internal/rootindex/rootindex.go:102-124` — `fenced` квадратичен при незакрытом fence в корневом CLAUDE.md/AGENTS.md: 200 KB → 11 с, 2 MiB → ~1100 с; SessionStart/Stop упираются в таймаут, `status` зависает; при отсутствии маркеров `Update` зовёт `fenced` повторно (:203). Подтверждено замером.
- **Q-H2** `internal/rootindex/rootindex.go:69-75` — `sanitize` чистит `<!--`/`-->` в цикле до обрезки длины: квадратично по summary из `context/<unit>.md` (1 MiB ≈ 60 с на юнит). Подтверждено замером.

### Medium
- **Q-M1** `internal/hooks/session_start.go:34-157` — `SessionStart` 124 строки, CC 34, 8 обязанностей.
- **Q-M2** `internal/units/units.go:53-72`, `ignore.go:75-96`, `walk.go:44-59` — изменяемое состояние сопоставителя внутри `Tree`; `Classify` после `Discover` мутирует бюджет glob/кеш, вопреки «read-only after Open» (`project.go:64-65`).
- **Q-M3** `internal/config/config.go:113-157,216` — YAML разбирается до 4 раз; дублированный ключ даёт вводящее в заблуждение «wrong type» и сброс значений. Подтверждено тестом.
- **Q-M4** (из ревью областей) `internal/hooks/stop.go:174-181` — юнит с ведущим `-` (`--exclude`) превращает совет в `ack --exclude <юнит>`; `units.SafeName` пропускает `-`. Подтверждено.

### Low (кратко)
L1 путь CLI в строке формата `Sprintf` (`session_start.go:63,115,124,180`); L2 копии `eolOf`/`bom`; L3 три классификатора ошибок и двойная проверка «not a git repository»; L4 повторённый шаблон загрузки журнала, `LoadJournal` не нормализует `AckFailNotified`; L5 `writeChanges` 91 строка, CC 22; L6 трижды повторён разбор `hook` в `main.go`; L7 `Config.fix` выбирает сообщение по префиксу «must»; L8 для `_root` советуется невозможный `ack --exclude _root`; L9 `ack --exclude` при битом конфиге — голая ошибка yaml; L10 пустой `session_id` не отвергается. Из ревью областей: удаление юнита не поднимает `_root` (`stop.go:150-160`), давний юнит без доков даёт ложное «code changed in _root» (`stop.go:335`), `AddExclude` через YAML-якорь меняет другие ключи (`config.go:389`), `ack --exclude` не экранирует glob (`project.go:318`), неверное имя теста в `context/cmd/auto-context.md:30`.

Открыто с аудита 2026-10-20: дублирование `Active`/`ActivationRefused`, 4 похожие структуры ответа хуков, `Cache.Len` только для тестов.

## Architecture Findings

Счётчики (1B): Critical 0 · High 1 · Medium 4 · Low 9. Циклов нет, зависимости направлены вниз, инварианты `context/_root.md` соблюдены.

### High
- **A-H1** Закоммиченный `source_hash` зависит не только от закоммиченного кода: неотслеживаемые файлы (`ls-files -co`, и они же в `min_files`), локальные ignore (`--exclude-standard`), режим обнаружения (git/обход), вкомпилированные `Default()` версии бинарника; схема хэша не версионирована. Воспроизведено: ack при неотслеживаемом `scratch.go` → в чистом клоне `status --exit-code` = `_root stale`, exit 1 — ломает рекомендованный в README CI-сценарий.

### Medium
- **A-M1** Доменная логика хода (`recordTurn`, `pendingUnits`, `withParents`, политика auto-ack) живёт в `hooks/stop.go` вместе с текстами и JSON-протоколом; `project` отдаёт наружу `*units.Tree`.
- **A-M2** Проверки здоровья проекта продублированы в CLI (`loadRepo`/`runStatus`) и SessionStart; нет типизированной модели проблем.
- **A-M3** `status --exit-code` возвращает 1 и для stale, и для ошибки/битого конфига, и в неактивном проекте; нет `--json`, навыки парсят текст.
- **A-M4** Установки Claude и Codex разных версий делят `.auto-context/` и frontmatter: взаимная перезапись `cache.json`, `SetHash` стирает неизвестные ключи frontmatter, у журнала нет версии.

### Low (9)
`Agent.CLI()` читает окружение напрямую; `CLAUDE_PROJECT_DIR` учитывается и для Codex; дедлайн хука только для git; `FindRoot` в `config` и дубль в шиме; `session` зависит от `units` ради `Stat`; `Project.Exclude` нарушает read-only; frontmatter гарантированно конфликтует при слиянии; при отброшенных настройках ack не блокируется; скрытая подкоманда `hook-exit`. Из ревью областей: хуки и CLI выбирают корень по-разному (`main.go:155-167` vs `:233`).

## Critical Issues for Phase 2 Context

1. **Исполнение кода из локального `.git/config` через `git log`** (`internal/project/changes.go:86`, `internal/gitx/gitx.go:116`): `log.showSignature` + `gpg.program`; `changes` авто-одобряется PreToolUse. Подтверждено. Система защиты — список известных опасных ключей; нужна проверка остальных (`core.hooksPath`, `diff.external`, `core.sshCommand`, `protocol.*`, `include.path` и т.п.).
2. **Шим ищет `uname`/`tr`/`dirname`/`sed` через PATH** (`bin/auto-context:3-14`) — относительный элемент PATH даёт выполнение кода из репо на каждом хуке. Подтверждено.
3. **Таймаут gitx не работает при живом потомке git** (`gitx.go:117-124`, нет `WaitDelay`). Подтверждено.
4. **DoS враждебным репо через квадратичные `fenced`/`sanitize`** (Q-H1, Q-H2) и дедлайн хука только для git (обход/хэширование без ограничения).
5. **`Input.Command()` разбирает JSON без учёта регистра** (`internal/hooks/hooks.go:23-31`) — авто-одобрение по `Command`, а исполняется `command`.
6. **CLI создаёт `.auto-context/` без `.gitignore`** (`project.go:326-337`, `logx.go:41`) — риск закоммитить кэш.
7. **`StripControls` пропускает bidi/Cf** в вывод `changes` (`textx.go:29-31`).
9. **Путь из репо без очистки в выводе `status`/`ack`** (`internal/docstate/docstate.go:79,146-166`) — симлинк context-дока с многострочным именем цели → инструкции в контексте Claude. Подтверждено (`raw/area-core-data.md`).
8. **FindRoot обходит защиту $HOME при неканоническом HOME** (`config.go:447-451`).
