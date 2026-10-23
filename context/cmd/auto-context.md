---
unit: cmd/auto-context
source_hash: v2:sha256:9acbfd8d91f5b007b4889d7a4e18ab8cc723e29a0d0f44bf736c1f1031d8c231
commit: c1bec2b5cbe35cde1bcf38cbdaecb885b111345f
updated: "2026-10-24"
---
# cmd/auto-context
CLI-бинарь: запуск хуков Claude Code и Codex и команды ack/status/changes/units/version.

## Назначение
Тонкая обвязка: разбирает аргументы, находит корень и вызывает `internal/hooks` или операции `internal/project`.

## Публичный API / точки входа
- `hook <event> [--agent codex]` — JSON из stdin (до 1 МиБ), ответ в stdout, код всегда 0; `--agent` — `claude` (по умолчанию) или `codex`. Разбор — `parseHookArgs` по таблице `hookTable`: неизвестное событие, неверный `--agent` и `permission-request` без `--agent codex` — запись `hook: bad arguments` в лог и выход 0.
- `ack <unit>...` — `Project.Ack`, затем `RefreshIndex` — только если хоть один ack удался; `.` — `_root`; аргумент с `-` в начале — ошибка использования. Отказ — код 3, по строке на юнит: `unknown unit <u>` (имя — только прошедшее `units.SafeName`, иначе `(name not shown)`), `no context/<u>.md: …` для отсутствующего дока, тексты `project.ErrConfigBroken`/`ErrConfigDegraded`, иначе `<u>: <категория>` (`project.CauseOf(err).Text(project.CLI)`) с `(details in .auto-context/log)`.
- `ack --exclude <unit>` — сначала отказ при битом конфиге (`ErrConfigBroken`, код 3), затем проверка каталога и `Project.Exclude` (перечитывает проект на месте) и `RefreshIndex`; кэш хэшей не загружается.
- `ack --upgrade` — `Project.Upgrade`: доки, актуальные по старой схеме хэша (`old hash scheme`), переписываются в схему `v2:`; в stdout — `upgraded N unit(s) to hash scheme v2`, отказы — в stderr, код 3.
- `status [--stale] [--exit-code] [--json]` — таблица (или JSON), `warning:` и `note:`; всё в stdout: это отчёт, читается целиком (skill `sync` запускает `status --stale --json` через Bash; с `--json` ошибка открытия — в stderr, stdout — только документ). Предупреждения и заметки — рендер `Project.Diagnose()` (`renderStatusProblems`, тексты — `Problem.Text(project.CLI, …)`; кэш сохраняет `runStatus` после статусов и передаёт предупреждения сохранения): сначала настройка проекта (state-dir, конфиг, `.gitignore`, обход), затем кэш и его сохранение, оглавление, лог; заметки — активация, язык, лог. Лог проверяется последним и заново (`Project.LogProblems`): в число ошибок входят подробности, только что записанные самим `status`; при нечитаемом логе о логе — только это. `note:` о доках-сиротах (не больше 20 имён, только прошедшие `units.SafeName`, остальные — числом), языке доков, числе ошибок в логе (с просьбой не читать его самому), неактивном проекте; `warning:` о конфиге, конфиге под `.gitignore`, кэше, маркерах оглавления, обходе, логе, который нельзя дописать. При битом конфиге таблицы нет (юниты были бы по умолчанию), `paused: true`.
- `changes <unit>` — `Project.WriteChanges`; битый конфиг и неизвестный юнит проверяются до него; сбой записи — в stderr, код 3.
- `units [--json]` — TSV `<path>\t<reason>\t<files>` или JSON.
- `version [-v]` — версия: `-X main.version`, иначе версия модуля из `debug.ReadBuildInfo` (для `go install …@vX`), иначе `dev`. `-v` добавляет `revision` (`vcs.revision`, с `(modified)`), версию Go, `platform GOOS/GOARCH` и форматы на диске: `cache 5`, `snapshot 1`, `journal 1`, `hash v2` (копии констант пакетов; расхождение ловит `TestVersionVerbose`).
- `hook-exit <event> <code>` — внутренняя, не в usage: шим вызывает её после падения бинаря (код ≠ 0), запись в лог корня хука (как у Claude).

## Контракт CLI
- Коды выхода: 0 — успех / всё актуально; 1 — только `status --exit-code` при stale/missing; 2 — неверное использование (usage в stderr); 3 — ошибка: не открыть проект или cwd, битый конфиг (`ack`, `units`, `changes`, `status --exit-code`), отказ ack, сбой `changes`, неактивный проект при `status --exit-code`. Без `--exit-code` `status` отдаёт 0, если проект открылся.
- `status --json` (`format_version` 1): `{"format_version":1,"paused":bool,"units":[{"unit","state","doc","note","detail"}],"orphans":{"shown":[…],"hidden":N},"warnings":[…],"notes":[…]}`; `note` — фиксированный `docstate.Note`, `detail` — фиксированная категория; списки всегда массивы (не `null`); тексты `warnings`/`notes` — как строки `warning:`/`note:` без префикса. Сочетается с `--stale` (только stale/missing) и `--exit-code`.
- `units --json` (`format_version` 1): `{"format_version":1,"units":[{"path","reason","files"}]}`.
- Вывод не цитирует текст из репозитория: только имена, прошедшие `units.SafeName`, числа, фиксированные категории (таблица причин `project.CauseOf`/`Cause.Text`, `gitx.Brief`, `config.Describe`) и вывод `changes` после `textx.StripControls`. Полный текст ошибки — в `.auto-context/log` (`logx.Errorf`, только в активном проекте).

## Устройство и потоки данных
- Корень CLI (`cliRoot`): `CLAUDE_PROJECT_DIR`, если cwd внутри него (сравнение и по разрешённым симлинкам), — так `ack` после `cd` во вложенный репозиторий идёт в проект хуков; иначе `project.FindRoot(cwd)`.
- Корень хука (`hookRoot`): для Claude — `CLAUDE_PROJECT_DIR`, иначе `cwd` из JSON, иначе cwd процесса — абсолютным путём, через `project.FindRoot`; Codex эту переменную не выставляет, унаследованная от сессии Claude указывала бы на чужой проект, поэтому для Codex она не читается. Без абсолютного пути хук ничего не делает. При сбое `getwd` хук работает от `CLAUDE_PROJECT_DIR` (только Claude) или `cwd` из JSON.
- `hookTable` — событие → обработчик, бюджет git и хеширования (`budget`, для Codex — `codexBudget`, если короче: SessionEnd — 2 с) и `codexOnly`. `runHook` создаёт `context.WithTimeout` по `budgetFor(event, agent)` — на несколько секунд меньше таймаута из `hooks/hooks.json` и `hooks/codex.json` (это проверяют тесты) — и передаёт его в `Env.Ctx`; глобального состояния нет. Команды `ack`, `status`, `units`, `changes` работают в `signal.NotifyContext(context.Background(), os.Interrupt)`: Ctrl-C останавливает git и хеширование; если открытие проекта прервано, ошибка — `interrupted` (`project.CauseOfCtx`), тайм-аут или переполнение листинга git — свои фиксированные тексты, а не `unreadable`.
- `runHook`: читает вход, проверяет `hooks.CheckEvent` (несовпадение `hook_event_name` с подкомандой — запись в лог и пустой ответ, без `SessionFailure`), заполняет `Env.PluginRoot` (`PLUGIN_ROOT` для Codex, иначе `CLAUDE_PLUGIN_ROOT`) и вызывает обработчик в защищённой от паник функции (паника логируется со стеком); при иных сбоях SessionStart во включённом проекте выводится `hooks.SessionFailure`. Память ограничена (`SetMemoryLimit`), GC реже обычного.
- `loadRepo` открывает проект и собирает предупреждения (`Project.DiagnoseOpen()`, только `SeverityWarning`, через `cliRender(p)` — с контекстом команды: прерванный `ack`/`changes` сообщает `interrupted`), но кэш хэшей не грузит: его загружают и о нём предупреждают только `status` и `ack` (`units`, `changes`, `ack --exclude` его не читают).

## Инварианты и подводные камни
- `status` не печатает текст лога, конфига, ошибок путей и stderr git: всё это может прийти из репозитория.
- `changes` нужен, чтобы skill `sync` не требовал git-разрешений: `Bash(git log *)` разрешил бы и `--output=<любой файл>`.
- `hookTable` сверяется с `hooks/hooks.json` (`TestHooksJSONContract`) и `hooks/codex.json` (`TestCodexHooksManifest`); `TestE2EHooksThroughShim` собирает бинарь в каталог плагина с пробелом и запускает каждую команду обоих манифестов через настоящий шим и `sh -c` (код 0, ответ пуст или JSON; stdin 2 МиБ — код 0 и `input larger than` в логе). Frontmatter каждого `skills/*/SKILL.md` — строгий YAML с `name` по имени каталога (`TestSkillsFrontmatter`); шим проверяет `shim_test.go`.
- `TestUpgradeFromV1Project` закрепляет обновление проекта v1.0.0 (неотслеживаемые файлы, старые хэши → `ok (old hash scheme)`, после `ack --upgrade` — `v2:`) и его известную цену: юнит с файлом > 32 МиБ и `_root`, если юнитом был каталог с пробелом, становятся `stale`.
- Regexp `PreToolUse` и `PermissionRequest` — подмножество подкоманд CLI (без `hook` и `hook-exit`).

## Связи
Зависит от: `internal/config`, `internal/docstate`, `internal/hooks`, `internal/logx`, `internal/project`, `internal/units`.
Используется в: никем (main); вызывается шимом (`bin/auto-context`) и skills.
