---
unit: internal/docstate
source_hash: sha256:ae04c671bd4f3de67e8daabbf8d9fd8b1ba39f3c09d965fd1120dc4ad4e81e7e
updated: "2026-10-20"
---
# internal/docstate
Определяет, актуальны ли context-документы юнитов, и фиксирует актуальность.

## Назначение
Сравнивает отпечаток исходников юнита с `source_hash` из frontmatter его `context/<unit>.md`.

## Публичный API / точки входа
- `StatusOf(root, t, c, unit)` — статус одного юнита (все юниты обходит `Project.Statuses`).
- `UnitStatus{Unit, State, ContextPath, Summary, Note, Detail}`, `NoteText()`; `Note` — `NoteNotAcked`, `NoteOutside`, `NoteBroken`, `NoteUnreadable`, `NoteHashError`.
- `OutsideText` — описание отклонённого дока для сообщений хуков и CLI.
- `Doc(root, unit)` — чем является context-док: `DocAbsent`, `DocPresent`, `DocRefused` (ссылка наружу, в `.git`, на не-`.md`, висячая, не обычный файл), `DocUnreadable`.
- `Ack(root, t, c, unit, now)` — записывает текущий отпечаток во frontmatter.

## Инварианты и подводные камни
- Note бывает только у `Stale`; `Detail` — текст ошибки у `NoteUnreadable` и `NoteHashError`. У `NoteOutside` нет Summary: чужой текст не попадает в индекс.
- Ошибка одного юнита не влияет на остальные.
- Отпечаток `_root` включает список остальных юнитов: их добавление или удаление делает `_root` stale.
- `Ack` не переписывает док, где уже записан тот же отпечаток того же юнита: повторный ack (авто-ack на каждом Stop, параллельная сессия) не выглядит правкой доков. `Ack` не создаёт документ; сохранение кеша — за вызывающим.

## Связи (зависит от / используется в)
Зависит от: `internal/frontmatter`, `internal/fsutil`, `internal/hashing`, `internal/units`.
Используется в: `cmd/auto-context`, `internal/hooks`, `internal/project`.
