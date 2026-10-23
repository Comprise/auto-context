# internal/gitx
Единый запуск git для плагина с фиксированной защитой.

- `gitx.go` — запуск git с защитой и разбор его ошибок
- `proc_unix.go`, `proc_other.go` — группа процессов git и её убийство по таймауту
- `proc_linux.go`, `proc_unix_other.go` — атрибуты процесса git: своя группа, на Linux `Pdeathsig`
- `topology.go` — `NotRepo` и проверка топологии `WorkTreeOK`
- `gitx_unix_test.go` — тест убийства группы процессов (только Unix)
- `gitx_linux_test.go` — git умирает вместе с убитым хуком (только Linux)

Подробно: context/internal/gitx.md
