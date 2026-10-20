---
unit: internal/fsutil
source_hash: sha256:428697f815cfd464f72f143ea5544d82dbdd594841ccdd642571733c5bfbac24
updated: "2026-10-20"
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
- `Abs(root, rel)` — slash-путь относительно root в путь ОС; `StatePath(root, elem...)` — путь служебного файла в `StateDir`.
- `EnsureStateDir(root)` — `.auto-context/state` и `.auto-context/.gitignore`: всё в каталоге вне git, кроме `config.yaml` и самого `.gitignore`; файл без строк `/*` и `!/config.yaml` (например, просто `*`) перезаписывается, правка пользователя с ними остаётся.

## Инварианты и подводные камни
- `.git` и `.auto-context` сравниваются без учёта регистра и через `os.SameFile` (`.GIT` на macOS/Windows).
- Компоненты с `ModeIrregular` (junction и mount point на Windows) отклоняются.
- Раскрытый root и проверка верхних имён кешируются на процесс.
- Окно TOCTOU между проверкой и записью есть: защита — от содержимого репозитория, не от параллельного злоумышленника.
- Политика покрывает служебные и документные пути; исходники `units` и `hashing` читают с Lstat, без `Resolve`.

## Связи
Зависит от: только стандартная библиотека.
Используется в: `internal/config`, `internal/docstate`, `internal/frontmatter`, `internal/hashing`, `internal/hooks`, `internal/logx`, `internal/project`, `internal/rootindex`, `internal/session`, `internal/units`.
