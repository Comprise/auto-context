# internal/logx
Тихий журнал ошибок хуков в `.auto-context/log` с ротацией.

- `logx.go` — запись ошибок в лог с ротацией, подсчёт записей
- `lock_flock.go`, `lock_excl.go`, `lock_other.go` — блокировка ротации: flock на unix, файл с O_EXCL на остальных ОС

Подробно: context/internal/logx.md
