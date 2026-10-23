# internal/gitx
Единый запуск git для плагина с фиксированной защитой.

- `gitx.go` — запуск git с защитой и разбор его ошибок
- `proc_unix.go`, `proc_other.go` — группа процессов git и её убийство по таймауту
- `topology.go` — `NotRepo` и проверка топологии `WorkTreeOK`
- `gitx_unix_test.go` — тест убийства группы процессов (только Unix)

Подробно: context/internal/gitx.md
