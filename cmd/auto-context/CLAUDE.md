# cmd/auto-context
Точка входа CLI: диспетчер хуков Claude Code и Codex и команд обслуживания.

- `main.go` — разбор аргументов, запуск хуков (всегда код 0), команды ack/status/units/version
- `changes.go` — команда `changes` (отчёт строит `internal/project`)

Подробно: context/cmd/auto-context.md
