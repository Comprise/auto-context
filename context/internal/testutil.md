---
unit: internal/testutil
source_hash: sha256:b0f0a89d4189748e88ee24d3350dddd37af4e30a9486ab5327c07fa96ffd1488
updated: "2026-10-20"
---
# internal/testutil
Общие хелперы тестов: чистое окружение, запись файлов, запуск git.

## Назначение
Тесты не должны зависеть от сессии и git-настроек разработчика (`CLAUDE_PROJECT_DIR`, `GIT_DIR`, глобальный `commit.gpgsign`). Пакет даёт один `TestMain` и общие хелперы вместо локальных копий.

## Публичный API / точки входа
- `ScrubEnv()` — снимает все `GIT_*`, `CLAUDE_PROJECT_DIR`, `CLAUDE_PLUGIN_ROOT`, `CLAUDE_PLUGIN_DATA`, `PLUGIN_ROOT`, `PLUGIN_DATA`, `CODEX_HOME`; ставит `GIT_CONFIG_GLOBAL=os.DevNull`, `GIT_CONFIG_NOSYSTEM=1` и автора/коммитера `t`/`t@t`.
- `Main(m)` — тело `TestMain`: `ScrubEnv()`, затем `os.Exit(m.Run())`.
- `WriteFile(t, root, rel, content)` — пишет файл, создаёт каталоги; `rel` через `/`.
- `RequireGit(t)` — `t.Skip`, если git не установлен.
- `Git(t, dir, args...)` — `git -C dir args...`; stdout возвращает, сбой — `t.Fatalf` с выводом.

## Инварианты и подводные камни
- Импортируется только из `_test.go`.
- `TestMain` с `testutil.Main` стоит в пакетах, которые запускают git или читают переменные агента: `cmd/auto-context`, `internal/hooks`, `internal/units`, `internal/gitx`, `internal/project`.
- `HOME` не трогается: от него зависят поиск корня и шим, тесты ставят его сами.
- Путь «нет файла» в тестах — `os.DevNull`, не `/dev/null` (Windows).

## Связи
Зависит от: только стандартная библиотека.
Используется в: тестах остальных пакетов.
