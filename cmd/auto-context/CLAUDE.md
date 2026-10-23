# cmd/auto-context
Точка входа CLI: диспетчер хуков Claude Code и Codex и команд обслуживания.

- `main.go` — разбор аргументов, таблица хуков (всегда код 0), корень CLI и хука, команды ack/status/units/version, коды выхода 0/1/2/3 и `--json`
- `changes.go` — команда `changes` (отчёт строит `internal/project`)

Подробно: context/cmd/auto-context.md
