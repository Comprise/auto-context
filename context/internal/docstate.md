---
unit: internal/docstate
source_hash: v2:sha256:108c4c5c22947a913be5ad06a4c9c7b907921ed835ba86e35aae4b9f46daa6b3
commit: 5f376668531441d1e371d3591c3a533fbc709cac
updated: "2026-10-23"
---
# internal/docstate
Определяет, актуальны ли context-документы юнитов, и фиксирует актуальность.

## Назначение
Сравнивает отпечаток исходников юнита с `source_hash` из frontmatter его `context/<unit>.md`. Индексный файл юнита (`CLAUDE.md` или `AGENTS.md`) в отпечаток не входит.

Схема v2 (`HashPrefix = "v2:"`, запись `v2:sha256:<hex>`) считается по индексному виду дерева: `Tree.HashFiles` юнита, для `_root` ещё `Tree.HashUnits`. Неотслеживаемые файлы и юниты только из них в отпечаток не входят, поэтому в чистом клоне он тот же. Хэш без префикса — старая схема (v1.0.0): полный вид, все файлы и все юниты. Если он совпадает с посчитанным по старым правилам, юнит `ok` с нотой `NoteOldScheme`, иначе `stale`. Юнит только полного вида (одни неотслеживаемые файлы) хэширует пустой список.

## Публичный API / точки входа
- `StatusOf(ctx, root, t, c, unit)` — статус одного юнита (все юниты обходит `Project.Statuses`). `StatusOfDoc(ctx, root, t, c, unit, d)` — то же по уже прочитанному доку (`ReadDoc(root, unit)` → `DocText{Meta, Body, Err}`): `Statuses` читает каждый док один раз. Если хеширование прервано концом `ctx` по дедлайну (`context.DeadlineExceeded`), статус — `Stale`, `NoteHashError` с `Detail` = `TimeUp` («the hook's time is up»); при отмене (`context.Canceled`, Ctrl-C в CLI) — `Detail` = `Interrupted` («interrupted»); обе проверки идут до `fsutil.Category`.
- `UnitStatus{Unit, State, ContextPath, Summary, Note, Detail}`, `NoteText()`; `Note` — `NoteNotAcked`, `NoteOutside` (текст `unusable symlink`), `NoteBroken`, `NoteUnreadable`, `NoteHashError`, `NoteOldScheme` (текст `old hash scheme`, только при `OK`).
- `OldScheme(st)` — юнит актуален, но записан в старой схеме: ack только обновит схему.
- `SummaryOf(root, unit)` — summary дока без хэширования; `""`, если док не прочитать или он отклонён.
- `OutsideText` — описание отклонённого дока для сообщений хуков и CLI.
- `Doc(root, unit)` — чем является context-док: `DocAbsent`, `DocPresent`, `DocRefused` (ссылка наружу, в `.git`, на не-`.md`, висячая, не обычный файл), `DocUnreadable`.
- `AckIfNeeded(ctx, root, t, c, unit, now, head)` — записывает текущий отпечаток v2 во frontmatter и коммит, который вернёт `head` (nil или пусто — без `commit`); `written` — был ли файл переписан.
- Ошибка неизвестного юнита оборачивает `units.ErrUnknownUnit` (текст `unknown unit <u>`); без `context/_root.md` ack `_root` советует создать его (исключить `_root` нельзя).

## Инварианты и подводные камни
- Note бывает у `Stale` и (только `NoteOldScheme`) у `OK`. `Detail` у `NoteUnreadable` и `NoteHashError` — фиксированная категория `fsutil.Category`, а не текст ошибки: пути из репозитория в вывод не попадают. У `NoteOutside` нет Summary: чужой текст не попадает в индекс. Док, ведущий на каталог или другой не обычный файл, — `NoteOutside`, как `DocRefused` у `Doc`.
- Ошибка одного юнита не влияет на остальные.
- Отпечаток `_root` включает список остальных юнитов индексного вида: их добавление или удаление делает `_root` stale.
- `AckIfNeeded` всегда пишет схему v2 (док старой схемы переписывается) и не переписывает док, где уже записан тот же отпечаток того же юнита: повторный ack (авто-ack на каждом Stop, параллельная сессия) не выглядит правкой доков; `commit` тогда не обновляется, а `head` не вызывается. Ack не создаёт документ; сохранение кеша — за вызывающим.

## Связи (зависит от / используется в)
Зависит от: `internal/frontmatter`, `internal/fsutil`, `internal/hashing`, `internal/units`.
Используется в: `cmd/auto-context`, `internal/hooks`, `internal/project`.
