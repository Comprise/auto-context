---
unit: internal/gitx
source_hash: v2:sha256:672bee5221db3314304b9b9d1fc445c95e4b7924216399a8c7ff5856d5ffa06c
commit: 88ca1135820fc05a2e9e2398df7e9876079719ae
updated: "2026-10-23"
---
# internal/gitx
Единый запуск git для плагина с фиксированной защитой.

## Назначение
Все вызовы git (список файлов в `units`, отчёт `changes` и проверки в `project`) идут через `Run`, чтобы конфиг или окружение не могли запустить программу, повесить хук, выдать неограниченный вывод или подменить репозиторий.

## Публичный API / точки входа
- `Run(dir, limit, args...)` — `git -C dir` с фиксированной защитой (см. ниже), с таймаутом `Timeout` (20 с) и не позже общего дедлайна процесса; stdout до `limit` байт (`Result.Truncated`).
- `NotRepo(err)` — `dir` не в git-репозитории: код 128 и «not a git repository» в stderr.
- `WorkTreeOK(root)` — git в `root` работает со своим деревом (`rev-parse --show-toplevel --absolute-git-dir`): реальный `root` равен toplevel или лежит под ним, а git-каталог (а) под toplevel, или (б) содержит файл `gitdir`, указывающий на `<toplevel>/.git` (связанный worktree), или (в) лежит под `<A>/.git/modules/` или `<A>/.git/worktrees/<n>/modules/` для строгого предка `A` корня (сабмодуль; во втором случае подходит и связанный worktree `<n>` рядом с основным репозиторием, если сам он — строгий предок корня). Если toplevel выше `root`, git-каталог не может лежать под `root` (`core.worktree`, указывающий вверх). `.git`-файл на каталог `worktrees/<n>` репозитория-предка — не сабмодуль. `false` без ошибки — топология чужая (`core.worktree` за пределы, `.git`-файл `gitdir:` на другой репозиторий, bare); ошибка — git не ответил.
- `SetDeadline(t)` — все последующие `Run` укладываются до `t` (хук ставит свой бюджет); после дедлайна git не запускается (`TimedOut`). Нулевое время снимает дедлайн.
- `Error` — код выхода и stderr (до 4 КиБ); `NotRun` (git не запустился), `TimedOut` (убит по таймауту/дедлайну или не запущен после дедлайна).
- `Brief(err)` — сбой без текста git: «timed out», «could not be run», «exit N».
- `Code(err)` — 0, код выхода или -1 (не запустился, таймаут, сигнал); `NotRun(err)`.

## Фиксированная защита `Run`
- Аргументы: `-c core.fsmonitor=false`, `-c core.pager=cat`, `-c log.showSignature=false` (`gpg.program` и родственные не запускаются из `log`), `--no-pager`.
- Окружение: `GIT_TERMINAL_PROMPT=0`, `GIT_OPTIONAL_LOCKS=0`, `GIT_CONFIG_NOSYSTEM=1`, `GIT_NO_LAZY_FETCH=1` (git ≥ 2.45: нет догрузки отсутствующих объектов от promisor-remote), `GIT_ALLOW_PROTOCOL=none` (любой git: ни один транспорт — `uploadpack`, `core.sshCommand`, `ext::`, `core.gitProxy` — не запускается), `GIT_PROTOCOL_FROM_USER=0`, `LC_ALL=C`, `LANGUAGE=C`; внешние `GIT_DIR`/`GIT_WORK_TREE`/`GIT_INDEX_FILE` и подобные удалены.
- Процессы: на Unix git в своей группе процессов (`proc_unix.go`), по таймауту убивается вся группа (`SIGKILL`); `WaitDelay` 1 с — потомок, держащий pipe, не вешает вызов (после выхода git это ошибка с кодом -1, `Brief` — «killed»). Следствие: kill группы процессов хука git не задевает — его ограничивают только `Timeout` и дедлайн (`SetDeadline`). На других ОС (`proc_other.go`) убивается только git, `WaitDelay` тот же.

## Инварианты и подводные камни
- Ни одной подкоманде плагина сеть не нужна: запрет транспортов регрессий не даёт.
- I-1: ключи, читающие файлы вне репозитория (`core.excludesFile`, `include.path`/`includeIf`, `core.attributesFile`, `objects/info/alternates`, `mailmap.file`), не закрыты; содержимое в вывод не попадает, остаётся лишь оракул «исключён / не исключён».
- Топологию (`core.worktree`, `.git`-файл) `Run` не проверяет: это делает вызывающий через `WorkTreeOK`.
- Английский вывод — контракт: вызывающие сравнивают сообщения git («not a git repository»).
- Защиту от фильтров (`diff`/`status` запускают clean-фильтры) `Run` не даёт: её решает вызывающий (`project.trustedFilters`, `--ignore-submodules=all`, `--no-ext-diff --no-textconv`).

## Связи
Зависит от: только стандартная библиотека.
Используется в: `cmd/auto-context`, `internal/project`, `internal/units`.
