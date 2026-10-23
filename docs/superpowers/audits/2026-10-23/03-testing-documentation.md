# Phase 3: Testing & Documentation Review

Полные отчёты: `raw/3A-testing.md` (с примерами тестов для 8 пунктов из фазы 2), `raw/3B-documentation.md`.

## Test Coverage Findings

Счётчики (3A): Critical 0 · High 2 · Medium 7 · Low 7. Покрытие 91,2% (с `-coverpkg` 93,6%): cmd 88,2 · config 93,3 · docstate 92,6 · frontmatter 98,4 · fsutil 86,9 · gitx 94,3 · hashing 89,1 · hooks 94,0 · logx 85,3 · project 78,0 · rootindex 94,3 · session 91,9 · textx 100 · units 95,1. Прогоны `-count=3 -race`, `-shuffle=on`, `umask 077`, без HOME, ru-локаль — зелёные.

### High
- **T-H1** `TestSessionStartRefusedMarker` и кейс «маркер-симлинк наружу» в `TestActive` молча SKIP на всех ОС: симлинк создаётся в несуществующем `.auto-context/`, ошибка принимается за «symlinks unsupported». Ветка `ActivationRefused`=true и «plugin is off here» не проверяются. Поведение верное (проверено), но регрессия не будет поймана.
- **T-H2** FIFO вместо `.gitignore` в git-режиме не покрыт: каждый хук висит 20 с до таймаута git, сообщение ошибочно винит размер репо. Ветка таймаута `ls-files` (`internal/units/gitfiles.go:41`) не покрыта. Воспроизведено бинарём.

### Medium
- **T-M1** Тесты падают (35 шт.), если `TMPDIR` внутри git-репозитория: нет `GIT_CEILING_DIRECTORIES` в `testutil.ScrubEnv`.
- **T-M2** `changes`: не покрыты обрезка `maxChangesOutput`, сбой git внутри секции, причины `ackPoint`.
- **T-M3** `Project.Exclude` (пишет коммитный конфиг): не покрыты ветки ошибок.
- **T-M4** Нет конкурентных тестов параллельных сессий/процессов на одном репо.
- **T-M5** CI: только `go test ./...` — нет `-race`, `-shuffle`, порога покрытия, контроля неожиданных SKIP, fuzz.
- **T-M6** Нет сквозного теста «бинарь + шим + команды из hooks.json»; `main()` 0%, лимит stdin 1 МиБ не проверен.
- **T-M7** Нет fuzz-тестов (rootindex, frontmatter, lfWriter, .gitignore, allowedCommand) и бенчмарков горячих путей.

### Low
L-1 `rootindex.Update` на файле `"\r"` перезаписывает секцию дважды (найдено fuzz); остальные L-2…L-7 — в отчёте.

## Documentation Findings

Счётчики (3B): Critical 0 · High 2 · Medium 6 · Low 11. Из 20 сверенных утверждений 16 верны. README.md/README.ru.md и вложенные README синхронны; doc-комментарии Go полные и соответствуют коду; находки прошлого аудита исправлены.

### High
- **D-H1** `README.md:196-200` — раздел Security обещает больше, чем код: `changes` передаёт темы коммитов и имена файлов, имена юнитов уходят в контекст, git исполняет команды из `.git/config` архива (S-1), читает файлы вне репо (I-1, S-4).
- **D-H2** `README.md:153,180` — CI-сценарий `status --exit-code` и «игнорируемые git файлы не учитываются» вводят в заблуждение (документационная сторона A-H1).

### Medium
- **D-M1** `context/_root.md:49-52`, `context/internal/gitx.md:10` — модель угроз без сценария архива с `.git/`; `gitx.md` заявляет отсутствующую защиту.
- **D-M2** `skills/sync/SKILL.md:28-33`, `skills/auto-context-docs/SKILL.md:15-29`, `skills/init/SKILL.md:69-73` — нет предупреждения о недоверенном выводе (S-6).
- **D-M3** `README.md:147-157` — неполная справка CLI: коды выхода, семантика `--exit-code`, `version`, `ack .` = `_root`, формат вывода.
- **D-M4** `README.md:51-99` — нет удаления, отключения в проекте, обновления в Codex.
- **D-M5** нет CHANGELOG; `--generate-notes` (`release.yml:148`) почти пуст; не описаны изменения форматов (ключ `commit` во frontmatter, который старая версия стирает; `cache.json` v2→v3; строже `index_file`).
- **D-M6** `CLAUDE.md:8-56` — закоммиченный раздел требует MCP code-review-graph, а `.mcp.json` в `.gitignore`.

### Low
L-1…L-11 — неточности README, устаревшие места спецификации (§4, §7, §10.2, §13), `hooks/README.md:40`, комментарий `EnsureStateDir` (`internal/fsutil/fsutil.go:319-323`); подробности в отчёте.
