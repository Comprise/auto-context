# bin/

`auto-context` — POSIX sh-шим. Claude Code добавляет `bin/` плагина в PATH для Bash, поэтому skills вызывают `auto-context` по имени.

- Выбирает бинарь `../libexec/auto-context-<os>-<arch>[.exe]` по `uname` (linux, darwin, windows через Git Bash/MSYS; amd64, arm64). Бинари собирает `make dist` или релиз.
- Для `hook …` всегда выходит с 0: падение бинаря (Go fatal error выходит с 2, Claude Code считает это блокировкой) записывается через `hook-exit` в `.auto-context/log`. При падении SessionStart во включённом проекте шим выводит фиксированный JSON с `systemMessage` вместо частичного ответа бинаря.
- «Включённый проект» шим определяет, как `config.FindRoot` (без проверки владельца): от `CLAUDE_PROJECT_DIR` (или `.`) вверх до каталога с `.git` или `.auto-context/config.yaml`, не выше `$HOME`; проект включён, если там есть `.auto-context/config.yaml` или `context/_root.md`.
- Шим не читает stdin (его читает бинарь), поэтому включённость проверяет от `CLAUDE_PROJECT_DIR`, а без него — от рабочего каталога процесса (в Codex это cwd сессии), а не от `cwd` из JSON. Его уведомление о пропавшем или упавшем бинаре — best effort.
- Нет бинаря для платформы: для `hook session-start` во включённом проекте — JSON с `systemMessage`, иначе для хуков тишина; для CLI — ошибка и код 1.
- Поведение покрыто `cmd/auto-context/shim_test.go`.
