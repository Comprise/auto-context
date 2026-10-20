# bin/

`auto-context` — POSIX sh-шим. Claude Code добавляет `bin/` плагина в PATH для Bash, поэтому skills вызывают `auto-context` по имени.

- Выбирает бинарь `../libexec/auto-context-<os>-<arch>[.exe]` по `uname` (linux, darwin, windows через Git Bash/MSYS; amd64, arm64). Бинари собирает `make dist` или релиз.
- Для `hook …` всегда выходит с 0: падение бинаря (Go fatal error выходит с 2, Claude Code считает это блокировкой) записывается через `hook-exit` в `.auto-context/log`. При падении SessionStart во включённом проекте шим выводит фиксированный JSON с `systemMessage` вместо частичного ответа бинаря.
- «Включённый проект» шим определяет сам, без поиска корня: `.auto-context/config.yaml` или `context/_root.md` в `CLAUDE_PROJECT_DIR` (или в `.`).
- Нет бинаря для платформы: для `hook session-start` во включённом проекте — JSON с `systemMessage`, иначе для хуков тишина; для CLI — ошибка и код 1.
- Поведение покрыто `cmd/auto-context/shim_test.go`.
