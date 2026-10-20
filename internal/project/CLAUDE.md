# internal/project
Сервисный слой: открывает репозиторий и даёт хукам и CLI общие операции.

- `project.go` — активация, открытие репозитория и общие операции: статусы, ack, оглавление, exclude, кэш
- `changes.go` — отчёт `auto-context changes`: фиксированные вызовы git

Подробно: context/internal/project.md
