# Аудит 2026-10-20 (без безопасности)

Четыре отчёта по направлениям: `core.md` (корректность ядра), `integration.md` (Claude Code/Codex, упаковка, релиз),
`tests.md` (тесты), `quality.md` (качество кода и точность доков). Безопасность в аудит не входила.

Проверки координатора:
- `tests.md` №5 ложная: `internal/hooks/testdata/permission_request.json` читает `cmd/auto-context/main_test.go:1051`.
- `core.md` №2 воспроизведена: `auto-context ack typo` в неактивном git-репо выходит с кодом 1 и создаёт корневой
  `CLAUDE.md` с пустой секцией `auto-context:units`.
- Дубль: `integration.md` №6 и `quality.md` №2 — одна находка (`hooks/README.md` не знает `codex.json`).

План исправлений: `docs/superpowers/plans/2026-10-20-audit-fixes.md`.
