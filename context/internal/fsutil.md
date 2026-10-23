---
unit: internal/fsutil
source_hash: sha256:db67ea4f392f789ab18259bdabf78b4479215ad86da45ac8142740318a121be2
commit: b986c6a7235d656f67c8bd59c79ab4f8bd420c14
updated: "2026-10-23"
---
# internal/fsutil
Политика доверия к путям, чтение с лимитом, атомарная запись, `.auto-context/state`.

## Назначение
Все чтения и записи хуков и CLI проходят здесь: репозиторий может быть враждебным клоном, а хуки срабатывают сами.

## Публичный API / точки входа
- `Resolve(root, path)` — раскрывает симлинки; результат внутри root, не в `.git/` и не в `StateDir` извне (и из `StateDir` не наружу), иначе `ErrOutside`. Идемпотентен, принимает путь и под раскрытым root; относительный путь — ошибка вызывающего (не `ErrOutside`). Для пути под корнем без симлинков среди существующих компонентов `EvalSymlinks` не вызывается (корень раскрывается один раз и кэшируется).
- `ResolveDoc` — то же для документов: симлинк только на `.md`.
- `ReadFile`, `ReadDoc` — только обычный файл не больше `max` (`ErrNotRegular`, `ErrTooLarge`), FIFO отклоняется до открытия.
- `WriteAtomic` (с fsync), `WriteDoc` (по правилу `ResolveDoc`), `WriteState` (без fsync, для восстановимого состояния).
- `Category(err)` — фиксированная категория ошибки для вывода: `ErrOutside` → «it leads outside the repository or into .git», `ErrNotRegular` → «not a regular file», `ErrTooLarge` → «too large», `os.ErrPermission` → «permission denied», `os.ErrNotExist` → «missing», иначе «unreadable». Текст `err` в результат не попадает.
- `Abs(root, rel)` — slash-путь относительно root в путь ОС; `StatePath(root, elem...)` — путь служебного файла в `StateDir`.
- `EnsureStateDir(root)` — `.auto-context/state` и `.auto-context/.gitignore`: всё в каталоге вне git, кроме `config.yaml` и самого `.gitignore`; файл без строк `/*` и `!/config.yaml` (например, просто `*`) перезаписывается, правка пользователя с ними остаётся.

## Инварианты и подводные камни
- Правило вывода: тексты ошибок fsutil содержат пути из репозитория, поэтому в stdout/stderr, `additionalContext` и `systemMessage` идёт только `Category(err)`; полный текст — в `.auto-context/log`.
- Проверка «путь выходит наружу» — `!filepath.IsLocal(rel)`; чтение выделяет буфер по `Stat` (`size+512`) и читает не больше `limit+1` байт.
- `.git` и `.auto-context` сравниваются без учёта регистра и через `os.SameFile` (`.GIT` на macOS/Windows).
- Компоненты с `ModeIrregular` (junction и mount point на Windows) отклоняются.
- Раскрытый root и проверка верхних имён кешируются на процесс.
- Окно TOCTOU между проверкой и записью есть: защита — от содержимого репозитория, не от параллельного злоумышленника.
- Политика покрывает служебные и документные пути; исходники `units` и `hashing` читают с Lstat, без `Resolve`.

## Связи
Зависит от: только стандартная библиотека.
Используется в: `internal/config`, `internal/docstate`, `internal/frontmatter`, `internal/hashing`, `internal/hooks`, `internal/logx`, `internal/project`, `internal/rootindex`, `internal/session`, `internal/units`.
