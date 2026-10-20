---
unit: internal/session
source_hash: sha256:c21e799151c8993ee2c9416ee7fc783e1cb684bb8321f66db89a41c8fff3a034
updated: "2026-10-20"
---
# internal/session
Файлы сессии одного хода: снимок отслеживаемых файлов и журнал Stop.

## Назначение
PromptSubmit сохраняет снимок (mtime/size отслеживаемых файлов), Stop сравнивает с ним текущее состояние и ведёт журнал хода: какой код менялся, какие доки трогались, о чём уже напомнили. Файлы — `.auto-context/state/<id>.snapshot` и `<id>.json`, где `<id>` — `SanitizeID`.

## Публичный API / точки входа
- `Snapshot` — путь → `units.Stat`; `FromTree` берёт копию из `Tree.Stats()` без второго копирования; `Diff` — изменённые, удалённые, добавленные пути.
- `SnapshotTime` — mtime файла снимка в ns, то есть начало хода; нет файла — ошибка с `os.ErrNotExist`.
- `LoadSnapshot` / `Save`; снимок без заголовка или с битой записью — ошибка с путём файла.
- `Journal`: `LoadJournal`, `Save`, `AddCode`/`AddDocs`/`Notify`, `Pause`/`Paused` (конфиг ломался в этой сессии; снимок до паузы, если он есть, сохраняется), `MarkConfigNotified`/`ConfigNotified` (Stop уже сказал об этом), `Resume` (снимает паузу и сбрасывает напоминания), `NotifyAckFailed`/`AckFailNotified` (о неудачном авто-ack сказано в этом ходе); `Clear` — удалить файлы сессии.
- `RemoveStale(root, maxAge, now)` — все обычные файлы state-dir старше `maxAge`; каталоги и их содержимое не трогает.

## Инварианты и подводные камни
- Формат снимка — заголовок `auto-context snapshot 1` и записи `<mtime> <size> <путь>\0`: пути могут содержать пробелы и переводы строк.
- Списки журнала — отсортированные множества: `LoadJournal` и `Save` нормализуют их, как бы журнал ни был собран.
- `Clear` удаляет только после `fsutil.Resolve`: state-dir, ведущий наружу, не даёт удалить чужой файл.
- Файлы сессии удаляет SessionEnd; оставшиеся от сессий без SessionEnd (сбой, kill) — `RemoveStale` в SessionStart.
- Запись без fsync (`fsutil.WriteState`): файлы восстановимы. Лимит чтения — `MaxSize` (256 МиБ).
- Сравнение по mtime+size: правка того же размера в ту же наносекунду не видна.

## Связи
Зависит от: `internal/fsutil`, `internal/units`.
Используется в: `internal/hooks`.
