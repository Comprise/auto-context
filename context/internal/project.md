---
unit: internal/project
source_hash: sha256:a560ad93375422d9a9f21d5ae2d48afdf40fec5833e66a94b00f2f3ac07cbc28
updated: "2026-10-21"
---
# internal/project
Сервисный слой: открывает репозиторий и даёт хукам и CLI общие операции.

## Назначение
Хукам и CLI нужна одна последовательность «конфиг → обход → кэш → статусы → оглавление → кэш на диск» и одно правило, когда в проект можно писать; пакет держит их в одном месте.

## Публичный API / точки входа
- `Active(root)` — проект включён: `.auto-context/config.yaml` или `context/_root.md`, обычный файл внутри репозитория; `ActivationRefused(root)` — метка есть, но ведёт наружу, не обычный файл или недоступна.
- `Open(root)` → `*Project`, только для чтения (кроме кэша, загружаемого лениво); геттеры `Root()`, `IndexFile()` — имя индексного файла из конфига, `Config()`, `Tree()`, `ConfigProblems()` — отброшенные настройки, `ConfigErr()` — конфиг не прочитан (`ConfigBroken()`).
- `Cache()` — кэш отпечатков, загружается при первом обращении; `CacheErr()` — почему он непригоден. `ConfigIgnored()` — конфиг под `.gitignore`, ошибка — сбой git, кроме «не репозиторий» и «нет git»; `StateDirErr()` — `.auto-context` ведёт наружу.
- `Prewarm(files)` — параллельное хеширование изменённых файлов с периодическим сохранением кэша; `ShowNames(paths)` — пути, которые можно показать Claude (`units.SafeName`, не больше 20), и число остальных.
- Статусы, ack (`Ack` — пишет и HEAD в `commit`: один `git rev-parse --verify -q HEAD` на `Project`, лениво и только при перезаписи дока, в пределах дедлайна хука; при сбое git или без коммитов `commit` не пишется, ack не падает; `AckTouched` — для авто-ack: оглавление обновляется при любом затронутом доке — summary мог измениться без смены хеша, — его ошибка возвращается отдельно), оглавление (`UpdateIndex` с полным списком статусов, `RefreshIndex`; в неактивном проекте `UpdateIndex` ничего не читает и не пишет), `Exclude`, `SaveCache`, доки-сироты (`Orphans`), отчёт `changes` (`WriteChanges`).

## Инварианты и подводные камни
- При `ConfigBroken` `Ack`, `UpdateIndex` и `WriteChanges` отказывают (`ErrConfigBroken`): юниты по умолчанию не те.
- `Statuses` сначала параллельно хеширует изменившиеся файлы (`Cache.Prewarm`) и сохраняет кэш каждые 5 с: хук, убитый по таймауту, не теряет работу.
- `SaveCache` ничего не пишет в неактивном проекте, при `.auto-context`, ведущем наружу (ошибка `StateDirErr`), и если кэш не загружался (PromptSubmit и большинство Stop).
- `Exclude` принимает только существующий каталог, не `_root`; ошибка разрешения пути сообщается как есть.
- `Open` ничего не пишет; state-dir готовит `hooks.load`.
- `WriteChanges`: коммиты — `git log <commit>..HEAD`, если `commit` из frontmatter — полный hex-id в нижнем регистре (40 или 64 символа; иное из дока до git не доходит) и предок HEAD (`merge-base --is-ancestor`); иначе (поля нет, коммит ушёл после rebase — выход 1; неизвестный коммит, мелкий клон, тайм-аут — другой код, своя пометка) — `--since=<updated>` по дате коммита, и заголовок секции предупреждает, что позже влитые старые коммиты не видны. Пути относительно корня (`:(literal)<unit>`, `.` для `_root`, `--relative` у `log`/`diff`); без коммитов — только `git status` (новые и изменённые файлы); сбой `rev-parse HEAD`, кроме «HEAD нет», — заметка `(git failed: …)`; первая ошибка записи в `w` возвращается. Вывод git проходит через `textx.StripControls`: в темах коммитов могут быть escape-последовательности. Сравнение с рабочим деревом — только если все `filter.*` из глобального конфига или командной строки (`trustedFilters`, `--includes`, при ошибке — отказ), без захода в сабмодули.

## Связи
Зависит от: `internal/config`, `internal/docstate`, `internal/frontmatter`, `internal/fsutil`, `internal/gitx`, `internal/hashing`, `internal/rootindex`, `internal/textx`, `internal/units`.
Используется в: `cmd/auto-context`, `internal/hooks`.
