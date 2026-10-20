# auto-context
Плагин Claude Code и Codex CLI на Go: держит индексные файлы юнитов и библиотеку context/ в синхроне с кодом.

- `cmd/auto-context/` — бинарник (hook, ack, status, changes, units, version)
- `internal/` — пакеты ядра; `hooks/`, `skills/`, `bin/`, `.claude-plugin/`, `.codex-plugin/`, `.agents/` — упаковка плагина
- `docs/superpowers/` — спецификация и план; `Makefile` — `make test`, `make dist`

Подробно: context/_root.md

<!-- code-review-graph MCP tools -->
## MCP Tools: code-review-graph

**This project has a knowledge graph. Start with the code-review-graph
MCP tools to narrow scope, then read the source.** The graph is cheaper than scanning files and
gives you structural context (callers, dependents, test coverage) that file search cannot.

### When to use graph tools FIRST

- **Exploring code**: `semantic_search_nodes_tool` or `query_graph_tool` instead of Grep
- **Understanding impact**: `get_impact_radius_tool` instead of manually tracing imports
- **Code review**: `detect_changes_tool` + `get_review_context_tool` instead of reading entire files
- **Finding relationships**: `query_graph_tool` with callers_of/callees_of/imports_of/tests_for
- **Architecture questions**: `get_architecture_overview_tool` + `list_communities_tool`

### Verify in the source

- Narrow scope with the graph, then read the source. Do not change code from graph output alone.
- For any non-trivial change, read the implementation and the relevant tests before concluding.
- Verify the exact source when touching behavior, database logic, migrations, retries, fallbacks,
  recovery, or compatibility code.
- When the graph and the source disagree, the source wins. The graph may be stale or may not
  model that relationship.
- An empty graph result can mean "not indexed" or "not statically visible", not "does not exist".

### Key Tools

| Tool | Use when |
| ------ | ---------- |
| `detect_changes_tool` | Reviewing code changes — gives risk-scored analysis |
| `get_review_context_tool` | Need source snippets for review — token-efficient |
| `get_impact_radius_tool` | Understanding blast radius of a change |
| `get_affected_flows_tool` | Finding which execution paths are impacted |
| `query_graph_tool` | Tracing callers, callees, imports, tests, dependencies |
| `semantic_search_nodes_tool` | Finding functions/classes by name or keyword |
| `get_architecture_overview_tool` | Understanding high-level codebase structure |
| `refactor_tool` | Planning renames, finding dead code |

### Workflow

1. The graph auto-updates on file changes (via hooks).
2. Use `detect_changes_tool` for code review.
3. Use `get_affected_flows_tool` to understand impact.
4. Use `query_graph_tool` pattern="tests_for" to check coverage.
<!-- /code-review-graph MCP tools -->

<!-- auto-context:units -->
## Юниты (подробно — context/<unit>.md)
- `cmd/auto-context` — CLI-бинарь: запуск хуков Claude Code и Codex и команды ack/status/changes/units/version.
- `internal/config` — Читает, валидирует и правит необязательный `.auto-context/config.yaml`, ищет корень репозитория.
- `internal/docstate` — Определяет, актуальны ли context-документы юнитов, и фиксирует актуальность.
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
- `internal/units` — Находит юниты документации в репозитории и классифицирует пути.
<!-- /auto-context:units -->
