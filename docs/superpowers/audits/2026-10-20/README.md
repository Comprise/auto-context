# Audit 2026-10-20 (excluding security)

**English** · [Русский](README.ru.md)

Four reports by area: `core.md` (core correctness), `integration.md` (Claude Code/Codex, packaging, release),
`tests.md` (tests), `quality.md` (code quality and docs accuracy). Security was not part of the audit.

Coordinator checks:
- `tests.md` no. 5 is false: `internal/hooks/testdata/permission_request.json` is read by `cmd/auto-context/main_test.go:1051`.
- `core.md` no. 2 reproduced: `auto-context ack typo` in an inactive git repo exits with code 1 and creates a root
  `CLAUDE.md` with an empty `auto-context:units` section.
- Duplicate: `integration.md` no. 6 and `quality.md` no. 2 are one finding (`hooks/README.md` does not know `codex.json`).

Fix plan: `docs/superpowers/plans/2026-10-20-audit-fixes.md`.
