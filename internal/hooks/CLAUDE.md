# internal/hooks
Обработчики хуков Claude Code и Codex: SessionStart, PromptSubmit, Stop, PreToolUse, PermissionRequest, SessionEnd.

- `hooks.go` — входной JSON хука, открытие проекта и подготовка state-dir
- `agent.go` — `Agent` (claude|codex): как тексты называют навыки и команды
- `session_start.go` — корневой индекс, stale/missing доки и проблемы проекта
- `prompt_submit.go` — начало хода: сброс журнала и новый снимок
- `stop.go` — ввод для `turn.Evaluate`, авто-ack и увиденные доки, текст обратной связи «код изменён, доки нет»
- `pre_tool_use.go` — авто-одобрение Bash-вызовов собственного CLI
- `permission_request.go` — то же авто-одобрение для Codex (PermissionRequest)
- `session_end.go` — удаление файлов сессии
- `testdata/*.golden.json` — эталоны вывода SessionStart и Stop (Claude и Codex)
- `concurrent_test.go` — параллельные Stop из нескольких процессов, бенчмарк Stop

Подробно: context/internal/hooks.md
