# internal/fsutil
Политика доверия к путям, чтение с лимитом, атомарная запись, `.auto-context/state`.

- `fsutil.go` — политика путей, чтение с лимитом, атомарная запись, state-dir
- `nonblock_unix.go`, `nonblock_other.go` — флаги открытия для чтения (`O_NONBLOCK|O_NOCTTY` на unix)

Подробно: context/internal/fsutil.md
