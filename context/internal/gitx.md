---
unit: internal/gitx
source_hash: sha256:4d043f074b6a21f395b891557c46e18dc6ce0ba945950fc7778bbb4ce931940d
updated: "2026-10-20"
---
# internal/gitx
Единый запуск git для плагина с фиксированной защитой.

## Назначение
Все вызовы git (список файлов в `units`, отчёт `changes` и проверки в `project`) идут через `Run`, чтобы конфиг или окружение не могли запустить программу, повесить хук, выдать неограниченный вывод или подменить репозиторий.

## Публичный API / точки входа
- `Run(dir, limit, args...)` — `git -C dir` с `core.fsmonitor=false`, без пейджера, без системного конфига (`GIT_CONFIG_NOSYSTEM`), без запросов, без optional locks, по-английски (`LC_ALL=C`), без внешних `GIT_DIR`/`GIT_WORK_TREE`/`GIT_INDEX_FILE` и подобных, с таймаутом `Timeout` (20 с) и не позже общего дедлайна процесса; stdout до `limit` байт (`Result.Truncated`).
- `SetDeadline(t)` — все последующие `Run` укладываются до `t` (хук ставит свой бюджет); после дедлайна git не запускается (`TimedOut`). Нулевое время снимает дедлайн.
- `Error` — код выхода и stderr (до 4 КиБ); `NotRun` (git не запустился), `TimedOut` (убит по таймауту/дедлайну или не запущен после дедлайна).
- `Brief(err)` — сбой без текста git: «timed out», «could not be run», «exit N».
- `Code(err)` — 0, код выхода или -1 (не запустился, таймаут, сигнал); `NotRun(err)`.

## Инварианты и подводные камни
- Английский вывод — контракт: вызывающие сравнивают сообщения git («not a git repository»).
- Защиту от фильтров (`diff`/`status` запускают clean-фильтры) `Run` не даёт: её решает вызывающий (`project.trustedFilters`, `--ignore-submodules=all`, `--no-ext-diff --no-textconv`).

## Связи
Зависит от: только стандартная библиотека.
Используется в: `cmd/auto-context`, `internal/project`, `internal/units`.
