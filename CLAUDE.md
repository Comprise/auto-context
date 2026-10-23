# auto-context
Плагин Claude Code и Codex CLI на Go: держит индексные файлы юнитов и библиотеку context/ в синхроне с кодом.

- `cmd/auto-context/` — бинарник (hook, ack, status, changes, units, version)
- `internal/` — пакеты ядра; `hooks/`, `skills/`, `bin/`, `.claude-plugin/`, `.codex-plugin/`, `.agents/` — упаковка плагина
- `docs/superpowers/` — спецификация и план; `Makefile` — `make test`, `make dist`

Подробно: context/_root.md

<!-- auto-context:units -->
## Юниты (подробно — context/<unit>.md)
- `cmd/auto-context` — CLI-бинарь: запуск хуков Claude Code и Codex и команды ack/status/changes/units/version.
- `internal/config` — Читает, валидирует и правит необязательный `.auto-context/config.yaml`.
- `internal/docstate` — Определяет, актуальны ли context-документы юнитов, и фиксирует актуальность.
- `internal/filelock` — Короткие эксклюзивные блокировки файлов для параллельных процессов хуков.
- `internal/filestat` — Отпечаток файла для обнаружения изменений (mtime и размер) — общий тип для обнаружения, снимка сессии и хуков.
- `internal/frontmatter` — Читает и пишет YAML-блок меты в контекстных файлах.
- `internal/fsutil` — Политика доверия к путям, чтение с лимитом, атомарная запись, `.auto-context/state`.
- `internal/gitx` — Единый запуск git для плагина с фиксированной защитой.
- `internal/hashing` — Считает source_hash юнита по списку файлов с кешем mtime/size.
- `internal/hooks` — Обработчики хуков Claude Code и Codex: жизненный цикл хода, обратная связь о доках.
- `internal/logx` — Дописывает ошибки хуков в `.auto-context/log`, никогда не падая.
- `internal/project` — Сервисный слой: открывает репозиторий и даёт хукам и CLI общие операции.
- `internal/rootindex` — Генерирует и обновляет секцию оглавления юнитов в корневом индексном файле (CLAUDE.md или AGENTS.md).
- `internal/session` — Файлы сессии одного хода: снимок отслеживаемых файлов и журнал Stop.
- `internal/testutil` — Общие хелперы тестов: чистое окружение, запись файлов, запуск git.
- `internal/textx` — Очистка текста из репозитория перед выводом в лог, терминал или контекст Claude.
- `internal/turn` — Правила хода: что Stop считает обновлением доков, когда допустим auto-ack, какие юниты требуют напоминания.
- `internal/units` — Находит юниты документации в репозитории и классифицирует пути.
<!-- /auto-context:units -->
