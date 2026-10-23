# internal/filelock
Короткие эксклюзивные блокировки файлов для параллельных процессов хуков (ротация лога, кэш хэшей).

- `filelock.go` — `Lock(path)` и время ожидания `Wait`
- `lock_flock.go`, `lock_excl.go`, `lock_other.go` — flock на unix, файл с O_EXCL на остальных ОС

Подробно: context/internal/filelock.md
