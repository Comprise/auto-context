---
unit: cmd/auto-context
source_hash: sha256:25fa5fed586fca0c23c022d7300ecb658fd8bf35b8633f15e6f40aea3571cba0
updated: "2026-10-20"
---
# cmd/auto-context
CLI-бинарь: запуск хуков Claude Code и Codex и команды ack/status/changes/units/version.

## Назначение
Тонкая обвязка: разбирает аргументы, находит корень и вызывает `internal/hooks` или операции `internal/project`.

## Публичный API / точки входа
- `hook <event> [--agent codex]` — JSON из stdin (до 1 МиБ), ответ в stdout, код всегда 0; `--agent` — `claude` (по умолчанию) или `codex`, иное — запись в лог и выход 0. Событие `permission-request` — только для Codex: без `--agent codex` это ошибка аргументов (запись в лог, выход 0).
- `ack <unit>...` — `Project.Ack`, затем `RefreshIndex` — только если хоть один ack удался; `.` — `_root`. При битом конфиге — отказ (`project.ErrConfigBroken`), как и у `units` и `changes`.
- `ack --exclude <unit>` — `Project.Exclude` (каталог должен существовать); затем проект перечитывается и оглавление обновляется.
- `status [--stale] [--exit-code]` — таблица (предупреждение о маркерах называет индексный файл из конфига); `warning:` (конфиг, конфиг под `.gitignore`, кэш, маркеры оглавления, обход) и ошибка открытия — в stdout: это отчёт, а `!`-вставка skill `sync` может не видеть stderr; `note:` о доках-сиротах (не больше 20 имён, только прошедшие `units.SafeName`, остальные — числом), языке доков, числе ошибок в логе (с просьбой не читать его самому) и `warning:`, если лог нельзя дописать; `--exit-code` — 1 при stale/missing. При битом конфиге таблицы нет (юниты были бы по умолчанию): код 0, с `--exit-code` — 1.
- `changes <unit>` — `Project.WriteChanges`; ошибка (в том числе записи в stdout) — в stderr, код 1.
- `units`, `version`; `hook-exit <event> <code>` — внутренняя, шим пишет через неё падение бинаря в лог.

## Устройство и потоки данных
- Корень хука: `CLAUDE_PROJECT_DIR`, иначе `cwd` из JSON, иначе cwd процесса — абсолютным путём, через `config.FindRoot`; при сбое `getwd` — `CLAUDE_PROJECT_DIR`.
- Перед обработчиком `gitx.SetDeadline` по `budgetFor(event, agent)` (`hookBudget`; для SessionEnd у Codex — 2 с; на несколько секунд меньше таймаута хука из `hooks/hooks.json`, это проверяет тест): все вызовы git хука вместе укладываются в его время; по выходе из `runHook` дедлайн снимается.
- Обработчик из `hookHandlers` в защищённой от паник функции (паника логируется со стеком); при сбое SessionStart во включённом проекте выводится `hooks.SessionFailure`. Память ограничена (`SetMemoryLimit`), GC реже обычного.

## Инварианты и подводные камни
- Коды выхода CLI: 0 успех; 1 ошибка выполнения; 2 неверное использование.
- `status` не печатает текст лога, конфига и stderr git: всё это может прийти из репозитория (`config.Describe`, `gitx.Brief`, безопасные предупреждения `units`).
- `changes` нужен, чтобы skill `sync` не требовал git-разрешений: `Bash(git log *)` разрешил бы и `--output=<любой файл>`.
- `hookHandlers` сверяется с `hooks/hooks.json` и `hooks/codex.json` (`TestHooksJSONContract`); frontmatter каждого `skills/*/SKILL.md` — строгий YAML с `name` по имени каталога (`TestSkillsFrontmatter`: `claude plugin validate` пропускает часть невалидного YAML); шим проверяет `shim_test.go` (в том числе поиск корня, как у `config.FindRoot`).
- Regexp `PreToolUse` и `PermissionRequest` — подмножество подкоманд CLI (без `hook` и `hook-exit`).

## Связи
Зависит от: `internal/config`, `internal/docstate`, `internal/gitx`, `internal/hooks`, `internal/logx`, `internal/project`, `internal/rootindex`, `internal/textx`, `internal/units`.
Используется в: никем (main); вызывается шимом и skills.
