# internal/hashing
Хеш исходников юнита (source_hash) с кешем по mtime и размеру.

- `hashing.go` — отпечаток исходников юнита, хеширование файлов, `Prewarm`
- `cache.go` — кэш по mtime/size: бинарный файл `cache.v5.bin`, загрузка, слияние при сохранении под блокировкой

Подробно: context/internal/hashing.md
