# internal/fsutil
Политика доверия к путям, чтение с лимитом, атомарная запись, `.auto-context/state`.

- `fsutil.go` — политика путей, чтение с лимитом, атомарная запись, state-dir
- `nonblock_unix.go`, `nonblock_other.go` — флаги открытия для чтения (`O_NONBLOCK|O_NOCTTY` на unix)
- `rename_windows.go`, `rename_other.go` — замена файла при атомарной записи (на Windows с повтором, пока цель открыта другим процессом)

Подробно: context/internal/fsutil.md
