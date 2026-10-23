# Review Scope

## Target

Весь проект auto-context (коммит af0cae8, ветка main): плагин Claude Code и Codex CLI на Go, который держит индексные файлы юнитов (CLAUDE.md/AGENTS.md) и библиотеку context/ в синхроне с кодом. Модель угроз: репозиторий может быть враждебным клоном, хуки срабатывают автоматически; хуки всегда завершаются с кодом 0. Архитектура и инварианты: context/_root.md. Предыдущий аудит: docs/superpowers/audits/2026-10-20/ (исправлен в af0cae8), отчёт безопасности: CLAUDE-SECURITY-20261019-223634/.

## Files

- `cmd/auto-context/` — бинарник (hook, ack, status, changes, units, version)
- `internal/` — config, docstate, frontmatter, fsutil, gitx, hashing, hooks, logx, project, rootindex, session, testutil, textx, units (код и тесты)
- `bin/auto-context` — POSIX sh-shim
- `hooks/hooks.json`, `hooks/codex.json`
- `.claude-plugin/`, `.codex-plugin/`, `.agents/` — манифесты
- `skills/` — навыки плагина
- `.github/workflows/`, `.github/scripts/`, `Makefile`, `go.mod`
- `README.md`, `README.ru.md`, `*/README*`, `context/*.md`, `CLAUDE.md`, `docs/superpowers/`

## Flags

- Security Focus: no
- Performance Critical: no
- Strict Mode: no
- Framework: Go (stdlib, без внешних фреймворков); плагины Claude Code / Codex CLI

## Review Phases

1. Code Quality & Architecture
2. Security & Performance
3. Testing & Documentation
4. Best Practices & Standards
5. Consolidated Report
