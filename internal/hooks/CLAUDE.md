# internal/hooks
Обработчики хуков Claude Code: SessionStart, PromptSubmit, Stop, PreToolUse, SessionEnd.

- `hooks.go` — входной JSON хука, открытие проекта и подготовка state-dir
- `session_start.go` — корневой индекс, stale/missing доки и проблемы проекта
- `prompt_submit.go` — начало хода: сброс журнала и новый снимок
- `stop.go` — сравнение со снимком, авто-ack, обратная связь «код изменён, доки нет»
- `pre_tool_use.go` — авто-одобрение Bash-вызовов собственного CLI
- `session_end.go` — удаление файлов сессии
- `testdata/session_start.golden.json` — эталон вывода SessionStart

Подробно: context/internal/hooks.md
