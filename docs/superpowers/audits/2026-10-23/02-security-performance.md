# Phase 2: Security & Performance Review

Полные отчёты: `raw/2A-security.md`, `raw/2B-performance.md`. Находки фазы 1 и ревью по областям (gpg.program, шим и PATH, gitx без WaitDelay, путь в `docstate` Detail, имя `--exclude`) здесь не дублируются.

## Security Findings

Счётчики (2A): Critical 0 · High 1 · Medium 2 · Low 3 · Info 1. govulncheck: уязвимостей в зависимостях нет.

### High
- **S-1 (CVSS 7.8, CWE-15/78) — исполнение команды из локального `.git/config` при ленивой докачке объектов.** Репо-архив с `.git/` объявлен partial clone (`extensions.partialClone`); недостающий блоб (skip-worktree `.gitignore`) заставляет git запустить транспорт из конфига (`remote.<r>.uploadpack`, `core.sshCommand`, `ext::`, `core.gitProxy` — все 4 подтверждены). Срабатывает в каждом хуке через `internal/units/gitfiles.go:29` и в авто-одобряемых `status`/`units`/`changes` (`internal/project/changes.go:127`). ВОСПРОИЗВЕДЕНО. Исправление в `internal/gitx/gitx.go:116-118`: env `GIT_NO_LAZY_FETCH=1`, `GIT_ALLOW_PROTOCOL=none` (+ `-c log.showSignature=false` для находки фазы 1). Тот же репо срабатывает и на `git status` самого Claude Code, но плагину нужно закрыться у себя.

### Medium
- **S-2 (CWE-1427) — имя каталога-юнита как prompt injection** в `additionalContext` SessionStart/Stop и выводе `status`/`units`: `SafeName` (`internal/units/units.go:181-191`) пропускает пробелы, длина не ограничена; выводится в `internal/hooks/session_start.go:290,293`, `cmd/auto-context/main.go:377,440`. ВОСПРОИЗВЕДЕНО.
- **S-3 — ещё места, где текст ошибки с путями/конфигом репо уходит в вывод:** `cmd/auto-context/main.go:299,303,324` (`ack`: `warning: root index: %v` с многострочным путём цели симлинка `CLAUDE.md`), `main.go:397` (`status`, одной строкой через `textx.Err`), `internal/project/changes.go:72,79,129` (stderr git цитирует значения `.git/config`). ВОСПРОИЗВЕДЕНО. Исправление: фиксированные категории (`errCategory`), `gitx.Brief`.

### Low
- **S-4 (CWE-22/200)** — `core.worktree` или `.git`-файл с `gitdir:` уводят git за корень; `changes` выводит чужие файлы и темы коммитов (`gitx.go:116`, `gitfiles.go:29`, `changes.go:127`). ВОСПРОИЗВЕДЕНО. Исправление: сверять `rev-parse --show-toplevel --absolute-git-dir` с корнем.
- **S-5 (CWE-426)** — `skills/sync/SKILL.md:5,12`: `allowed-tools: Bash(auto-context status --stale)` и `!`-вставка запускают `auto-context` через PATH в обход `shadowed()`. ПО КОДУ.
- **S-6 (CWE-1427)** — навыки sync и auto-context-docs не предупреждают модель, что вывод `changes`, имена файлов и тексты доков — недоверенные данные. ПО КОДУ.
- **I-1** — `core.excludesFile`, `include.path`/`includeIf`, `core.attributesFile`, `objects/info/alternates` читают файлы вне репо; содержимое в вывод не попадает.

Проверено и не исполняется: `core.hooksPath`, `diff.external`, `diff.*.command`/textconv, `core.pager`/`pager.*`, `core.editor`, `trace2.*`, `gc.auto`, `status.submoduleSummary`; `filter.*` и `core.fsmonitor` закрыты.

## Performance Findings

Счётчики (2B): Critical 0 · High 0 · Medium 6 · Low 7 · Info 1. Вне `rootindex` квадратичных путей нет; хуки линейны (~1,5–2,5 мкс/файл), первой упирается память.

Замеры (rb: 248k файлов, 8k юнитов; нагрузка машины завышала wall в 1,5–3 раза): prompt-submit 0,32 с; stop без изменений 0,52 с; session-start тёплый 0,71 с (275 МБ), без cache.json 1,45 с (427 МБ); stop после `touch` всех файлов 1,76 с, **529 МБ — выше лимита 512 МиБ**. cache.json ≈138 Б/файл (27,7 МБ), полная перезапись 130–150 мс.

### Medium
- **P-M1** `internal/hooks/stop.go:70`, `internal/project/project.go:203` — Stop без правок кода грузит и прореживает весь cache.json (~25% CPU).
- **P-M2** `stop.go:139,290`, `project.go:274` — после правки context-дока каждый Stop пересчитывает статусы всех юнитов и индекс (0,51→0,78 с, 222→411 МБ).
- **P-M3** `project.go:297` — `RefreshIndex` вызывает полный `Statuses()` с хэшированием ради summary.
- **P-M4** `internal/hashing/hashing.go:68-141` — кэш одним JSON, полная перезапись на любую правку, racy-записи ≥2 перезаписи.
- **P-M5** `cmd/auto-context/main.go:173-174` — GOGC=400 + лимит 512 МиБ: на ~0,9 млн файлов GC почти непрерывен, хотя `maxList`/`maxCache` допускают ~2 млн.
- **P-M6** `hashing.go:197` — хэширование без лимита размера и без дедлайна (файл 4 ГБ +2,6 с к каждому рехэшу).

### Low / Info
P-L1 `fsutil.go:226` `io.ReadAll` без предвыделения; P-L2 `project.go:213-226` прогрев юнитов без доков (на init хэшируется весь репо впустую); P-L3 `walk.go:399,424` lstat без git последовательный; P-L4 `main.go:246` кэш грузится для `units`/`changes` зря; P-L5 `hashing.go:285-294` периодическое сохранение в Prewarm останавливает воркеры; P-L6 `hashing.go:122` параллельные сессии теряют хэши друг друга; P-L7 `gitfiles.go:29` `ls-files -co` ~220 мс на хук — советовать `core.untrackedCache`; P-I1 снимок сессии ~9 МБ живёт до 7 дней без SessionEnd.

## Critical Issues for Phase 3 Context

Тесты, которых не хватает для подтверждённых находок:
1. Регрессионные тесты gitx против исполнения из `.git/config`: `log.showSignature`+`gpg.program`, partial clone + lazy fetch (`uploadpack`/`sshCommand`/`ext::`), `core.worktree`/`gitdir:` за корнем.
2. Таймаут gitx при живом потомке git.
3. Шим при враждебном PATH (относительный элемент) и CRLF.
4. Инвариант «текст репо не цитируется»: таблицы `status`/`ack`/`units`/`changes` и `additionalContext` при враждебных именах юнитов, целях симлинков, stderr git; bidi в `StripControls`.
5. Имена юнитов с ведущим `-` и пробелами; JSON `tool_input` с ключами `Command`/`COMMAND`.
6. Патологические входы rootindex (незакрытые fence, длинные summary) — бенчмарки/лимиты времени.
7. `status`/`ack` в проекте без `.auto-context/` не оставляют неигнорируемых файлов; `source_hash` не зависит от неотслеживаемых файлов (A-H1).
8. Удаление юнита в ходе поднимает `_root` (тест `TestStopUnitDirDeleted` сейчас закрепляет молчание).

Документация, которую затрагивают находки: навыки (недоверенный вывод, S-6), README (CI-сценарий `status --exit-code`, A-H1/A-M3), `context/cmd/auto-context.md:30` (имя теста), модель угроз в `context/_root.md` (архив с `.git/`, partial clone).
