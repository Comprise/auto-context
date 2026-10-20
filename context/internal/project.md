---
unit: internal/project
source_hash: sha256:953e615099ff174dfd879e748d6a24366deddf5a6de0da90fc64972359931cfc
updated: "2026-10-20"
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
- Статусы, ack (`Ack`, `AckTouched` — для авто-ack: оглавление обновляется при любом затронутом доке — summary мог измениться без смены хеша, — его ошибка возвращается отдельно), оглавление (`UpdateIndex` с полным списком статусов, `RefreshIndex`; в неактивном проекте `UpdateIndex` ничего не читает и не пишет), `Exclude`, `SaveCache`, доки-сироты (`Orphans`), отчёт `changes` (`WriteChanges`).

## Инварианты и подводные камни
- При `ConfigBroken` `Ack`, `UpdateIndex` и `WriteChanges` отказывают (`ErrConfigBroken`): юниты по умолчанию не те.
- `Statuses` сначала параллельно хеширует изменившиеся файлы (`Cache.Prewarm`) и сохраняет кэш каждые 5 с: хук, убитый по таймауту, не теряет работу.
- `SaveCache` ничего не пишет в неактивном проекте, при `.auto-context`, ведущем наружу (ошибка `StateDirErr`), и если кэш не загружался (PromptSubmit и большинство Stop).
- `Exclude` принимает только существующий каталог, не `_root`; ошибка разрешения пути сообщается как есть.
- `Open` ничего не пишет; state-dir готовит `hooks.load`.
- `WriteChanges`: пути относительно корня (`:(literal)<unit>`, `.` для `_root`, `--relative` у `log`/`diff`); без коммитов — только `git status` (новые и изменённые файлы); сбой `rev-parse HEAD`, кроме «HEAD нет», — заметка `(git failed: …)`. Сравнение с рабочим деревом — только если все `filter.*` из глобального конфига или командной строки (`trustedFilters`, `--includes`, при ошибке — отказ), без захода в сабмодули.

## Связи
Зависит от: `internal/config`, `internal/docstate`, `internal/frontmatter`, `internal/fsutil`, `internal/gitx`, `internal/hashing`, `internal/rootindex`, `internal/textx`, `internal/units`.
Используется в: `cmd/auto-context`, `internal/hooks`.
