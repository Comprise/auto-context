---
unit: internal/fsutil
source_hash: v2:sha256:0cccabd13af895595e615644edf272d27d5942a4cb521a9294f226522af4cbe6
commit: c44126d5f7bca77708b36833c62e2266e53df7a0
updated: "2026-10-24"
---
# internal/fsutil
Политика доверия к путям, чтение с лимитом, атомарная запись, `.auto-context/state`.

## Назначение
Все чтения и записи хуков и CLI проходят здесь: репозиторий может быть враждебным клоном, а хуки срабатывают сами.

## Публичный API / точки входа
- `Resolve(root, path)` — раскрывает симлинки; результат внутри root, не в `.git/` и не в `StateDir` извне (и из `StateDir` не наружу), иначе `ErrOutside`. Идемпотентен, принимает путь и под раскрытым root; относительный путь — ошибка вызывающего (не `ErrOutside`). Для пути под корнем без симлинков среди существующих компонентов `EvalSymlinks` не вызывается (корень раскрывается один раз и кэшируется).
- `ResolveDoc` — то же для документов: симлинк только на `.md`.
- `ReadFile`, `ReadDoc` — только обычный файл не больше `max` (`ErrNotRegular`, `ErrTooLarge`). FIFO, устройство, сокет или файл больше лимита отклоняются до открытия (`Stat`); файл открывается с `O_NONBLOCK|O_NOCTTY` (unix), и режим и размер открытого файла (`f.Stat`) проверяются ещё раз, так что FIFO, подменённый после проверки, не вешает чтение.
- `WriteAtomic` (с fsync), `WriteDoc` (по правилу `ResolveDoc`), `WriteState` (без fsync, для восстановимого состояния). Временный файл рядом с целью (`<имя>.tmp<случайное>`, `O_CREATE|O_EXCL`), затем `f.Chmod` до закрытия и `Rename` на раскрытое имя: запись через симлинк `CLAUDE.md → AGENTS.md` меняет `AGENTS.md`, симлинк остаётся. На Windows замена файла, открытого другим процессом (параллельный хук, редактор), отказывает с «Access is denied»: `rename` (`rename_windows.go`) повторяет её до 500 мс (`renameWait`); на остальных ОС (`rename_other.go`) — обычный `Rename`.
- `Category(err)` — фиксированная категория ошибки для вывода: `ErrOutside` → «it leads outside the repository or into .git», `ErrNotRegular` → «not a regular file», `ErrTooLarge` → «too large», `os.ErrPermission` → «permission denied», `os.ErrNotExist` → «missing», иначе «unreadable». Текст `err` в результат не попадает.
- `Abs(root, rel)` — slash-путь относительно root в путь ОС; `StatePath(root, elem...)` — путь служебного файла в `StateDir`.
- `EnsureStateDir(root)` — `.auto-context/state` и `.auto-context/.gitignore`: всё в каталоге вне git, кроме `config.yaml` и самого `.gitignore`; файл без строк `/*` и `!/config.yaml` (например, просто `*`) перезаписывается, правка пользователя с ними остаётся.

## Инварианты и подводные камни
- Правило вывода: тексты ошибок fsutil содержат пути из репозитория, поэтому в stdout/stderr, `additionalContext` и `systemMessage` идёт только `Category(err)`; полный текст — в `.auto-context/log`.
- Проверка «путь выходит наружу» — `!filepath.IsLocal(rel)`; чтение выделяет буфер по `Stat` (`size+512`) и читает не больше `limit+1` байт.
- `.git` и `.auto-context` сравниваются без учёта регистра и через `os.SameFile` (`.GIT` на macOS/Windows).
- Компоненты с `ModeIrregular` (junction и mount point на Windows) отклоняются.
- Раскрытый root и проверка верхних имён кешируются на процесс.
- `Resolve` — политика, ввод-вывод — через `os.OpenRoot(раскрытый root)`, открываемый на каждую операцию и закрываемый после неё (без кэша: дескриптор устаревал бы при пересоздании каталога и держал бы его занятым на Windows), по пути относительно root: подмена каталога или симлинка после проверки не уводит чтение и запись за корень. Правила `.git`, state-dir и `ResolveDoc` `os.Root` не знает, их окно TOCTOU остаётся: защита — от содержимого репозитория, не от параллельного злоумышленника.
- Ошибки `os.Root` содержат путь относительно root; `absErr` возвращает в них полный путь (для лога).
- Политика покрывает служебные и документные пути; исходники `units` и `hashing` читают с Lstat, без `Resolve`.

## Связи
Зависит от: только стандартная библиотека.
Используется в: `internal/config`, `internal/docstate`, `internal/frontmatter`, `internal/hashing`, `internal/hooks`, `internal/logx`, `internal/project`, `internal/rootindex`, `internal/session`, `internal/units`.
