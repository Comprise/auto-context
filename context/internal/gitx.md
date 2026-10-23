---
unit: internal/gitx
source_hash: v2:sha256:16584c26e5dabf982478d62f06daafde49d0f96b6c0dd400009c8f7aef43260e
commit: c1bec2b5cbe35cde1bcf38cbdaecb885b111345f
updated: "2026-10-24"
---
# internal/gitx
Единый запуск git для плагина с фиксированной защитой.

## Назначение
Все вызовы git (список файлов в `units`, отчёт `changes` и проверки в `project`) идут через `Run`, чтобы конфиг или окружение не могли запустить программу, повесить хук, выдать неограниченный вывод или подменить репозиторий.

## Публичный API / точки входа
- `Run(ctx, dir, limit, args...)` — `git -C dir` с фиксированной защитой (см. ниже), с таймаутом `Timeout` (20 с) и не дольше `ctx` (бюджет хука, прерванная команда CLI); после конца `ctx` git не запускается (`TimedOut`, stderr «not run: the hook's time is up» или «canceled»); stdout до `limit` байт (`Result.Truncated`).
- `NotRepo(err)` — `dir` не в git-репозитории: код 128 и «not a git repository» в stderr.
- `WorkTreeOK(ctx, root)` — git в `root` работает со своим деревом (`rev-parse --show-toplevel --absolute-git-dir`): реальный `root` равен toplevel или лежит под ним, а git-каталог (а) под toplevel, или (б) содержит файл `gitdir`, указывающий на `<toplevel>/.git` (связанный worktree), или (в) лежит под `<A>/.git/modules/` или `<A>/.git/worktrees/<n>/modules/` для строгого предка `A` корня (сабмодуль; во втором случае подходит и связанный worktree `<n>` рядом с основным репозиторием, если сам он — строгий предок корня). Если toplevel выше `root`, git-каталог не может лежать под `root` (`core.worktree`, указывающий вверх). `.git`-файл на каталог `worktrees/<n>` репозитория-предка — не сабмодуль. `false` без ошибки — топология чужая (`core.worktree` за пределы, `.git`-файл `gitdir:` на другой репозиторий, bare); ошибка — git не ответил.
- `Error` — код выхода и stderr (до 4 КиБ); `NotRun` (git не запустился), `TimedOut` (убит по таймауту или по концу `ctx`, или не запущен после него).
- `Brief(err)` — сбой без текста git: «timed out», «could not be run», «exit N».
- `Code(err)` — 0, код выхода или -1 (не запустился, таймаут, сигнал); `NotRun(err)`.

## Фиксированная защита `Run`
- Аргументы: `-c core.fsmonitor=false`, `-c core.pager=cat`, `-c log.showSignature=false` (`gpg.program` и родственные не запускаются из `log`), `--no-pager`.
- Окружение: `GIT_TERMINAL_PROMPT=0`, `GIT_OPTIONAL_LOCKS=0`, `GIT_CONFIG_NOSYSTEM=1`, `GIT_NO_LAZY_FETCH=1` (git ≥ 2.45: нет догрузки отсутствующих объектов от promisor-remote), `GIT_ALLOW_PROTOCOL=none` (любой git: ни один транспорт — `uploadpack`, `core.sshCommand`, `ext::`, `core.gitProxy` — не запускается), `GIT_PROTOCOL_FROM_USER=0`, `LC_ALL=C`, `LANGUAGE=C`; внешние `GIT_DIR`/`GIT_WORK_TREE`/`GIT_INDEX_FILE` и подобные удалены.
- Процессы: на Unix git в своей группе процессов (`proc_unix.go`), по таймауту убивается вся группа (`SIGKILL`); `WaitDelay` 1 с — потомок, держащий pipe, не вешает вызов (после выхода git это ошибка с кодом -1, `Brief` — «killed»). Следствие: kill группы процессов хука git не задевает — его ограничивают только `Timeout` и `ctx` (`TestRunHonorsParentDeadline`). На других ОС (`proc_other.go`) убивается только git, `WaitDelay` тот же.

## Инварианты и подводные камни
- Ни одной подкоманде плагина сеть не нужна: запрет транспортов регрессий не даёт.
- I-1: ключи, читающие файлы вне репозитория (`core.excludesFile`, `include.path`/`includeIf`, `core.attributesFile`, `objects/info/alternates`, `mailmap.file`), не закрыты; содержимое в вывод не попадает, остаётся лишь оракул «исключён / не исключён».
- Топологию (`core.worktree`, `.git`-файл) `Run` не проверяет: это делает вызывающий через `WorkTreeOK`.
- Английский вывод — контракт: вызывающие сравнивают сообщения git («not a git repository»).
- Защиту от фильтров (`diff`/`status` запускают clean-фильтры) `Run` не даёт: её решает вызывающий (`project.trustedFilters`, `--ignore-submodules=all`, `--no-ext-diff --no-textconv`).

## Связи
Зависит от: только стандартная библиотека.
Используется в: `cmd/auto-context`, `internal/project`, `internal/units`.
