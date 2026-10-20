---
unit: internal/frontmatter
source_hash: sha256:2bf33941928e5d4629b8688332d7f482c526afdc9888cec286588e4096c988fb
updated: "2026-10-20"
---
# internal/frontmatter
Читает и пишет YAML-блок меты в контекстных файлах.

## Назначение
Отпечаток и дату ack во frontmatter пишет только бинарь (`ack` и авто-ack Stop); пакет изолирует разбор и перезапись блока.

## Публичный API / точки входа
- `Meta{Unit, SourceHash, Updated}`, `MaxSize` (1 МиБ).
- `Read(root, path)` — через `fsutil.ReadDoc`; без нашего блока — `ErrNoFrontmatter` и всё содержимое; с битым — `ErrBroken`.
- `SetHash(root, path, unit, hash, today)` — переписывает наш блок или добавляет его перед содержимым, через `fsutil.WriteDoc`.
- `Summary(body)` — первая непустая строка после первого `# `-заголовка.

## Инварианты и подводные камни
- Блок наш, только если в нём есть строка `unit:`, `source_hash:` или `updated:`; иной ведущий блок `---…---` — текст пользователя, не заменяется.
- `Summary` пуст, если сразу после заголовка идёт заголовок.
- `SetHash` пишет блок и строки YAML с теми же концами строк, что первая строка файла (CRLF или LF); тело не меняется.

## Связи
Зависит от: `internal/fsutil`; внешняя `yaml.v3`.
Используется в: `internal/docstate`, `internal/project`.
