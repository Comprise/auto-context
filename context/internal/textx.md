---
unit: internal/textx
source_hash: sha256:fe16edee643d7280be9fb90ebe4d06ab4383110fce04e3fec298c3bf0e3d7f2a
updated: "2026-10-20"
---
# internal/textx
Очистка текста из репозитория перед выводом в лог, терминал или контекст Claude.

## Назначение
Одно правило для всех мест, куда попадает чужой текст (ошибки с путями, summary, сообщения git): без управляющих и форматных символов, ограниченной длины.

## Публичный API / точки входа
- `Clean(s)` — управляющие и форматные символы (переводы строк, ANSI, bidi) в пробелы.
- `Cap(s, max)` — первые `max` рун и `…`, если строка длиннее.
- `Line(s, max)` — `Clean`, свёрнутые пробелы, `Cap`.
- `Err(err)` — текст ошибки одной строкой до 200 рун.

## Связи
Зависит от: только стандартная библиотека.
Используется в: `cmd/auto-context`, `internal/hooks`, `internal/logx`, `internal/project`, `internal/rootindex`, `internal/units`.
